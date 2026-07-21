// Package redisclient creates verified Redis connections shared by API and workers.
package redisclient

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/weouc-plus/campus-platform/internal/core/bootstrap"
)

// Options converts validated platform configuration into go-redis options.
func Options(config bootstrap.RedisConfig, db int) (*redis.Options, error) {
	options := &redis.Options{Addr: config.Address, Password: config.Password, DB: db}
	tlsConfig, err := TLSConfig(config)
	if err != nil {
		return nil, err
	}
	options.TLSConfig = tlsConfig
	return options, nil
}

// TLSConfig constructs the shared, verification-enabled Redis TLS settings.
func TLSConfig(config bootstrap.RedisConfig) (*tls.Config, error) {
	if !config.TLS {
		return nil, nil
	}
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		ServerName:         config.ServerName,
		InsecureSkipVerify: config.InsecureSkipVerify, // #nosec G402 -- production validation forbids this escape hatch.
	}
	if config.CAFile != "" {
		// Security boundary: this operator-controlled path is read only at startup.
		pem, err := os.ReadFile(config.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read Redis CA file: %w", err)
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			return nil, fmt.Errorf("load system CA pool: %w", err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("redis CA file contains no certificates")
		}
		tlsConfig.RootCAs = pool
	}
	if config.ClientCertFile != "" {
		certificate, err := tls.LoadX509KeyPair(config.ClientCertFile, config.ClientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load Redis client certificate: %w", err)
		}
		tlsConfig.Certificates = []tls.Certificate{certificate}
	}
	return tlsConfig, nil
}

// Open creates and verifies a Redis client.
func Open(ctx context.Context, config bootstrap.RedisConfig, db int) (*redis.Client, error) {
	options, err := Options(config, db)
	if err != nil {
		return nil, err
	}
	client := redis.NewClient(options)
	if err = client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("ping redis: %w", err)
	}
	return client, nil
}

// AsynqOptions returns worker options with the exact same TLS policy.
func AsynqOptions(config bootstrap.RedisConfig, db int) (asynq.RedisClientOpt, error) {
	tlsConfig, err := TLSConfig(config)
	if err != nil {
		return asynq.RedisClientOpt{}, err
	}
	return asynq.RedisClientOpt{
		Addr: config.Address, Password: config.Password, DB: db, TLSConfig: tlsConfig,
	}, nil
}
