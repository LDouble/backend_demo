package infrastructure

import (
	"bytes"
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"github.com/weouc-plus/campus-platform/internal/core/domainevent"
	"github.com/weouc-plus/campus-platform/internal/modules/academic_verification/domain"
	"gorm.io/gorm"
)

type baseRoleRecorder struct {
	guest  []uint64
	member []uint64
}

func (r *baseRoleRecorder) EnsureGuestForUser(_ context.Context, userID uint64) error {
	r.guest = append(r.guest, userID)
	return nil
}

func (r *baseRoleRecorder) EnsureMemberForUser(_ context.Context, userID uint64) error {
	r.member = append(r.member, userID)
	return nil
}

func TestStudentCardApprovalSupersedesPendingAndSwitchesBaseRole(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(
		&domain.AcademicIdentity{},
		&domain.AcademicVerificationMaterial{},
		&domain.AcademicVerificationRequest{},
		&domainevent.Event{},
	); err != nil {
		t.Fatal(err)
	}
	roles := &baseRoleRecorder{}
	store := NewStore(db, roles)
	now := time.Now().UTC()
	materials := []domain.AcademicVerificationMaterial{
		{UserId: 7, StorageKey: "a", MimeType: "image/png", SizeBytes: 10, Sha256: "one", Status: domain.MaterialAvailable, ExpiresAt: now.Add(time.Hour), Version: 1},
		{UserId: 7, StorageKey: "b", MimeType: "image/png", SizeBytes: 10, Sha256: "two", Status: domain.MaterialAvailable, ExpiresAt: now.Add(time.Hour), Version: 1},
	}
	if err = db.Create(&materials).Error; err != nil {
		t.Fatal(err)
	}
	first, err := store.SubmitStudentCard(context.Background(), 7, "张三", "20260001", materials[0].ID, now)
	if err != nil {
		t.Fatal(err)
	}
	second, err := store.SubmitStudentCard(context.Background(), 7, "张三", "20260002", materials[1].ID, now)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := store.Approve(context.Background(), first.ID, 99, first.Version, now)
	if err != nil {
		t.Fatal(err)
	}
	if approved.Status != domain.RequestApproved || len(roles.member) != 1 || roles.member[0] != 7 {
		t.Fatalf("approved=%+v member=%v", approved, roles.member)
	}
	other, err := store.GetRequest(context.Background(), second.ID)
	if err != nil || other.Status != domain.RequestSuperseded {
		t.Fatalf("superseded request=%+v err=%v", other, err)
	}
	status, err := store.Status(context.Background(), 7)
	if err != nil || status.Identity == nil || status.Identity.StudentNo != "20260001" {
		t.Fatalf("status=%+v err=%v", status, err)
	}
	identity, err := store.Revoke(context.Background(), status.Identity.ID, 99, status.Identity.Version, "离校", now.Add(time.Minute))
	if err != nil || identity.Status != domain.IdentityRevoked || len(roles.guest) != 1 {
		t.Fatalf("identity=%+v guest=%v err=%v", identity, roles.guest, err)
	}
	var events []domainevent.Event
	if err = db.Order("id").Find(&events).Error; err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 {
		t.Fatalf("events=%d", len(events))
	}
	for _, event := range events {
		if bytes.Contains(event.Payload, []byte("20260001")) || bytes.Contains(event.Payload, []byte("张三")) {
			t.Fatalf("sensitive event payload=%s", event.Payload)
		}
		var payload domain.VerificationEvent
		if err = json.Unmarshal(event.Payload, &payload); err != nil || payload.UserID != 7 {
			t.Fatalf("payload=%s err=%v", event.Payload, err)
		}
	}
}

func TestSameStudentNumberCannotBindTwoUsers(t *testing.T) {
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{TranslateError: true})
	if err != nil {
		t.Fatal(err)
	}
	if err = db.AutoMigrate(
		&domain.AcademicIdentity{},
		&domain.AcademicVerificationRequest{},
		&domainevent.Event{},
	); err != nil {
		t.Fatal(err)
	}
	store := NewStore(db, &baseRoleRecorder{})
	now := time.Now().UTC()
	if _, err = store.VerifyCredentials(context.Background(), 1, "甲", "same-number", now); err != nil {
		t.Fatal(err)
	}
	if _, err = store.VerifyCredentials(context.Background(), 2, "乙", "same-number", now); err == nil {
		t.Fatal("duplicate student number was accepted")
	}
}
