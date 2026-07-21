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
		return runMigration(ctx, args[1:], output)
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
	return errors.New("usage: campusctl migration up|down [steps] | plan <module> | new <name> --module <module> | promote <draft> | snapshot <module> | check | list")
}

func runMigration(ctx context.Context, args []string, output io.Writer) error {
	if len(args) == 0 {
		return usage()
	}
	if args[0] == "up" || args[0] == "down" {
		cfg, err := bootstrap.Load(configPath())
		if err != nil {
			return err
		}
		steps := 0
		if len(args) > 2 {
			return usage()
		}
		if len(args) == 2 {
			steps, err = strconv.Atoi(args[1])
			if err != nil {
				return fmt.Errorf("invalid steps: %w", err)
			}
		}
		return migration.Run(ctx, cfg.MySQL.DSN, args[0], steps)
	}
	flags := flag.NewFlagSet("migration "+args[0], flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	root := flags.String("root", repositoryRoot(), "repository root")
	switch args[0] {
	case "plan":
		if len(args) < 2 {
			return usage()
		}
		module := args[1]
		if err := flags.Parse(args[2:]); err != nil || flags.NArg() != 0 {
			return usage()
		}
		plan, err := migration.Plan(ctx, *root, module)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(output, "module=%s destructive=%d\n%s", plan.Module, len(plan.Destructive), plan.UpSQL)
		return err
	case "new":
		if len(args) < 2 {
			return usage()
		}
		name := args[1]
		module := flags.String("module", "", "module name")
		if err := flags.Parse(args[2:]); err != nil || flags.NArg() != 0 || *module == "" {
			return usage()
		}
		draft, err := migration.NewDraft(ctx, *root, name, *module)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(output, "draft=%s up=%s down=%s\n", draft.Name, draft.Up, draft.Down)
		return err
	case "promote":
		if len(args) < 2 {
			return usage()
		}
		draft := args[1]
		if err := flags.Parse(args[2:]); err != nil || flags.NArg() != 0 {
			return usage()
		}
		promoted, err := migration.Promote(ctx, *root, draft, time.Now().UTC())
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(output, "version=%s name=%s\n", promoted.Version, promoted.Name)
		return err
	case "snapshot":
		if len(args) < 2 {
			return usage()
		}
		module := args[1]
		if err := flags.Parse(args[2:]); err != nil || flags.NArg() != 0 {
			return usage()
		}
		if err := migration.Snapshot(ctx, *root, module); err != nil {
			return err
		}
		_, err := fmt.Fprintf(output, "snapshotted module=%s\n", module)
		return err
	case "check":
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
			return usage()
		}
		return migration.Check(ctx, *root)
	case "list":
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
			return usage()
		}
		rows, err := migration.List(*root)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if _, err = fmt.Fprintf(output, "%s\t%s\n", row.Version, row.Name); err != nil {
				return err
			}
		}
		return nil
	default:
		return usage()
	}
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
	permissions, err := permission.NewService(ctx, db, roles)
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
	if len(args) > 0 && args[0] == "modules" {
		flags := flag.NewFlagSet("generate modules", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		check := flags.Bool("check", false, "check all generated modules")
		prune := flags.Bool("prune", false, "remove stale managed generated files")
		root := flags.String("root", repositoryRoot(), "repository root")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 || (*check && *prune) {
			return usage()
		}
		modules, err := generator.DiscoverModules(ctx, *root)
		if err != nil {
			return err
		}
		globalManaged, err := generator.GlobalManagedFiles(ctx, *root, modules)
		if err != nil {
			return err
		}
		if *check {
			if err = generator.SyncModuleRegistry(ctx, *root, modules, true); err != nil {
				return err
			}
		} else {
			// Render the complete plan in an isolated root before touching the worktree.
			temporaryRoot, tempErr := os.MkdirTemp("", "campus-generate-plan-*")
			if tempErr != nil {
				return fmt.Errorf("create generation plan root: %w", tempErr)
			}
			defer func() { _ = os.RemoveAll(temporaryRoot) }()
			if tempErr = generator.SyncModuleRegistry(ctx, temporaryRoot, modules, false); tempErr != nil {
				return tempErr
			}
			plannedManaged := append([]string{".agent/modules.json"}, globalManaged...)
			for _, module := range modules {
				schema, loadErr := generator.Load(ctx, filepath.Join(*root, filepath.FromSlash(module.Schema)))
				if loadErr != nil {
					return loadErr
				}
				var planned generator.Result
				if planned, tempErr = generator.Generate(ctx, schema, generator.Options{Root: temporaryRoot, Source: module.Schema}); tempErr != nil {
					return tempErr
				}
				plannedManaged = append(plannedManaged, planned.Managed...)
			}
			stale, staleErr := generator.FindStaleManagedFiles(*root, plannedManaged)
			if staleErr != nil {
				return staleErr
			}
			if len(stale) > 0 && !*prune {
				return fmt.Errorf("%w: %s", generator.ErrStaleArtifacts, strings.Join(stale, ", "))
			}
			if err = generator.SyncModuleRegistry(ctx, *root, modules, false); err != nil {
				return err
			}
		}
		managed := append([]string{".agent/modules.json"}, globalManaged...)
		for _, module := range modules {
			schemaPath := filepath.Join(*root, filepath.FromSlash(module.Schema))
			schema, loadErr := generator.Load(ctx, schemaPath)
			if loadErr != nil {
				return loadErr
			}
			result, generateErr := generator.Generate(ctx, schema, generator.Options{
				Root:   *root,
				Source: module.Schema,
				Check:  *check,
			})
			if generateErr != nil {
				return generateErr
			}
			managed = append(managed, result.Managed...)
		}
		stale, err := generator.ReconcileManagedFiles(ctx, *root, managed, *check, *prune)
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(output, "generated modules count=%d checked=%t pruned=%d\n", len(modules), *check, len(stale))
		return err
	}
	if len(args) > 0 && args[0] == "openapi" {
		flags := flag.NewFlagSet("generate openapi", flag.ContinueOnError)
		flags.SetOutput(io.Discard)
		check := flags.Bool("check", false, "check global OpenAPI drift")
		root := flags.String("root", repositoryRoot(), "repository root")
		if err := flags.Parse(args[1:]); err != nil || flags.NArg() != 0 {
			return usage()
		}
		changed, err := generator.GenerateOpenAPI(ctx, generator.GenerateOpenAPIOptions{Root: *root, Check: *check})
		if err != nil {
			return err
		}
		_, err = fmt.Fprintf(output, "generated openapi changed=%t\n", changed)
		return err
	}
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
