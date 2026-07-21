package bootstrap

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("CAMPUS_MYSQL_DSN", "user:pass@tcp(localhost:3306)/db")
	t.Setenv("CAMPUS_JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("CAMPUS_CONFIG_MASTER_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
}
func TestLoadDefaultsAndOverrides(t *testing.T) {
	setRequired(t)
	t.Setenv("CAMPUS_SERVER_ADDRESS", ":9090")
	t.Setenv("CAMPUS_REDIS_DB", "2")
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Address != ":9090" || cfg.Redis.DB != 2 || cfg.JWT.Issuer != "campus-platform" {
		t.Fatalf("cfg=%+v", cfg)
	}
}
func TestLoadYAML(t *testing.T) {
	setRequired(t)
	path := filepath.Join(t.TempDir(), "bootstrap.yaml")
	if err := os.WriteFile(path, []byte("server:\n  address: ':7070'\nredis:\n  address: 'redis:6379'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CAMPUS_SERVER_ADDRESS", "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Address != ":7070" || cfg.Redis.Address != "redis:6379" {
		t.Fatalf("cfg=%+v", cfg)
	}
}
func TestLoadRejectsMissingSecrets(t *testing.T) {
	t.Setenv("CAMPUS_MYSQL_DSN", "dsn")
	t.Setenv("CAMPUS_JWT_SECRET", "short")
	t.Setenv("CAMPUS_CONFIG_MASTER_KEY", "")
	if _, err := Load(""); err == nil {
		t.Fatal("expected secret validation")
	}
}

func TestLoadRejectsInvalidYAML(t *testing.T) {
	setRequired(t)
	path := filepath.Join(t.TempDir(), "bootstrap.yaml")
	if err := os.WriteFile(path, []byte("server: ["), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected YAML decoding error")
	}
}
