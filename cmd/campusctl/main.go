// Command campusctl manages migrations, administrators and generated modules.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/weouc-plus/campus-platform/internal/core/bootstrap"
	"github.com/weouc-plus/campus-platform/internal/core/permission"
	"github.com/weouc-plus/campus-platform/internal/core/user"
	"github.com/weouc-plus/campus-platform/internal/generator"
	"github.com/weouc-plus/campus-platform/internal/infrastructure/migration"
	"github.com/weouc-plus/campus-platform/internal/infrastructure/mysql"
	"gorm.io/gorm"
)

func main() {
	if err := run(context.Background(), os.Args[1:], os.Stdout); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string, output io.Writer) error {
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "migration":
		if len(args) < 2 {
			return usage()
		}
		cfg, err := bootstrap.Load(configPath())
		if err != nil {
			return err
		}
		steps := 0
		if len(args) > 2 {
			steps, err = strconv.Atoi(args[2])
			if err != nil {
				return fmt.Errorf("invalid steps: %w", err)
			}
		}
		return migration.Run(ctx, cfg.MySQL.DSN, args[1], steps)
	case "admin":
		if len(args) != 2 || args[1] != "bootstrap" {
			return usage()
		}
		cfg, err := bootstrap.Load(configPath())
		if err != nil {
			return err
		}
		return bootstrapAdmin(ctx, cfg, output)
	case "module":
		return runModule(ctx, args[1:], output)
	case "generate":
		return runGenerate(ctx, args[1:], output)
	default:
		return usage()
	}
}
func usage() error {
	return errors.New("usage: campusctl migration up|down [steps] | campusctl admin bootstrap | campusctl module validate <schema> | campusctl module list [--root path] | campusctl generate module <schema> [--check] [--root path]")
}
func configPath() string {
	if v := os.Getenv("CAMPUS_BOOTSTRAP_FILE"); v != "" {
		return v
	}
	return "bootstrap.yaml"
}
func bootstrapAdmin(parent context.Context, cfg bootstrap.Config, output io.Writer) error {
	if cfg.Admin.Username == "" || cfg.Admin.Password == "" {
		return errors.New("CAMPUS_ADMIN_USERNAME and CAMPUS_ADMIN_PASSWORD are required")
	}
	ctx, cancel := context.WithTimeout(parent, 15*time.Second)
	defer cancel()
	db, err := mysql.Open(ctx, cfg.MySQL.DSN)
	if err != nil {
		return err
	}
	sqlDB, _ := db.DB()
	if sqlDB != nil {
		defer func() { _ = sqlDB.Close() }()
	}
	repo := mysql.NewUserRepository(db)
	roles := mysql.NewRoleRepository(db)
	permissions, err := permission.NewService(db, roles)
	if err != nil {
		return err
	}
	users := user.NewService(repo, permissions)
	admin, err := repo.GetByUsername(ctx, cfg.Admin.Username)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		admin, err = users.Create(ctx, cfg.Admin.Username, cfg.Admin.Password)
	}
	if err != nil {
		return fmt.Errorf("bootstrap user: %w", err)
	}
	if err = permissions.Bootstrap(ctx, admin.ID); err != nil {
		return fmt.Errorf("bootstrap permissions: %w", err)
	}
	for page := 1; ; page++ {
		rows, _, listErr := repo.List(ctx, page, 100)
		if listErr != nil {
			return fmt.Errorf("list users for member backfill: %w", listErr)
		}
		for i := range rows {
			if roleErr := permissions.EnsureMemberForUser(ctx, rows[i].ID); roleErr != nil {
				return fmt.Errorf("backfill member role: %w", roleErr)
			}
		}
		if len(rows) < 100 {
			break
		}
	}
	_, err = fmt.Fprintf(output, "administrator %s is ready\n", admin.Username)
	if err != nil {
		return fmt.Errorf("write administrator result: %w", err)
	}
	return nil
}

func runModule(ctx context.Context, args []string, output io.Writer) error {
	if len(args) == 0 {
		return usage()
	}
	switch args[0] {
	case "validate":
		if len(args) != 2 {
			return usage()
		}
		schema, err := generator.Load(ctx, args[1])
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(output, "module=%s entity=%s table=%s fields=%d permissions=%d\n", schema.Module, schema.Entity.Name, schema.Entity.Table, len(schema.Entity.Fields), len(schema.Permissions))
		if err != nil {
			return fmt.Errorf("write validation result: %w", err)
		}
		return nil
	case "list":
		flags := flag.NewFlagSet("module list", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		root := flags.String("root", repositoryRoot(), "repository root")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
			return usage()
		}
		modules, err := generator.ListModules(ctx, *root)
		if err != nil {
			return err
		}
		for _, module := range modules {
			if _, err = fmt.Fprintf(output, "%s\t%s\t%s\n", module.Name, module.Entity, module.Schema); err != nil {
				return fmt.Errorf("write module list: %w", err)
			}
		}
		return nil
	default:
		return usage()
	}
}

func runGenerate(ctx context.Context, args []string, output io.Writer) error {
	if len(args) < 2 || args[0] != "module" {
		return usage()
	}
	schemaPath := args[1]
	flags := flag.NewFlagSet("generate module", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	check := flags.Bool("check", false, "check generated file drift")
	root := flags.String("root", repositoryRoot(), "repository root")
	if err := flags.Parse(args[2:]); err != nil || flags.NArg() != 0 {
		return usage()
	}
	schema, err := generator.Load(ctx, schemaPath)
	if err != nil {
		return err
	}
	source := schemaPath
	if absoluteRoot, rootErr := filepath.Abs(*root); rootErr == nil {
		if absoluteSchema, schemaErr := filepath.Abs(schemaPath); schemaErr == nil {
			if relative, relErr := filepath.Rel(absoluteRoot, absoluteSchema); relErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
				source = relative
			}
		}
	}
	result, err := generator.Generate(ctx, schema, generator.Options{Root: *root, Source: source, Check: *check})
	if err != nil {
		return err
	}
	mode := "generated"
	if *check {
		mode = "checked"
	}
	_, err = fmt.Fprintf(output, "%s module=%s files=%d changed=%d\n", mode, result.Module, result.Files, len(result.Changed))
	if err != nil {
		return fmt.Errorf("write generation result: %w", err)
	}
	return nil
}

func repositoryRoot() string {
	if root := os.Getenv("CAMPUS_REPOSITORY_ROOT"); root != "" {
		return root
	}
	return "."
}
