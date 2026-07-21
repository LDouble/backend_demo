package permission

import (
	"context"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/weouc-plus/campus-platform/internal/core/idempotency"
	"github.com/weouc-plus/campus-platform/internal/core/model"
	"go.uber.org/zap"
	"gorm.io/gorm"
)

type memoryNotifier struct {
	mu          sync.Mutex
	subscribers []func(string) error
	ready       chan struct{}
	fail        bool
}

func newMemoryNotifier() *memoryNotifier { return &memoryNotifier{ready: make(chan struct{}, 4)} }

func (n *memoryNotifier) Publish(_ context.Context, version string) error {
	n.mu.Lock()
	if n.fail {
		n.mu.Unlock()
		return errors.New("publish failed")
	}
	subscribers := append([]func(string) error(nil), n.subscribers...)
	n.mu.Unlock()
	for _, consume := range subscribers {
		if err := consume(version); err != nil {
			return err
		}
	}
	return nil
}

func (n *memoryNotifier) Run(ctx context.Context, consume func(string) error) error {
	n.mu.Lock()
	n.subscribers = append(n.subscribers, consume)
	n.mu.Unlock()
	n.ready <- struct{}{}
	<-ctx.Done()
	return nil
}

type syncRoleRepository struct{ db *gorm.DB }

func (r syncRoleRepository) Create(ctx context.Context, role *model.Role) error {
	return r.db.WithContext(ctx).Create(role).Error
}
func (r syncRoleRepository) Get(ctx context.Context, id uint64) (*model.Role, error) {
	var role model.Role
	return &role, r.db.WithContext(ctx).First(&role, id).Error
}
func (r syncRoleRepository) GetByName(ctx context.Context, name string) (*model.Role, error) {
	var role model.Role
	return &role, r.db.WithContext(ctx).Where("name = ?", name).First(&role).Error
}
func (r syncRoleRepository) List(ctx context.Context, page, size int) ([]model.Role, int64, error) {
	var roles []model.Role
	var total int64
	db := r.db.WithContext(ctx).Model(&model.Role{})
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	return roles, total, db.Offset((page - 1) * size).Limit(size).Find(&roles).Error
}
func (r syncRoleRepository) UpdateDescription(ctx context.Context, id uint64, description string) error {
	return r.db.WithContext(ctx).Model(&model.Role{}).Where("id = ?", id).Update("description", description).Error
}
func (r syncRoleRepository) Delete(ctx context.Context, id uint64) error {
	return r.db.WithContext(ctx).Delete(&model.Role{}, id).Error
}

func newSyncService(t *testing.T, db *gorm.DB) *Service {
	t.Helper()
	service, err := NewService(context.Background(), db, syncRoleRepository{db: db})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func TestPolicyOutboxRelayAndCrossInstanceReload(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "policy-sync.db")), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&model.User{}, &model.Role{}, &policyRule{}, &policyOutbox{}); err != nil {
		t.Fatal(err)
	}
	writer := newSyncService(t, db)
	reader := newSyncService(t, db)
	writer.WithLogger(zap.NewNop())
	if err = writer.ReloadPolicy(context.Background()); err != nil {
		t.Fatal(err)
	}
	role, err := writer.CreateRole(context.Background(), "sync_reader", "", false)
	if err != nil {
		t.Fatal(err)
	}
	if err = writer.EnsureMemberForUser(context.Background(), 7); err != nil {
		t.Fatal(err)
	}
	if err = writer.SetUserRoles(context.Background(), 7, []string{role.Name}); err != nil {
		t.Fatal(err)
	}
	if allowed, _ := reader.Enforce(context.Background(), 7, "/api/v1/sync/1", "GET"); allowed {
		t.Fatal("reader unexpectedly observed policy before notification")
	}

	notifier := newMemoryNotifier()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		reader.runReloadLoop(ctx, notifier)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
	select {
	case <-notifier.ready:
	case <-time.After(time.Second):
		t.Fatal("subscriber did not start")
	}
	if err = writer.SetPermissions(context.Background(), role.ID, []Permission{{PathPattern: "/api/v1/configs/:id", Methods: []string{"GET"}}}); err != nil {
		t.Fatal(err)
	}
	if err = db.Transaction(func(tx *gorm.DB) error {
		txCtx := idempotency.WithTransaction(context.Background(), tx)
		roles, getErr := writer.GetUserRoles(txCtx, 7)
		if getErr != nil || len(roles) != 2 {
			t.Fatalf("transaction roles=%v err=%v", roles, getErr)
		}
		permissions, getErr := writer.GetPermissions(txCtx, role.ID)
		if getErr != nil || len(permissions) != 1 {
			t.Fatalf("transaction permissions=%v err=%v", permissions, getErr)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err = writer.relayPolicyChanges(context.Background(), notifier); err != nil {
		t.Fatal(err)
	}
	if stats := writer.SyncStats(); stats.Published == 0 {
		t.Fatalf("sync stats=%+v", stats)
	}
	if allowed, err := reader.Enforce(context.Background(), 7, "/api/v1/configs/1", "GET"); err != nil || !allowed {
		t.Fatalf("cross-instance reload allowed=%v err=%v", allowed, err)
	}
	var pending int64
	if err = db.Model(&policyOutbox{}).Where("dispatched_at IS NULL").Count(&pending).Error; err != nil || pending != 0 {
		t.Fatalf("pending=%d err=%v", pending, err)
	}
}

func TestPolicyOutboxRelayRetainsFailedPublish(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "policy-sync-failure.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&model.User{}, &model.Role{}, &policyRule{}, &policyOutbox{}); err != nil {
		t.Fatal(err)
	}
	service := newSyncService(t, db)
	if err = db.Create(&policyOutbox{Version: "v1"}).Error; err != nil {
		t.Fatal(err)
	}
	notifier := newMemoryNotifier()
	notifier.fail = true
	if err = service.relayPolicyChanges(context.Background(), notifier); err == nil {
		t.Fatal("failed publish was accepted")
	}
	var row policyOutbox
	if err = db.First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Attempts != 1 || row.DispatchedAt != nil {
		t.Fatalf("row=%+v", row)
	}
}

func TestConcurrentLastSuperAdminRevocationPreservesOne(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "last-super-admin.db")+"?_pragma=busy_timeout(5000)"), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&model.User{}, &model.Role{}, &policyRule{}, &policyOutbox{}); err != nil {
		t.Fatal(err)
	}
	service := newSyncService(t, db)
	for _, userID := range []uint64{1, 2} {
		if err = service.Bootstrap(context.Background(), userID); err != nil {
			t.Fatal(err)
		}
	}
	start := make(chan struct{})
	results := make(chan error, 2)
	for _, userID := range []uint64{1, 2} {
		go func(id uint64) {
			<-start
			results <- service.SetUserRoles(context.Background(), id, nil)
		}(userID)
	}
	close(start)
	for range 2 {
		<-results
	}
	reloaded := newSyncService(t, db)
	remaining := 0
	for _, userID := range []uint64{1, 2} {
		roles, getErr := reloaded.GetUserRoles(context.Background(), userID)
		if getErr != nil {
			t.Fatal(getErr)
		}
		for _, role := range roles {
			if role == model.SuperAdminRole {
				remaining++
			}
		}
	}
	if remaining == 0 {
		t.Fatal("concurrent revocation removed every super administrator")
	}
}

func TestDisableUserPreservesActiveSuperAdmin(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "disable-user.db")), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&model.User{}, &model.Role{}, &policyRule{}, &policyOutbox{}); err != nil {
		t.Fatal(err)
	}
	service := newSyncService(t, db)
	users := []model.User{
		{Username: "admin-one", PasswordHash: "hash", Status: model.UserActive},
		{Username: "admin-two", PasswordHash: "hash", Status: model.UserActive},
		{Username: "member-only", PasswordHash: "hash", Status: model.UserActive},
	}
	if err = db.Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	for _, user := range users[:2] {
		if err = service.Bootstrap(context.Background(), user.ID); err != nil {
			t.Fatal(err)
		}
	}
	if err = service.EnsureMemberForUser(context.Background(), users[2].ID); err != nil {
		t.Fatal(err)
	}
	if err = service.DisableUser(context.Background(), users[2].ID); err != nil {
		t.Fatal(err)
	}
	if err = service.DisableUser(context.Background(), users[0].ID); err != nil {
		t.Fatal(err)
	}
	if err = service.DisableUser(context.Background(), users[1].ID); err == nil {
		t.Fatal("last active super administrator was disabled")
	}
	if err = service.DisableUser(context.Background(), 999999); err == nil {
		t.Fatal("missing user was disabled")
	}
	if err = service.SetPermissions(context.Background(), 999999, nil); err == nil {
		t.Fatal("missing role permissions were updated")
	}
}

func TestPolicySyncStartAndStop(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "sync-lifecycle.db")), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(&model.User{}, &model.Role{}, &policyRule{}, &policyOutbox{}); err != nil {
		t.Fatal(err)
	}
	service := newSyncService(t, db)
	notifier := newMemoryNotifier()
	service.StartSync(context.Background(), notifier)
	select {
	case <-notifier.ready:
	case <-time.After(time.Second):
		t.Fatal("policy sync did not subscribe")
	}
	service.StopSync()
}
