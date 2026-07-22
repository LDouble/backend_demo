package infrastructure

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/weouc-plus/campus-platform/internal/modules/academic_verification/domain"
)

// EncryptedMaterialStore confines AES-256-GCM files to one operator-controlled root.
type EncryptedMaterialStore struct {
	rootPath string
	aead     cipher.AEAD
}

// NewEncryptedMaterialStore validates the key and prepares the private directory.
func NewEncryptedMaterialStore(rootPath string, key []byte) (*EncryptedMaterialStore, error) {
	if len(key) != 32 {
		return nil, fmt.Errorf("academic material key must contain exactly 32 bytes")
	}
	rootPath = filepath.Clean(rootPath)
	if !filepath.IsAbs(rootPath) {
		return nil, fmt.Errorf("academic material root must be absolute")
	}
	if err := os.MkdirAll(rootPath, 0o750); err != nil {
		return nil, fmt.Errorf("create academic material root: %w", err)
	}
	if err := os.Chmod(rootPath, 0o750); err != nil {
		return nil, fmt.Errorf("restrict academic material root: %w", err)
	}
	block, err := aes.NewCipher(append([]byte(nil), key...))
	if err != nil {
		return nil, fmt.Errorf("create academic material cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("create academic material GCM: %w", err)
	}
	return &EncryptedMaterialStore{rootPath: rootPath, aead: aead}, nil
}

// Save encrypts material with a fresh random nonce and unpredictable storage key.
func (s *EncryptedMaterialStore) Save(ctx context.Context, plaintext []byte, _ string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	keyBytes := make([]byte, 32)
	if _, err := io.ReadFull(rand.Reader, keyBytes); err != nil {
		return "", fmt.Errorf("generate academic storage key: %w", err)
	}
	storageKey := hex.EncodeToString(keyBytes)
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("generate academic material nonce: %w", err)
	}
	ciphertext := s.aead.Seal(nonce, nonce, plaintext, []byte(storageKey))
	root, err := os.OpenRoot(s.rootPath)
	if err != nil {
		return "", fmt.Errorf("open academic material root: %w", err)
	}
	defer root.Close()
	file, err := root.OpenFile(storageKey, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return "", fmt.Errorf("create academic material: %w", err)
	}
	clean := false
	defer func() {
		_ = file.Close()
		if !clean {
			_ = root.Remove(storageKey)
		}
	}()
	if _, err = file.Write(ciphertext); err != nil {
		return "", fmt.Errorf("write academic material: %w", err)
	}
	if err = file.Sync(); err != nil {
		return "", fmt.Errorf("sync academic material: %w", err)
	}
	if err = file.Close(); err != nil {
		return "", fmt.Errorf("close academic material: %w", err)
	}
	clean = true
	return storageKey, nil
}

// Open authenticates and decrypts one confined material file.
func (s *EncryptedMaterialStore) Open(ctx context.Context, storageKey string) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !validStorageKey(storageKey) {
		return nil, fmt.Errorf("invalid academic storage key")
	}
	root, err := os.OpenRoot(s.rootPath)
	if err != nil {
		return nil, fmt.Errorf("open academic material root: %w", err)
	}
	defer root.Close()
	file, err := root.Open(storageKey)
	if err != nil {
		return nil, fmt.Errorf("open academic material file: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, fmt.Errorf("stat academic material: %w", err)
	}
	maxCiphertext := domain.MaxMaterialBytes + int64(s.aead.NonceSize()+s.aead.Overhead())
	if info.Size() < int64(s.aead.NonceSize()+s.aead.Overhead()) || info.Size() > maxCiphertext {
		return nil, fmt.Errorf("invalid academic material ciphertext size")
	}
	ciphertext, err := io.ReadAll(io.LimitReader(file, maxCiphertext+1))
	if err != nil {
		return nil, fmt.Errorf("read academic material: %w", err)
	}
	nonceSize := s.aead.NonceSize()
	plaintext, err := s.aead.Open(nil, ciphertext[:nonceSize], ciphertext[nonceSize:], []byte(storageKey))
	if err != nil {
		return nil, fmt.Errorf("authenticate academic material: %w", err)
	}
	return plaintext, nil
}

// Delete removes one confined ciphertext file and tolerates an already-cleaned file.
func (s *EncryptedMaterialStore) Delete(ctx context.Context, storageKey string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validStorageKey(storageKey) {
		return fmt.Errorf("invalid academic storage key")
	}
	root, err := os.OpenRoot(s.rootPath)
	if err != nil {
		return fmt.Errorf("open academic material root: %w", err)
	}
	defer root.Close()
	if err = root.Remove(storageKey); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("remove academic material: %w", err)
	}
	return nil
}

func validStorageKey(value string) bool {
	if len(value) != 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == 32 && hex.EncodeToString(decoded) == value
}
