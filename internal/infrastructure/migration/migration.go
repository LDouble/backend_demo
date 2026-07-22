// Package migration runs embedded versioned database migrations.
package migration

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"

	"github.com/golang-migrate/migrate/v4"
	// Register the MySQL migration driver.
	_ "github.com/golang-migrate/migrate/v4/database/mysql"
	// Register the file migration source.
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

// Run applies migrations in the requested direction. Down defaults to one step.
func Run(ctx context.Context, dsn, direction string, steps int) error {
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("migration context: %w", err)
	}
	path, err := directory()
	if err != nil {
		return err
	}
	m, err := migrate.New("file://"+filepath.ToSlash(path), "mysql://"+dsn)
	if err != nil {
		return fmt.Errorf("create migrator: %w", err)
	}
	defer func() { _, _ = m.Close() }()
	switch direction {
	case "up":
		err = m.Up()
	case "down":
		if steps <= 0 {
			steps = 1
		}
		err = m.Steps(-steps)
	default:
		return fmt.Errorf("unsupported migration direction %q", direction)
	}
	if errors.Is(err, migrate.ErrNoChange) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("migrate %s: %w", direction, err)
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("migration context: %w", err)
	}
	return nil
}

func directory() (string, error) {
	if v := os.Getenv("CAMPUS_MIGRATIONS_PATH"); v != "" {
		return v, nil
	}
	candidates := []string{"migrations"}
	if _, file, _, ok := runtime.Caller(0); ok {
		candidates = append(candidates, filepath.Join(filepath.Dir(file), "..", "..", "..", "migrations"))
	}
	for _, candidate := range candidates {
		if _, err := os.Stat(candidate); err == nil {
			return filepath.Abs(candidate)
		} else if !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
	}
	return "", fmt.Errorf("migrations directory not found")
}
