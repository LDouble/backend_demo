package infrastructure

import (
	"context"
	"crypto/sha256"
	"testing"

	"github.com/weouc-plus/campus-platform/internal/core/configcenter"
	"github.com/weouc-plus/campus-platform/internal/modules/activity/domain"
)

func TestActivityContactVisibilityAndAAD(t *testing.T) {
	key := sha256.Sum256([]byte("activity contact test key"))
	cipher, err := configcenter.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	store := &Store{cipher: cipher}
	ciphertext, err := cipher.Encrypt("activity_owner_wechat", activityContactAAD(7))
	if err != nil {
		t.Fatal(err)
	}
	activity := &domain.Activity{
		ID:                7,
		CreatedBy:         11,
		Status:            domain.ActivityStatusPublished,
		ReviewStatus:      domain.ReviewStatusApproved,
		ContactType:       "wechat",
		ContactCiphertext: ciphertext,
	}

	owner, err := store.Contact(context.Background(), activity, 11)
	if err != nil || owner.Value != "activity_owner_wechat" {
		t.Fatalf("owner contact = %#v, %v", owner, err)
	}

	activity.Status = domain.ActivityStatusCancelled
	terminal, err := store.Contact(context.Background(), activity, 11)
	if err != nil || terminal.Value != "a*******************t" {
		t.Fatalf("terminal contact = %#v, %v", terminal, err)
	}

	activity.ID = 8
	if _, err := store.Contact(context.Background(), activity, 11); err == nil {
		t.Fatal("Contact() error = nil, want AAD decryption failure")
	}
}
