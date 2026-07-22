package infrastructure

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/weouc-plus/campus-platform/internal/modules/academic_verification/application"
	"golang.org/x/crypto/bcrypt"
)

func TestMockProviderUsesBcryptAndUniformCredentialErrors(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("correct-password"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "provider.json")
	data, err := json.Marshal([]mockCredential{{
		StudentNo: "20260001", RealName: "测试学生", PasswordHash: string(hash),
	}})
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	provider, err := NewMockProvider(path)
	if err != nil {
		t.Fatal(err)
	}
	name, err := provider.Verify(context.Background(), "20260001", "correct-password")
	if err != nil || name != "测试学生" {
		t.Fatalf("Verify() name=%q err=%v", name, err)
	}
	for _, attempt := range []struct{ studentNo, password string }{
		{studentNo: "20260001", password: "wrong"},
		{studentNo: "unknown", password: "correct-password"},
	} {
		if _, err = provider.Verify(context.Background(), attempt.studentNo, attempt.password); !errors.Is(err, application.ErrInvalidCredentials) {
			t.Fatalf("Verify(%q) error=%v", attempt.studentNo, err)
		}
	}
}

func TestMockProviderRejectsMalformedWhitelist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "provider.json")
	if err := os.WriteFile(path, []byte(`[{"student_no":"1","real_name":"x","password_hash":"plain"}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewMockProvider(path); err == nil {
		t.Fatal("plain password whitelist was accepted")
	}
}

func TestDevelopmentMockProviderFixture(t *testing.T) {
	provider, err := NewMockProvider(filepath.Join("..", "..", "..", "..", "deploy", "dev", "academic-provider.json"))
	if err != nil {
		t.Fatal(err)
	}
	name, err := provider.Verify(context.Background(), "20260001", "password")
	if err != nil || name != "测试学生01" {
		t.Fatalf("Verify() name=%q err=%v", name, err)
	}
}
