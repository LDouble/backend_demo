package infrastructure

import (
	"context"
	"crypto/sha256"
	"testing"

	"github.com/weouc-plus/campus-platform/internal/core/configcenter"
	"github.com/weouc-plus/campus-platform/internal/modules/errand/domain"
)

func TestContactVisibilityAndAAD(t *testing.T) {
	key := sha256.Sum256([]byte("errand contact test key"))
	cipher, err := configcenter.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(nil, cipher)
	ciphertext, err := cipher.Encrypt("runner-wechat", taskContactAAD(7))
	if err != nil {
		t.Fatal(err)
	}
	runnerID := uint64(22)
	task := &domain.Task{ID: 7, RequesterId: 11, RunnerId: &runnerID, Status: domain.TaskAccepted, ContactType: "wechat", ContactCiphertext: ciphertext}

	owner, err := store.Contact(context.Background(), task, 11)
	if err != nil || owner.Value != "runner-wechat" {
		t.Fatalf("owner contact = %#v, %v", owner, err)
	}
	runner, err := store.Contact(context.Background(), task, runnerID)
	if err != nil || runner.Value != "runner-wechat" {
		t.Fatalf("runner contact = %#v, %v", runner, err)
	}
	masked, err := store.Contact(context.Background(), task, 33)
	if err != nil || masked.Value != "r***********t" {
		t.Fatalf("masked contact = %#v, %v", masked, err)
	}
	task.Status = domain.TaskCompleted
	terminal, err := store.Contact(context.Background(), task, 11)
	if err != nil || terminal.Value != "r***********t" {
		t.Fatalf("terminal contact = %#v, %v", terminal, err)
	}
	task.ID = 8
	if _, err := store.Contact(context.Background(), task, 11); err == nil {
		t.Fatal("Contact() error = nil, want AAD decryption failure")
	}
}
