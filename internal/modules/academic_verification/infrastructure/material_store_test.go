package infrastructure

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestEncryptedMaterialStoreRoundTripAndBoundaries(t *testing.T) {
	root := t.TempDir()
	store, err := NewEncryptedMaterialStore(root, []byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte("private student card bytes")
	key, err := store.Save(context.Background(), plaintext, "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if len(key) != 64 || filepath.Base(key) != key {
		t.Fatalf("storage key = %q", key)
	}
	info, err := os.Stat(filepath.Join(root, key))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("material mode = %o", info.Mode().Perm())
	}
	ciphertext, err := os.ReadFile(filepath.Join(root, key))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(ciphertext, plaintext) {
		t.Fatal("ciphertext contains recognizable plaintext")
	}
	got, err := store.Open(context.Background(), key)
	if err != nil || !bytes.Equal(got, plaintext) {
		t.Fatalf("Open() = %q, %v", got, err)
	}
	if _, err = store.Open(context.Background(), "../escape"); err == nil {
		t.Fatal("path traversal storage key was accepted")
	}
	ciphertext[len(ciphertext)-1] ^= 0xff
	if err = os.WriteFile(filepath.Join(root, key), ciphertext, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Open(context.Background(), key); err == nil {
		t.Fatal("tampered ciphertext was accepted")
	}
	if err = store.Delete(context.Background(), key); err != nil {
		t.Fatal(err)
	}
	if err = store.Delete(context.Background(), key); err != nil {
		t.Fatal("repeated delete must be idempotent")
	}
}

func TestEncryptedMaterialStoreRequiresAbsoluteRootAndAES256Key(t *testing.T) {
	if _, err := NewEncryptedMaterialStore("relative", make([]byte, 32)); err == nil {
		t.Fatal("relative root was accepted")
	}
	if _, err := NewEncryptedMaterialStore(t.TempDir(), make([]byte, 16)); err == nil {
		t.Fatal("short key was accepted")
	}
}
