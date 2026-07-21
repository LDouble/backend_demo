// Command worker runs the notice outbox relay and asynchronous delivery worker.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/hibiken/asynq"
	"github.com/weouc-plus/campus-platform/internal/core/bootstrap"
	"github.com/weouc-plus/campus-platform/internal/core/configcenter"
	"github.com/weouc-plus/campus-platform/internal/infrastructure/logger"
	"github.com/weouc-plus/campus-platform/internal/infrastructure/mysql"
	carpoolapp "github.com/weouc-plus/campus-platform/internal/modules/carpool/application"
	carpoolinfra "github.com/weouc-plus/campus-platform/internal/modules/carpool/infrastructure"
	noticeinfra "github.com/weouc-plus/campus-platform/internal/modules/notice/infrastructure"
	noticeworker "github.com/weouc-plus/campus-platform/internal/modules/notice/worker"
	"go.uber.org/zap"
)

func main() {
	if err := run(); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := bootstrap.Load(configPath())
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	log, err := logger.New()
	if err != nil {
		return err
	}
	defer func() { _ = log.Sync() }()
	db, err := mysql.Open(ctx, cfg.MySQL.DSN)
	if err != nil {
		return err
	}
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	defer func() { _ = sqlDB.Close() }()
	redisOpt := asynq.RedisClientOpt{Addr: cfg.Redis.Address, Password: cfg.Redis.Password, DB: cfg.Worker.RedisDB}
	client := asynq.NewClient(redisOpt)
	defer func() { _ = client.Close() }()
	server := asynq.NewServer(redisOpt, asynq.Config{Concurrency: cfg.Worker.Concurrency, Queues: map[string]int{"notifications": 1}, ShutdownTimeout: 10 * time.Second})
	store := noticeinfra.NewNoticeStore(db)
	processor := noticeworker.NewProcessor(store, noticeworker.NewLogProvider(log))
	cipher, err := configcenter.NewCipher(cfg.Secret.ConfigKey)
	if err != nil {
		return err
	}
	carpools := carpoolapp.NewManager(carpoolinfra.NewStore(db, cipher))
	mux := asynq.NewServeMux()
	processor.Register(mux)
	relay := noticeworker.NewRelay(store, client, cfg.Worker.PollInterval, log)
	errCh := make(chan error, 3)
	go func() { errCh <- relay.Run(ctx) }()
	go func() { errCh <- runCarpoolCompletion(ctx, carpools, cfg.Worker.PollInterval) }()
	go func() { errCh <- server.Run(mux) }()
	log.Info("notice worker started", zap.Int("concurrency", cfg.Worker.Concurrency), zap.Int("redis_db", cfg.Worker.RedisDB))
	select {
	case <-ctx.Done():
		server.Shutdown()
		return nil
	case err = <-errCh:
		cancel()
		server.Shutdown()
		if errors.Is(err, context.Canceled) {
			return nil
		}
		return err
	}
}

func runCarpoolCompletion(ctx context.Context, carpools *carpoolapp.Manager, interval time.Duration) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := carpools.CompleteDue(ctx); err != nil {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func configPath() string {
	if value := os.Getenv("CAMPUS_BOOTSTRAP_FILE"); value != "" {
		return value
	}
	return "bootstrap.yaml"
}
