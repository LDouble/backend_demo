package bootstrap

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func setRequired(t *testing.T) {
	t.Helper()
	t.Setenv("CAMPUS_MYSQL_DSN", "user:pass@tcp(localhost:3306)/db")
	t.Setenv("CAMPUS_JWT_SECRET", "0123456789abcdef0123456789abcdef")
	t.Setenv("CAMPUS_CONFIG_MASTER_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
}

func TestLoadAllEnvironmentOverrides(t *testing.T) {
	setRequired(t)
	t.Setenv("CAMPUS_REDIS_ADDRESS", "redis.internal:6380")
	t.Setenv("CAMPUS_REDIS_PASSWORD", "redis-secret")
	t.Setenv("CAMPUS_JWT_ISSUER", "test-issuer")
	t.Setenv("CAMPUS_ADMIN_USERNAME", "bootstrap-admin")
	t.Setenv("CAMPUS_ADMIN_PASSWORD", "long-admin-password")
	t.Setenv("CAMPUS_WORKER_REDIS_DB", "4")
	t.Setenv("CAMPUS_WORKER_CONCURRENCY", "17")
	t.Setenv("CAMPUS_WORKER_POLL_INTERVAL", "250ms")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Redis.Address != "redis.internal:6380" || cfg.Redis.Password != "redis-secret" || cfg.JWT.Issuer != "test-issuer" || cfg.Admin.Username != "bootstrap-admin" || cfg.Worker.RedisDB != 4 || cfg.Worker.Concurrency != 17 || cfg.Worker.PollInterval != 250*time.Millisecond {
		t.Fatalf("unexpected overrides: %+v", cfg)
	}
}

func TestLoadRequiredValueErrors(t *testing.T) {
	validKey := base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef"))
	tests := []struct{ name, dsn, jwt, key string }{
		{name: "mysql", jwt: "0123456789abcdef0123456789abcdef", key: validKey},
		{name: "jwt", dsn: "dsn", jwt: "short", key: validKey},
		{name: "config key", dsn: "dsn", jwt: "0123456789abcdef0123456789abcdef", key: "invalid"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("CAMPUS_MYSQL_DSN", test.dsn)
			t.Setenv("CAMPUS_JWT_SECRET", test.jwt)
			t.Setenv("CAMPUS_CONFIG_MASTER_KEY", test.key)
			if _, err := Load(""); err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
}
func TestLoadDefaultsAndOverrides(t *testing.T) {
	setRequired(t)
	t.Setenv("CAMPUS_SERVER_ADDRESS", ":9090")
	t.Setenv("CAMPUS_REDIS_DB", "2")
	t.Setenv("CAMPUS_SERVER_MAX_BODY_BYTES", "2048")
	t.Setenv("CAMPUS_SERVER_MAX_HEADER_BYTES", "4096")
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Server.Address != ":9090" || cfg.Server.MaxBodyBytes != 2048 || cfg.Server.MaxHeaderBytes != 4096 || cfg.Redis.DB != 2 || cfg.JWT.Issuer != "campus-platform" {
		t.Fatalf("cfg=%+v", cfg)
	}
}

func TestLoadProductionRedisTLSValidation(t *testing.T) {
	setRequired(t)
	t.Setenv("CAMPUS_ENV", "production")
	if _, err := Load(""); err == nil {
		t.Fatal("production accepted plaintext Redis")
	}
	t.Setenv("CAMPUS_REDIS_TLS", "true")
	t.Setenv("CAMPUS_REDIS_CLIENT_CERT_FILE", "client.pem")
	if _, err := Load(""); err == nil {
		t.Fatal("accepted client certificate without key")
	}
	t.Setenv("CAMPUS_REDIS_CLIENT_KEY_FILE", "client.key")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Redis.TLS || cfg.Environment != "production" {
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
