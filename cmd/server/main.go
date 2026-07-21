// Command server runs the campus platform API.
package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/weouc-plus/campus-platform/internal/app"
	"github.com/weouc-plus/campus-platform/internal/core/bootstrap"
	"go.uber.org/zap"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
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
	runtime, err := app.Build(ctx, cfg)
	if err != nil {
		return err
	}
	defer func() { _ = runtime.Close() }()
	server := &http.Server{
		Addr: cfg.Server.Address, Handler: runtime.Router,
		ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 15 * time.Second, IdleTimeout: 60 * time.Second,
		MaxHeaderBytes: cfg.Server.MaxHeaderBytes,
	}
	errCh := make(chan error, 1)
	go func() {
		runtime.Logger.Info("server started", zap.String("address", cfg.Server.Address))
		errCh <- server.ListenAndServe()
	}()
	select {
	case <-ctx.Done():
		shutdownCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		return server.Shutdown(shutdownCtx)
	case err = <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve http: %w", err)
	}
}
func configPath() string {
	if v := os.Getenv("CAMPUS_BOOTSTRAP_FILE"); v != "" {
		return v
	}
	return "bootstrap.yaml"
}
