// Package bootstrap loads the minimal configuration needed to start the service.
package bootstrap

import (
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config contains startup and security configuration.
type Config struct {
	Environment string         `yaml:"environment"`
	Server      ServerConfig   `yaml:"server"`
	MySQL       MySQLConfig    `yaml:"mysql"`
	Redis       RedisConfig    `yaml:"redis"`
	Worker      WorkerConfig   `yaml:"worker"`
	Academic    AcademicConfig `yaml:"academic"`
	WeChat      WeChatConfig   `yaml:"wechat"`
	JWT         JWTConfig      `yaml:"-"`
	Secret      SecretConfig   `yaml:"-"`
	Admin       AdminConfig    `yaml:"-"`
}

// WeChatConfig contains non-secret WeChat Mini Program endpoint knobs.
//
// Secrets are not stored here: each WeChat Mini Program's appid/secret lives
// in the configuration center (group=wechat, key=<appid>, encrypted=true).
// Only the upstream endpoint and HTTP timeout live in bootstrap, alongside
// other infrastructure settings.
type WeChatConfig struct {
	Endpoint    string        `yaml:"endpoint"`
	HTTPTimeout time.Duration `yaml:"http_timeout"`
}

// ServerConfig contains HTTP listener settings.
type ServerConfig struct {
	Address           string   `yaml:"address"`
	MaxBodyBytes      int64    `yaml:"max_body_bytes"`
	MaxHeaderBytes    int      `yaml:"max_header_bytes"`
	TrustedProxyCIDRs []string `yaml:"trusted_proxy_cidrs"`
	RequireProxyHTTPS bool     `yaml:"require_proxy_https"`
}

// MySQLConfig contains the startup database DSN.
type MySQLConfig struct {
	DSN string `yaml:"dsn"`
}

// RedisConfig contains the Redis connection settings.
type RedisConfig struct {
	Address            string `yaml:"address"`
	Password           string `yaml:"password"`
	DB                 int    `yaml:"db"`
	TLS                bool   `yaml:"tls"`
	TLSFilesRoot       string `yaml:"tls_files_root"`
	CAFile             string `yaml:"ca_file"`
	ClientCertFile     string `yaml:"client_cert_file"`
	ClientKeyFile      string `yaml:"client_key_file"`
	ServerName         string `yaml:"server_name"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
}

// WorkerConfig contains asynchronous delivery runtime settings.
type WorkerConfig struct {
	RedisDB      int           `yaml:"redis_db"`
	Concurrency  int           `yaml:"concurrency"`
	PollInterval time.Duration `yaml:"poll_interval"`
}

// AcademicConfig contains non-secret provider and private-storage paths.
type AcademicConfig struct {
	MaterialRoot string `yaml:"material_root"`
	ProviderFile string `yaml:"provider_file"`
}

// JWTConfig contains signing and lifetime settings.
type JWTConfig struct {
	Issuer     string
	Secret     []byte
	AccessTTL  time.Duration
	RefreshTTL time.Duration
}

// SecretConfig contains decoded encryption keys.
type SecretConfig struct {
	ConfigKey           []byte
	AcademicMaterialKey []byte
}

// AdminConfig contains first-administrator bootstrap values.
type AdminConfig struct {
	Username string
	Password string
}

const (
	// EnvironmentDevelopment enables local-development defaults.
	EnvironmentDevelopment = "development"
	// EnvironmentTest is reserved for isolated automated tests.
	EnvironmentTest = "test"
	// EnvironmentProduction enables fail-closed production safeguards.
	EnvironmentProduction = "production"
)

// IsProduction reports whether production safeguards must be enabled.
func (c Config) IsProduction() bool { return c.Environment == EnvironmentProduction }

type loadRequirements struct {
	jwt              bool
	configKey        bool
	server           bool
	redis            bool
	worker           bool
	admin            bool
	academicMaterial bool
	academicProvider bool
	wechat           bool
}

// Load reads API configuration and applies CAMPUS_ environment overrides.
func Load(path string) (Config, error) {
	return load(path, loadRequirements{
		jwt: true, configKey: true, server: true, redis: true, worker: true,
		academicMaterial: true, academicProvider: true, wechat: true,
	})
}

// LoadWorker reads configuration required by the asynchronous worker.
func LoadWorker(path string) (Config, error) {
	return load(path, loadRequirements{configKey: true, redis: true, worker: true, academicMaterial: true})
}

// LoadMigration reads only the configuration required to run migrations.
func LoadMigration(path string) (Config, error) {
	return load(path, loadRequirements{})
}

// LoadAdminBootstrap reads only the configuration required to create the first administrator.
func LoadAdminBootstrap(path string) (Config, error) {
	return load(path, loadRequirements{admin: true})
}

func load(path string, requirements loadRequirements) (Config, error) {
	cfg := Config{
		Environment: EnvironmentDevelopment,
		Server:      ServerConfig{Address: ":8080", MaxBodyBytes: 6 << 20, MaxHeaderBytes: 64 << 10},
		Redis:       RedisConfig{Address: "127.0.0.1:6379"},
		Worker:      WorkerConfig{RedisDB: 1, Concurrency: 10, PollInterval: time.Second},
		WeChat:      WeChatConfig{Endpoint: "https://api.weixin.qq.com", HTTPTimeout: 3 * time.Second},
		JWT:         JWTConfig{Issuer: "campus-platform", AccessTTL: 15 * time.Minute, RefreshTTL: 7 * 24 * time.Hour},
	}
	if path != "" {
		// #nosec G304 -- the bootstrap path is an explicit operator-controlled startup boundary.
		data, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("read bootstrap config: %w", err)
		}
		if err == nil && yaml.Unmarshal(data, &cfg) != nil {
			return Config{}, fmt.Errorf("decode bootstrap config")
		}
	}
	if err := override(&cfg); err != nil {
		return Config{}, err
	}
	cfg.Environment = strings.ToLower(strings.TrimSpace(cfg.Environment))
	switch cfg.Environment {
	case EnvironmentDevelopment, EnvironmentTest, EnvironmentProduction:
	default:
		return Config{}, fmt.Errorf("CAMPUS_ENV must be development, test, or production")
	}
	if cfg.MySQL.DSN == "" {
		return Config{}, fmt.Errorf("CAMPUS_MYSQL_DSN is required")
	}
	if requirements.jwt && len(cfg.JWT.Secret) < 32 {
		return Config{}, fmt.Errorf("CAMPUS_JWT_SECRET must contain at least 32 bytes")
	}
	if requirements.configKey && len(cfg.Secret.ConfigKey) != 32 {
		return Config{}, fmt.Errorf("CAMPUS_CONFIG_MASTER_KEY must be base64 for exactly 32 bytes")
	}
	if requirements.academicMaterial {
		if len(cfg.Secret.AcademicMaterialKey) != 32 {
			return Config{}, fmt.Errorf("CAMPUS_ACADEMIC_MATERIAL_KEY must be base64 for exactly 32 bytes")
		}
		if strings.TrimSpace(cfg.Academic.MaterialRoot) == "" || !filepath.IsAbs(cfg.Academic.MaterialRoot) {
			return Config{}, fmt.Errorf("CAMPUS_ACADEMIC_MATERIAL_ROOT must be an absolute path")
		}
	}
	if requirements.academicProvider && (strings.TrimSpace(cfg.Academic.ProviderFile) == "" || !filepath.IsAbs(cfg.Academic.ProviderFile)) {
		return Config{}, fmt.Errorf("CAMPUS_ACADEMIC_PROVIDER_FILE must be an absolute path")
	}
	if requirements.server {
		for _, cidr := range cfg.Server.TrustedProxyCIDRs {
			if _, _, err := net.ParseCIDR(cidr); err != nil {
				return Config{}, fmt.Errorf("invalid trusted proxy CIDR %q", cidr)
			}
		}
		if cfg.IsProduction() && (len(cfg.Server.TrustedProxyCIDRs) == 0 || !cfg.Server.RequireProxyHTTPS) {
			return Config{}, fmt.Errorf("production requires trusted_proxy_cidrs and require_proxy_https")
		}
	}
	if requirements.redis {
		if cfg.IsProduction() && !cfg.Redis.TLS {
			return Config{}, fmt.Errorf("redis TLS is required in production")
		}
		if cfg.Redis.InsecureSkipVerify && cfg.IsProduction() {
			return Config{}, fmt.Errorf("redis TLS verification cannot be disabled in production")
		}
		if (cfg.Redis.ClientCertFile == "") != (cfg.Redis.ClientKeyFile == "") {
			return Config{}, fmt.Errorf("redis client_cert_file and client_key_file must be configured together")
		}
		if cfg.Redis.CAFile != "" || cfg.Redis.ClientCertFile != "" {
			if strings.TrimSpace(cfg.Redis.TLSFilesRoot) == "" {
				return Config{}, fmt.Errorf("redis tls_files_root is required for custom TLS files")
			}
			for _, name := range []string{cfg.Redis.CAFile, cfg.Redis.ClientCertFile, cfg.Redis.ClientKeyFile} {
				if name != "" && (filepath.IsAbs(name) || !filepath.IsLocal(name)) {
					return Config{}, fmt.Errorf("redis TLS file names must be relative to tls_files_root")
				}
			}
		}
	}
	if requirements.worker {
		if cfg.Worker.Concurrency < 1 || cfg.Worker.Concurrency > 1024 {
			return Config{}, fmt.Errorf("worker concurrency must be between 1 and 1024")
		}
		if cfg.Worker.PollInterval < 100*time.Millisecond || cfg.Worker.PollInterval > time.Hour {
			return Config{}, fmt.Errorf("worker poll_interval must be between 100ms and 1h")
		}
	}
	if requirements.admin && (cfg.Admin.Username == "" || cfg.Admin.Password == "") {
		return Config{}, fmt.Errorf("CAMPUS_ADMIN_USERNAME and CAMPUS_ADMIN_PASSWORD are required")
	}
	if requirements.wechat {
		if cfg.WeChat.HTTPTimeout <= 0 {
			cfg.WeChat.HTTPTimeout = 3 * time.Second
		}
		if cfg.IsProduction() && cfg.WeChat.HTTPTimeout > 5*time.Second {
			return Config{}, fmt.Errorf("wechat http_timeout must not exceed 5s in production")
		}
		if strings.TrimSpace(cfg.WeChat.Endpoint) == "" {
			return Config{}, fmt.Errorf("wechat.endpoint must be a non-empty URL")
		}
		u, err := url.Parse(cfg.WeChat.Endpoint)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return Config{}, fmt.Errorf("wechat.endpoint must be a valid absolute URL")
		}
		// Code2Session sends appid+secret+code in the URL query, so plaintext
		// HTTP would expose the application secret on the wire. Production
		// must use HTTPS; loopback and test mocks can still use http://.
		if cfg.IsProduction() && !strings.EqualFold(u.Scheme, "https") {
			return Config{}, fmt.Errorf("wechat.endpoint must use https in production (got scheme %q)", u.Scheme)
		}
	}
	return cfg, nil
}

func override(c *Config) error {
	if v := os.Getenv("CAMPUS_ENV"); v != "" {
		c.Environment = v
	}
	if v := os.Getenv("CAMPUS_SERVER_ADDRESS"); v != "" {
		c.Server.Address = v
	}
	if v := os.Getenv("CAMPUS_SERVER_MAX_BODY_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			c.Server.MaxBodyBytes = n
		}
	}
	if v := os.Getenv("CAMPUS_SERVER_MAX_HEADER_BYTES"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.Server.MaxHeaderBytes = n
		}
	}
	if v := os.Getenv("CAMPUS_TRUSTED_PROXY_CIDRS"); v != "" {
		c.Server.TrustedProxyCIDRs = splitNonEmpty(v)
	}
	if v := os.Getenv("CAMPUS_REQUIRE_PROXY_HTTPS"); v != "" {
		parsed, err := strconv.ParseBool(v)
		if err != nil {
			return fmt.Errorf("CAMPUS_REQUIRE_PROXY_HTTPS must be a boolean")
		}
		c.Server.RequireProxyHTTPS = parsed
	}
	if v := os.Getenv("CAMPUS_MYSQL_DSN"); v != "" {
		c.MySQL.DSN = v
	}
	if v := os.Getenv("CAMPUS_REDIS_ADDRESS"); v != "" {
		c.Redis.Address = v
	}
	if v := os.Getenv("CAMPUS_REDIS_PASSWORD"); v != "" {
		c.Redis.Password = v
	}
	if v := os.Getenv("CAMPUS_REDIS_TLS"); v != "" {
		c.Redis.TLS, _ = strconv.ParseBool(v)
	}
	if v := os.Getenv("CAMPUS_REDIS_CA_FILE"); v != "" {
		c.Redis.CAFile = v
	}
	if v := os.Getenv("CAMPUS_REDIS_TLS_FILES_ROOT"); v != "" {
		c.Redis.TLSFilesRoot = v
	}
	if v := os.Getenv("CAMPUS_REDIS_CLIENT_CERT_FILE"); v != "" {
		c.Redis.ClientCertFile = v
	}
	if v := os.Getenv("CAMPUS_REDIS_CLIENT_KEY_FILE"); v != "" {
		c.Redis.ClientKeyFile = v
	}
	if v := os.Getenv("CAMPUS_REDIS_SERVER_NAME"); v != "" {
		c.Redis.ServerName = v
	}
	if v := os.Getenv("CAMPUS_REDIS_DB"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Redis.DB = n
		}
	}
	if v := os.Getenv("CAMPUS_WORKER_REDIS_DB"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			c.Worker.RedisDB = n
		}
	}
	if v := os.Getenv("CAMPUS_WORKER_CONCURRENCY"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("CAMPUS_WORKER_CONCURRENCY must be an integer")
		}
		c.Worker.Concurrency = n
	}
	if v := os.Getenv("CAMPUS_WORKER_POLL_INTERVAL"); v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("CAMPUS_WORKER_POLL_INTERVAL must be a duration")
		}
		c.Worker.PollInterval = d
	}
	if v := os.Getenv("CAMPUS_ACADEMIC_MATERIAL_ROOT"); v != "" {
		c.Academic.MaterialRoot = v
	}
	if v := os.Getenv("CAMPUS_ACADEMIC_PROVIDER_FILE"); v != "" {
		c.Academic.ProviderFile = v
	}
	if v := os.Getenv("CAMPUS_WECHAT_ENDPOINT"); v != "" {
		c.WeChat.Endpoint = v
	}
	if v := os.Getenv("CAMPUS_WECHAT_HTTP_TIMEOUT"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			c.WeChat.HTTPTimeout = d
		}
	}
	if v := os.Getenv("CAMPUS_JWT_ISSUER"); v != "" {
		c.JWT.Issuer = v
	}
	c.JWT.Secret = []byte(os.Getenv("CAMPUS_JWT_SECRET"))
	if raw := os.Getenv("CAMPUS_CONFIG_MASTER_KEY"); raw != "" {
		c.Secret.ConfigKey, _ = base64.StdEncoding.DecodeString(raw)
	}
	if raw := os.Getenv("CAMPUS_ACADEMIC_MATERIAL_KEY"); raw != "" {
		c.Secret.AcademicMaterialKey, _ = base64.StdEncoding.DecodeString(raw)
	}
	c.Admin.Username = os.Getenv("CAMPUS_ADMIN_USERNAME")
	c.Admin.Password = os.Getenv("CAMPUS_ADMIN_PASSWORD")
	return nil
}

func splitNonEmpty(value string) []string {
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		if part = strings.TrimSpace(part); part != "" {
			result = append(result, part)
		}
	}
	return result
}
