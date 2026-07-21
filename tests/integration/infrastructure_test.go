//go:build integration

package integration

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/weouc-plus/campus-platform/internal/core/bootstrap"
	"github.com/weouc-plus/campus-platform/internal/core/idempotency"
	"github.com/weouc-plus/campus-platform/internal/core/model"
	"github.com/weouc-plus/campus-platform/internal/core/permission"
	"github.com/weouc-plus/campus-platform/internal/infrastructure/migration"
	platformmysql "github.com/weouc-plus/campus-platform/internal/infrastructure/mysql"
	"github.com/weouc-plus/campus-platform/internal/infrastructure/redisclient"
	noticeapp "github.com/weouc-plus/campus-platform/internal/modules/notice/application"
	noticedomain "github.com/weouc-plus/campus-platform/internal/modules/notice/domain"
	noticeinfra "github.com/weouc-plus/campus-platform/internal/modules/notice/infrastructure"
	"gorm.io/gorm"
)

func TestMySQLConcurrentIdempotencyExecutesOnce(t *testing.T) {
	dsn := requiredEnv(t, "CAMPUS_INTEGRATION_MYSQL_DSN")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := platformmysql.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	key := "mysql-concurrent-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	hash, err := idempotency.RequestHash(map[string]string{"value": "same"})
	if err != nil {
		t.Fatal(err)
	}
	request := idempotency.Request{ActorID: 9_000_001, OperationID: "IntegrationConcurrent", Key: key, RequestHash: hash}
	t.Cleanup(func() {
		_ = db.Where("actor_id = ? AND operation_id = ?", request.ActorID, request.OperationID).Delete(&idempotency.Record{}).Error
	})
	start := make(chan struct{})
	results := make(chan idempotency.Result, 2)
	errors := make(chan error, 2)
	var calls atomic.Int32
	var group sync.WaitGroup
	for range 2 {
		group.Add(1)
		go func() {
			defer group.Done()
			<-start
			result, executeErr := idempotency.Execute(ctx, db, request, func(*gorm.DB) (idempotency.Result, error) {
				calls.Add(1)
				time.Sleep(100 * time.Millisecond)
				return idempotency.Result{HTTPStatus: 201, Body: []byte(`{"data":{"id":1}}`)}, nil
			})
			results <- result
			errors <- executeErr
		}()
	}
	close(start)
	group.Wait()
	close(results)
	close(errors)
	for executeErr := range errors {
		if executeErr != nil {
			t.Fatal(executeErr)
		}
	}
	replays := 0
	for result := range results {
		if result.Replayed {
			replays++
		}
	}
	if calls.Load() != 1 || replays != 1 {
		t.Fatalf("business calls=%d replayed=%d", calls.Load(), replays)
	}
}

func TestRedisPermissionPropagationAcrossInstances(t *testing.T) {
	dsn := requiredEnv(t, "CAMPUS_INTEGRATION_MYSQL_DSN")
	address := requiredEnv(t, "CAMPUS_INTEGRATION_REDIS_ADDRESS")
	password := requiredEnv(t, "CAMPUS_REDIS_PASSWORD")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := platformmysql.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	firstRedis, err := redisclient.Open(ctx, bootstrap.RedisConfig{Address: address, Password: password}, 14)
	if err != nil {
		t.Fatal(err)
	}
	secondRedis, err := redisclient.Open(ctx, bootstrap.RedisConfig{Address: address, Password: password}, 14)
	if err != nil {
		_ = firstRedis.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = firstRedis.Close(); _ = secondRedis.Close() })
	writer, err := permission.NewService(db, platformmysql.NewRoleRepository(db))
	if err != nil {
		t.Fatal(err)
	}
	reader, err := permission.NewService(db, platformmysql.NewRoleRepository(db))
	if err != nil {
		t.Fatal(err)
	}
	writer.StartSync(ctx, redisclient.NewPolicyNotifier(firstRedis))
	reader.StartSync(ctx, redisclient.NewPolicyNotifier(secondRedis))
	t.Cleanup(func() { writer.StopSync(); reader.StopSync() })
	suffix := strconv.FormatInt(time.Now().UnixNano(), 10)
	userRow := model.User{Username: "policy_" + suffix, PasswordHash: "integration", Status: model.UserActive, SessionVersion: 1}
	if err = db.Create(&userRow).Error; err != nil {
		t.Fatal(err)
	}
	role, err := writer.CreateRole(ctx, "policy_"+suffix, "integration", false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = writer.SetUserRoles(context.Background(), userRow.ID, nil)
		_ = writer.DeleteRole(context.Background(), role.ID)
		_ = db.Delete(&userRow).Error
	})
	if err = writer.SetUserRoles(ctx, userRow.ID, []string{role.Name}); err != nil {
		t.Fatal(err)
	}
	if err = writer.SetPermissions(ctx, role.ID, []permission.Permission{{PathPattern: "/api/v1/configs/:id", Methods: []string{"GET"}}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		allowed, enforceErr := reader.Enforce(ctx, userRow.ID, "/api/v1/configs/1", "GET")
		if enforceErr != nil {
			t.Fatal(enforceErr)
		}
		if allowed {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("permission change did not propagate within five seconds")
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func TestRedisSessionStoreLifecycle(t *testing.T) {
	address := requiredEnv(t, "CAMPUS_INTEGRATION_REDIS_ADDRESS")
	password := requiredEnv(t, "CAMPUS_REDIS_PASSWORD")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client, err := redisclient.Open(ctx, bootstrap.RedisConfig{Address: address, Password: password}, 15)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := client.Close(); err != nil {
			t.Errorf("close Redis: %v", err)
		}
	})
	store := redisclient.NewSessionStore(client)
	sid := "integration-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	if err = store.Create(ctx, sid, 7, "old-hash", time.Second); err != nil {
		t.Fatal(err)
	}
	if exists, e := store.Exists(ctx, sid); e != nil || !exists {
		t.Fatalf("created session exists=%v err=%v", exists, e)
	}
	if rotated, e := store.Rotate(ctx, sid, "old-hash", "new-hash", time.Second); e != nil || !rotated {
		t.Fatalf("rotate session rotated=%v err=%v", rotated, e)
	}
	if rotated, e := store.Rotate(ctx, sid, "old-hash", "reused", time.Second); e != nil || rotated {
		t.Fatalf("reused refresh rotated=%v err=%v", rotated, e)
	}
	if err = store.Delete(ctx, sid); err != nil {
		t.Fatal(err)
	}
	if exists, e := store.Exists(ctx, sid); e != nil || exists {
		t.Fatalf("deleted session exists=%v err=%v", exists, e)
	}

	expiringSID := sid + "-expiring"
	if err = store.Create(ctx, expiringSID, 7, "hash", 100*time.Millisecond); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for {
		exists, e := store.Exists(ctx, expiringSID)
		if e != nil {
			t.Fatal(e)
		}
		if !exists {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("session did not expire")
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func TestMigrationsUpDownUp(t *testing.T) {
	adminDSN := requiredEnv(t, "CAMPUS_INTEGRATION_MYSQL_ADMIN_DSN")
	databaseName := "campus_migration_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	admin, err := sql.Open("mysql", adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, e := admin.Exec("DROP DATABASE IF EXISTS `" + databaseName + "`"); e != nil {
			t.Errorf("drop migration database: %v", e)
		}
		if e := admin.Close(); e != nil {
			t.Errorf("close admin database: %v", e)
		}
	})
	if _, err = admin.Exec("CREATE DATABASE `" + databaseName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci"); err != nil {
		t.Fatal(err)
	}
	config, err := mysqldriver.ParseDSN(adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	config.DBName = databaseName
	dsn := config.FormatDSN()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err = migration.Run(ctx, dsn, "up", 0); err != nil {
		t.Fatalf("migration up: %v", err)
	}
	assertTableExists(t, admin, databaseName, "users", true)
	assertTableExists(t, admin, databaseName, "notices", true)
	assertTableExists(t, admin, databaseName, "activities", true)
	if err = migration.Run(ctx, dsn, "down", 1); err != nil {
		t.Fatalf("migration down: %v", err)
	}
	assertTableExists(t, admin, databaseName, "activities", true)
	assertTableExists(t, admin, databaseName, "idempotency_records", true)
	assertTableExists(t, admin, databaseName, "notices", true)
	assertTableExists(t, admin, databaseName, "users", true)
	if err = migration.Run(ctx, dsn, "up", 0); err != nil {
		t.Fatalf("second migration up: %v", err)
	}
	assertTableExists(t, admin, databaseName, "configs", true)
	assertTableExists(t, admin, databaseName, "notices", true)
	assertTableExists(t, admin, databaseName, "activities", true)
}

func TestGeneratedModuleMigrationUpDownUp(t *testing.T) {
	adminDSN := requiredEnv(t, "CAMPUS_INTEGRATION_MYSQL_ADMIN_DSN")
	databaseName := "campus_generated_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	admin, err := sql.Open("mysql", adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if _, e := admin.Exec("DROP DATABASE IF EXISTS `" + databaseName + "`"); e != nil {
			t.Errorf("drop generated migration database: %v", e)
		}
		if e := admin.Close(); e != nil {
			t.Errorf("close generated migration admin database: %v", e)
		}
	})
	if _, err = admin.Exec("CREATE DATABASE `" + databaseName + "` CHARACTER SET utf8mb4 COLLATE utf8mb4_0900_ai_ci"); err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile("../../schemas/examples/activity.yaml") // #nosec G304 -- fixed repository test fixture.
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err = os.MkdirAll(root+"/schemas", 0o750); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(root+"/schemas/activity.yaml", schema, 0o640); err != nil {
		t.Fatal(err)
	}
	plan, err := migration.Plan(context.Background(), root, "activity")
	if err != nil {
		t.Fatal(err)
	}
	config, err := mysqldriver.ParseDSN(adminDSN)
	if err != nil {
		t.Fatal(err)
	}
	config.DBName = databaseName
	db, err := sql.Open("mysql", config.FormatDSN())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if _, err = db.ExecContext(ctx, plan.UpSQL); err != nil {
		t.Fatalf("generated migration up: %v", err)
	}
	assertTableExists(t, admin, databaseName, "activities", true)
	if _, err = db.ExecContext(ctx, plan.DownSQL); err != nil {
		t.Fatalf("generated migration down: %v", err)
	}
	assertTableExists(t, admin, databaseName, "activities", false)
	if _, err = db.ExecContext(ctx, plan.UpSQL); err != nil {
		t.Fatalf("generated migration second up: %v", err)
	}
	assertTableExists(t, admin, databaseName, "activities", true)
}

func TestCasbinPolicyPersistence(t *testing.T) {
	dsn := requiredEnv(t, "CAMPUS_INTEGRATION_MYSQL_DSN")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	db, err := platformmysql.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if e := sqlDB.Close(); e != nil {
			t.Errorf("close application database: %v", e)
		}
	})
	roles := platformmysql.NewRoleRepository(db)
	first, err := permission.NewService(db, roles)
	if err != nil {
		t.Fatal(err)
	}
	roleName := "persist_" + strconv.FormatInt(time.Now().UnixNano(), 10)
	role, err := first.CreateRole(ctx, roleName, "持久化测试", false)
	if err != nil {
		t.Fatal(err)
	}
	const userID = uint64(9000001)
	if err = first.SetPermissions(ctx, role.ID, []permission.Permission{{PathPattern: "/api/v1/configs/:id", Methods: []string{"GET"}}}); err != nil {
		t.Fatal(err)
	}
	if err = first.SetUserRoles(ctx, userID, []string{roleName}); err != nil {
		t.Fatal(err)
	}
	reloaded, err := permission.NewService(db, platformmysql.NewRoleRepository(db))
	if err != nil {
		t.Fatal(err)
	}
	allowed, err := reloaded.Enforce(ctx, userID, "/api/v1/configs/42", "GET")
	if err != nil || !allowed {
		t.Fatalf("reloaded policy allowed=%v err=%v", allowed, err)
	}
	if err = reloaded.SetUserRoles(ctx, userID, nil); err != nil {
		t.Fatal(err)
	}
	if err = reloaded.DeleteRole(ctx, role.ID); err != nil {
		t.Fatal(err)
	}
}

func TestNoticeSnapshotAndReadPersistence(t *testing.T) {
	dsn := requiredEnv(t, "CAMPUS_INTEGRATION_MYSQL_DSN")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := platformmysql.Open(ctx, dsn)
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	user := model.User{Username: "notice_" + strconv.FormatInt(time.Now().UnixNano(), 10), PasswordHash: "integration", Status: model.UserActive}
	if err = db.WithContext(ctx).Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Delete(&user).Error })
	store := noticeinfra.NewNoticeStore(db)
	notice := &noticedomain.Notice{Title: "集成通知", Summary: "快照", Body: "不会写入投递日志的正文", Category: "campus", Priority: noticedomain.PriorityImportant, Status: noticedomain.StatusDraft, PushEnabled: true, Version: 1, CreatedBy: user.ID, UpdatedBy: user.ID}
	if err = store.Create(ctx, notice, []noticedomain.NoticeAudience{{AudienceType: noticedomain.AudienceAll, AudienceValue: "*"}}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Delete(notice).Error })
	if ok, queueErr := store.QueuePublish(ctx, notice.ID, user.ID, 1, time.Now().Add(-time.Second)); queueErr != nil || !ok {
		t.Fatalf("queue publish=%v err=%v", ok, queueErr)
	}
	if err = store.Publish(ctx, notice.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	rows, total, err := store.ListInbox(ctx, user.ID, noticeapp.InboxFilter{Page: 1, PageSize: 20})
	if err != nil || total != 1 || len(rows) != 1 {
		t.Fatalf("inbox=%+v total=%d err=%v", rows, total, err)
	}
	if err = store.MarkRead(ctx, user.ID, notice.ID, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if unread, countErr := store.UnreadCount(ctx, user.ID); countErr != nil || unread != 0 {
		t.Fatalf("unread=%d err=%v", unread, countErr)
	}
	var deliveries int64
	if err = db.Model(&noticedomain.NoticeDelivery{}).Where("notice_id = ? AND user_id = ?", notice.ID, user.ID).Count(&deliveries).Error; err != nil || deliveries != 1 {
		t.Fatalf("deliveries=%d err=%v", deliveries, err)
	}
}

func requiredEnv(t *testing.T, name string) string {
	t.Helper()
	value := os.Getenv(name)
	if value == "" {
		t.Skip(fmt.Sprintf("%s is not set", name))
	}
	return value
}

func assertTableExists(t *testing.T, db *sql.DB, schema, table string, want bool) {
	t.Helper()
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM information_schema.tables WHERE table_schema = ? AND table_name = ?", schema, table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if got := count == 1; got != want {
		t.Fatalf("table %s exists=%v want=%v", table, got, want)
	}
}
