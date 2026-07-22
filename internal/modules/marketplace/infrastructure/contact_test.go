package infrastructure

import (
	"context"
	"crypto/sha256"
	"testing"

	"github.com/weouc-plus/campus-platform/internal/core/configcenter"
	"github.com/weouc-plus/campus-platform/internal/modules/marketplace/domain"
)

func TestListingContactOwnerAndTerminalVisibility(t *testing.T) {
	key := sha256.Sum256([]byte("marketplace contact test key"))
	cipher, err := configcenter.NewCipher(key[:])
	if err != nil {
		t.Fatal(err)
	}
	store := NewStore(nil, cipher)
	ciphertext, err := cipher.Encrypt("13800138000", listingContactAAD(7))
	if err != nil {
		t.Fatal(err)
	}
	listing := &domain.Listing{ID: 7, OwnerId: 11, Status: domain.ListingPublished, ContactType: "phone", ContactCiphertext: ciphertext}

	owner, err := store.Contact(context.Background(), listing, 11)
	if err != nil || owner.Value != "13800138000" {
		t.Fatalf("owner contact = %#v, %v", owner, err)
	}
	listing.Status = domain.ListingSold
	terminal, err := store.Contact(context.Background(), listing, 11)
	if err != nil || terminal.Value != "1*********0" {
		t.Fatalf("terminal contact = %#v, %v", terminal, err)
	}
	listing.ID = 8
	if _, err := store.Contact(context.Background(), listing, 11); err == nil {
		t.Fatal("Contact() error = nil, want AAD decryption failure")
	}
}
