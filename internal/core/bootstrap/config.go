// Package bootstrap loads the minimal configuration needed to start the service.
package bootstrap

import (
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

// Config contains startup and security configuration.
type Config struct {
	Server ServerConfig `yaml:"server"`
	MySQL  MySQLConfig  `yaml:"mysql"`
	Redis  RedisConfig  `yaml:"redis"`
	Worker WorkerConfig `yaml:"worker"`
	JWT    JWTConfig    `yaml:"-"`
	Secret SecretConfig `yaml:"-"`
	Admin  AdminConfig  `yaml:"-"`
}

// ServerConfig contains HTTP listener settings.
type ServerConfig struct {
	Address string `yaml:"address"`
}

// MySQLConfig contains the startup database DSN.
type MySQLConfig struct {
	DSN string `yaml:"dsn"`
}

// RedisConfig contains the Redis connection settings.
type RedisConfig struct {
	Address  string `yaml:"address"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

// WorkerConfig contains asynchronous delivery runtime settings.
type WorkerConfig struct {
	RedisDB      int           `yaml:"redis_db"`
	Concurrency  int           `yaml:"concurrency"`
	PollInterval time.Duration `yaml:"poll_interval"`
}

// JWTConfig contains signing and lifetime settings.
type JWTConfig struct {
	Issuer     string
	Secret     []byte
	AccessTTL  time.Duration
	RefreshTTL time.Duration
}

// SecretConfig contains decoded encryption keys.
type SecretConfig struct{ ConfigKey []byte }

// AdminConfig contains first-administrator bootstrap values.
type AdminConfig struct {
	Username string
	Password string
}

// Load reads a YAML file when present and applies CAMPUS_ environment overrides.
func Load(path string) (Config, error) {
	cfg := Config{
		Server: ServerConfig{Address: ":8080"}, Redis: RedisConfig{Address: "127.0.0.1:6379"},
		Worker: WorkerConfig{RedisDB: 1, Concurrency: 10, PollInterval: time.Second},
		JWT:    JWTConfig{Issuer: "campus-platform", AccessTTL: 15 * time.Minute, RefreshTTL: 7 * 24 * time.Hour},
	}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			return Config{}, fmt.Errorf("read bootstrap config: %w", err)
		}
		if err == nil && yaml.Unmarshal(data, &cfg) != nil {
			return Config{}, fmt.Errorf("decode bootstrap config")
		}
	}
	override(&cfg)
	if cfg.MySQL.DSN == "" {
		return Config{}, fmt.Errorf("CAMPUS_MYSQL_DSN is required")
	}
	if len(cfg.JWT.Secret) < 32 {
		return Config{}, fmt.Errorf("CAMPUS_JWT_SECRET must contain at least 32 bytes")
	}
	if len(cfg.Secret.ConfigKey) != 32 {
		return Config{}, fmt.Errorf("CAMPUS_CONFIG_MASTER_KEY must be base64 for exactly 32 bytes")
	}
	return cfg, nil
}

func override(c *Config) {
	if v := os.Getenv("CAMPUS_SERVER_ADDRESS"); v != "" {
		c.Server.Address = v
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
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			c.Worker.Concurrency = n
		}
	}
	if v := os.Getenv("CAMPUS_WORKER_POLL_INTERVAL"); v != "" {
		if d, err := time.ParseDuration(v); err == nil && d > 0 {
			c.Worker.PollInterval = d
		}
	}
	if v := os.Getenv("CAMPUS_JWT_ISSUER"); v != "" {
		c.JWT.Issuer = v
	}
	c.JWT.Secret = []byte(os.Getenv("CAMPUS_JWT_SECRET"))
	if raw := os.Getenv("CAMPUS_CONFIG_MASTER_KEY"); raw != "" {
		c.Secret.ConfigKey, _ = base64.StdEncoding.DecodeString(raw)
	}
	c.Admin.Username = os.Getenv("CAMPUS_ADMIN_USERNAME")
	c.Admin.Password = os.Getenv("CAMPUS_ADMIN_PASSWORD")
}
