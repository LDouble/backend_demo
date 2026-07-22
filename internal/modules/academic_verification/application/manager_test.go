package application

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/apperror"
	"github.com/weouc-plus/campus-platform/internal/modules/academic_verification/domain"
)

type managerStore struct {
	Store
	created      *domain.AcademicVerificationMaterial
	verifiedUser uint64
	verifiedName string
	verifiedNo   string
}

func (s *managerStore) FindAvailableMaterial(context.Context, uint64, string, time.Time) (*domain.AcademicVerificationMaterial, error) {
	return nil, nil
}

func (s *managerStore) CreateMaterial(_ context.Context, material *domain.AcademicVerificationMaterial) error {
	material.ID = 1
	s.created = material
	return nil
}

func (s *managerStore) VerifyCredentials(
	_ context.Context,
	userID uint64,
	realName string,
	studentNo string,
	_ time.Time,
) (*domain.AcademicVerificationRequest, error) {
	s.verifiedUser, s.verifiedName, s.verifiedNo = userID, realName, studentNo
	return &domain.AcademicVerificationRequest{ID: 9, UserId: userID, Status: domain.RequestApproved}, nil
}

type memoryMaterials struct {
	saved []byte
}

func (m *memoryMaterials) Save(_ context.Context, data []byte, _ string) (string, error) {
	m.saved = append([]byte(nil), data...)
	return strings.Repeat("a", 64), nil
}

func (*memoryMaterials) Open(context.Context, string) ([]byte, error) { return nil, nil }
func (*memoryMaterials) Delete(context.Context, string) error         { return nil }

type providerStub struct {
	password string
	err      error
}

func (p *providerStub) Verify(_ context.Context, _ string, password string) (string, error) {
	p.password = password
	return "张三", p.err
}

type limiterStub struct {
	allowed  bool
	failures int
	clears   int
}

func (l *limiterStub) Allow(context.Context, uint64, string, string) (bool, error) {
	return l.allowed, nil
}

func (l *limiterStub) RecordFailure(context.Context, uint64, string, string) error {
	l.failures++
	return nil
}

func (l *limiterStub) Clear(context.Context, uint64, string, string) error {
	l.clears++
	return nil
}

func TestUploadValidatesSignatureAndSize(t *testing.T) {
	store := &managerStore{}
	materials := &memoryMaterials{}
	manager := NewManager(store, materials, nil, nil)
	png := append([]byte("\x89PNG\r\n\x1a\n"), make([]byte, 512)...)
	material, err := manager.Upload(context.Background(), 7, bytes.NewReader(png))
	if err != nil || material.ID != 1 || material.MimeType != "image/png" {
		t.Fatalf("material=%+v err=%v", material, err)
	}
	if !bytes.Equal(materials.saved, png) {
		t.Fatal("stored plaintext did not match validated bytes")
	}
	if _, err = manager.Upload(context.Background(), 7, strings.NewReader("<svg></svg>")); err == nil {
		t.Fatal("SVG material was accepted")
	}
	tooLarge := bytes.NewReader(make([]byte, domain.MaxMaterialBytes+1))
	if _, err = manager.Upload(context.Background(), 7, tooLarge); err == nil {
		t.Fatal("oversized material was accepted")
	}
}

func TestCredentialVerificationLimitsAndForgetsPassword(t *testing.T) {
	store := &managerStore{}
	provider := &providerStub{}
	limiter := &limiterStub{allowed: true}
	manager := NewManager(store, &memoryMaterials{}, provider, limiter)
	request, err := manager.VerifyCredentials(context.Background(), 7, "20260001", "secret-password", "127.0.0.1")
	if err != nil || request.ID != 9 || provider.password != "secret-password" || limiter.clears != 1 {
		t.Fatalf("request=%+v password=%q clears=%d err=%v", request, provider.password, limiter.clears, err)
	}
	if store.verifiedUser != 7 || store.verifiedName != "张三" || store.verifiedNo != "20260001" {
		t.Fatalf("persisted verification=%d/%q/%q", store.verifiedUser, store.verifiedName, store.verifiedNo)
	}

	provider.err = ErrInvalidCredentials
	if _, err = manager.VerifyCredentials(context.Background(), 7, "20260001", "bad", "127.0.0.1"); err == nil || limiter.failures != 1 {
		t.Fatalf("invalid credential error=%v failures=%d", err, limiter.failures)
	}
	var appErr *apperror.Error
	if !errors.As(err, &appErr) || appErr.Code != "invalid_academic_credentials" {
		t.Fatalf("public error=%v", err)
	}
	limiter.allowed = false
	if _, err = manager.VerifyCredentials(context.Background(), 7, "20260001", "bad", "127.0.0.1"); err == nil {
		t.Fatal("limited credential attempt was accepted")
	}
}
