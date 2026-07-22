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
	t.Setenv("CAMPUS_TRUSTED_PROXY_CIDRS", "10.0.0.0/8")
	t.Setenv("CAMPUS_REQUIRE_PROXY_HTTPS", "true")
	t.Setenv("CAMPUS_REDIS_TLS_FILES_ROOT", t.TempDir())
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

func TestLoadNormalizesAndValidatesEnvironment(t *testing.T) {
	setRequired(t)
	t.Setenv("CAMPUS_ENV", " Production ")
	t.Setenv("CAMPUS_REDIS_TLS", "true")
	t.Setenv("CAMPUS_TRUSTED_PROXY_CIDRS", "10.0.0.0/8")
	t.Setenv("CAMPUS_REQUIRE_PROXY_HTTPS", "true")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.IsProduction() || cfg.Environment != EnvironmentProduction {
		t.Fatalf("environment=%q", cfg.Environment)
	}
	t.Setenv("CAMPUS_ENV", "prod")
	if _, err = Load(""); err == nil {
		t.Fatal("invalid environment alias was accepted")
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

func TestLoadRejectsInvalidWorkerSettings(t *testing.T) {
	setRequired(t)
	tests := []struct {
		name string
		yaml string
	}{
		{name: "zero concurrency", yaml: "worker:\n  concurrency: 0\n  poll_interval: 1s\n"},
		{name: "excessive concurrency", yaml: "worker:\n  concurrency: 1025\n  poll_interval: 1s\n"},
		{name: "zero interval", yaml: "worker:\n  concurrency: 1\n  poll_interval: 0s\n"},
		{name: "too short interval", yaml: "worker:\n  concurrency: 1\n  poll_interval: 1ms\n"},
		{name: "excessive interval", yaml: "worker:\n  concurrency: 1\n  poll_interval: 2h\n"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "bootstrap.yaml")
			if err := os.WriteFile(path, []byte(test.yaml), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("invalid worker settings were accepted")
			}
		})
	}
}

func TestLoadRejectsMalformedWorkerOverrides(t *testing.T) {
	setRequired(t)
	t.Setenv("CAMPUS_WORKER_CONCURRENCY", "many")
	if _, err := Load(""); err == nil {
		t.Fatal("malformed concurrency was accepted")
	}
	t.Setenv("CAMPUS_WORKER_CONCURRENCY", "10")
	t.Setenv("CAMPUS_WORKER_POLL_INTERVAL", "often")
	if _, err := Load(""); err == nil {
		t.Fatal("malformed poll interval was accepted")
	}
}

func TestLoadRejectsRedisTLSPathEscape(t *testing.T) {
	setRequired(t)
	t.Setenv("CAMPUS_REDIS_TLS", "true")
	t.Setenv("CAMPUS_REDIS_TLS_FILES_ROOT", t.TempDir())
	t.Setenv("CAMPUS_REDIS_CA_FILE", "../ca.pem")
	if _, err := Load(""); err == nil {
		t.Fatal("Redis CA path escape was accepted")
	}
}

func TestPurposeSpecificLoadersRequireOnlyTheirSecrets(t *testing.T) {
	t.Setenv("CAMPUS_ENV", "production")
	t.Setenv("CAMPUS_MYSQL_DSN", "dsn")
	t.Setenv("CAMPUS_JWT_SECRET", "")
	t.Setenv("CAMPUS_CONFIG_MASTER_KEY", "")
	t.Setenv("CAMPUS_ADMIN_USERNAME", "")
	t.Setenv("CAMPUS_ADMIN_PASSWORD", "")
	if _, err := LoadMigration(""); err != nil {
		t.Fatalf("LoadMigration() error = %v", err)
	}
	if _, err := LoadAdminBootstrap(""); err == nil {
		t.Fatal("LoadAdminBootstrap() accepted missing administrator credentials")
	}
	t.Setenv("CAMPUS_ADMIN_USERNAME", "admin")
	t.Setenv("CAMPUS_ADMIN_PASSWORD", "secret")
	if _, err := LoadAdminBootstrap(""); err != nil {
		t.Fatalf("LoadAdminBootstrap() error = %v", err)
	}
	t.Setenv("CAMPUS_CONFIG_MASTER_KEY", base64.StdEncoding.EncodeToString([]byte("0123456789abcdef0123456789abcdef")))
	t.Setenv("CAMPUS_REDIS_TLS", "true")
	if _, err := LoadWorker(""); err != nil {
		t.Fatalf("LoadWorker() error = %v", err)
	}
}
