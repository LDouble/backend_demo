// Command worker runs the notice outbox relay and asynchronous delivery worker.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/hibiken/asynq"
	"github.com/weouc-plus/campus-platform/internal/core/bootstrap"
	"github.com/weouc-plus/campus-platform/internal/core/configcenter"
	domaineventinfra "github.com/weouc-plus/campus-platform/internal/infrastructure/domainevent"
	"github.com/weouc-plus/campus-platform/internal/infrastructure/logger"
	"github.com/weouc-plus/campus-platform/internal/infrastructure/mysql"
	"github.com/weouc-plus/campus-platform/internal/infrastructure/redisclient"
	carpoolapp "github.com/weouc-plus/campus-platform/internal/modules/carpool/application"
	carpoolinfra "github.com/weouc-plus/campus-platform/internal/modules/carpool/infrastructure"
	marketplaceapp "github.com/weouc-plus/campus-platform/internal/modules/marketplace/application"
	marketplaceinfra "github.com/weouc-plus/campus-platform/internal/modules/marketplace/infrastructure"
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
	redisOpt, err := redisclient.AsynqOptions(cfg.Redis, cfg.Worker.RedisDB)
	if err != nil {
		return err
	}
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
	marketplace := marketplaceapp.NewManager(marketplaceinfra.NewStore(db, cipher))
	mux := asynq.NewServeMux()
	processor.Register(mux)
	relay := noticeworker.NewRelay(store, client, cfg.Worker.PollInterval, log)
	domainPublisher := domaineventinfra.NewCompositePublisher(
		domaineventinfra.NewNoticePublisher(db),
		domaineventinfra.NewLogPublisher(log),
	)
	domainRelay := domaineventinfra.NewRelay(db, domainPublisher, cfg.Worker.PollInterval, log)
	errCh := make(chan error, 5)
	var workers sync.WaitGroup
	runWorker := func(work func() error) {
		workers.Add(1)
		go func() {
			defer workers.Done()
			errCh <- work()
		}()
	}
	runWorker(func() error { return relay.Run(ctx) })
	runWorker(func() error { return domainRelay.Run(ctx) })
	runWorker(func() error { return runCarpoolCompletion(ctx, carpools, cfg.Worker.PollInterval, log) })
	runWorker(func() error { return runMarketplaceExpiry(ctx, marketplace, time.Minute, log) })
	runWorker(func() error { return server.Run(mux) })
	log.Info("notice worker started", zap.Int("concurrency", cfg.Worker.Concurrency), zap.Int("redis_db", cfg.Worker.RedisDB))
	var runErr error
	select {
	case <-ctx.Done():
	case runErr = <-errCh:
		cancel()
	}
	server.Shutdown()
	workers.Wait()
	if runErr == nil || errors.Is(runErr, context.Canceled) {
		return nil
	}
	return runErr
}

func runCarpoolCompletion(ctx context.Context, carpools *carpoolapp.Manager, interval time.Duration, log *zap.Logger) error {
	return runPeriodic(ctx, interval, "carpool completion", log, carpools.CompleteDue)
}

func runMarketplaceExpiry(ctx context.Context, marketplace *marketplaceapp.Manager, interval time.Duration, log *zap.Logger) error {
	return runPeriodic(ctx, interval, "marketplace reservation expiry", log, marketplace.ExpireReservations)
}

func runPeriodic(ctx context.Context, interval time.Duration, name string, log *zap.Logger, work func(context.Context) (int64, error)) error {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if _, err := work(ctx); err != nil && !errors.Is(err, context.Canceled) {
			log.Error("periodic worker iteration failed", zap.String("worker", name), zap.Error(err))
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
