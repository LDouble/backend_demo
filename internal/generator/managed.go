package generator

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

const managedManifestVersion = 1

// ErrStaleArtifacts indicates generated files no longer belong to the current module plan.
var ErrStaleArtifacts = errors.New("stale generated artifacts require --prune")

type managedManifest struct {
	Version int      `json:"version"`
	Files   []string `json:"files"`
}

// GlobalManagedFiles returns generated outputs produced outside a single module plan.
func GlobalManagedFiles(ctx context.Context, root string, modules []ModuleInfo) ([]string, error) {
	files := []string{
		"api/openapi.yaml",
		"internal/api/generated/api.gen.go",
		"permissions/modules/core.json",
		"internal/infrastructure/mysql/query/gen.go",
		"internal/infrastructure/mysql/query/users.gen.go",
		"internal/infrastructure/mysql/query/roles.gen.go",
		"internal/infrastructure/mysql/query/configs.gen.go",
	}
	for _, module := range modules {
		schema, err := loadRepositorySchema(ctx, root, module.Schema)
		if err != nil {
			return nil, err
		}
		entities := schema.Entities
		if schema.Version == 1 {
			entities = []Entity{schema.Entity}
		}
		for _, entity := range entities {
			files = append(files, "internal/infrastructure/mysql/query/"+entity.Table+".gen.go")
		}
	}
	return uniquePaths(files), nil
}

// FindStaleManagedFiles compares the last managed plan without mutating files.
func FindStaleManagedFiles(root string, expected []string) ([]string, error) {
	manifestPath, err := safeJoin(root, ".agent/generated-files.json")
	if err != nil {
		return nil, err
	}
	data, err := repositoryReadFile(root, manifestPath)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read managed file manifest: %w", err)
	}
	var previous managedManifest
	if err = json.Unmarshal(data, &previous); err != nil || previous.Version != managedManifestVersion {
		return nil, fmt.Errorf("decode managed file manifest")
	}
	return difference(previous.Files, uniquePaths(expected)), nil
}

// ReconcileManagedFiles checks, records, or explicitly prunes stale generated artifacts.
func ReconcileManagedFiles(ctx context.Context, root string, expected []string, check, prune bool) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	expected = uniquePaths(expected)
	manifestPath, err := safeJoin(root, ".agent/generated-files.json")
	if err != nil {
		return nil, err
	}
	previous := managedManifest{Version: managedManifestVersion, Files: []string{}}
	data, readErr := repositoryReadFile(root, manifestPath)
	switch {
	case readErr == nil:
		if err = json.Unmarshal(data, &previous); err != nil || previous.Version != managedManifestVersion {
			return nil, fmt.Errorf("decode managed file manifest")
		}
	case !errors.Is(readErr, fs.ErrNotExist):
		return nil, fmt.Errorf("read managed file manifest: %w", readErr)
	}
	stale := difference(previous.Files, expected)
	if check {
		encoded, encodeErr := encodeManagedManifest(expected)
		if encodeErr != nil {
			return nil, encodeErr
		}
		if len(stale) > 0 {
			return stale, fmt.Errorf("%w: %s", ErrStaleArtifacts, strings.Join(stale, ", "))
		}
		if readErr != nil || !bytes.Equal(data, encoded) {
			return nil, fmt.Errorf("%w: .agent/generated-files.json", ErrDrift)
		}
		return []string{}, nil
	}
	if len(stale) > 0 && !prune {
		return stale, fmt.Errorf("%w: %s", ErrStaleArtifacts, strings.Join(stale, ", "))
	}
	if prune {
		for _, relative := range stale {
			path, joinErr := safeJoin(root, relative)
			if joinErr != nil {
				return nil, joinErr
			}
			if removeErr := removeGeneratedArtifact(root, path); removeErr != nil {
				return nil, fmt.Errorf("prune generated artifact %q: %w", relative, removeErr)
			}
		}
	}
	encoded, err := encodeManagedManifest(expected)
	if err != nil {
		return nil, err
	}
	if err = atomicWrite(root, manifestPath, encoded); err != nil {
		return nil, err
	}
	return stale, nil
}

func removeGeneratedArtifact(root, path string) error {
	data, err := repositoryReadFile(root, path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	generated := bytes.Contains(data, []byte(generatedHeader)) ||
		bytes.Contains(data, []byte("Code generated")) ||
		bytes.Contains(data, []byte("x-generated-module:")) ||
		bytes.Contains(data, []byte(`"module":`))
	if !generated {
		return fmt.Errorf("refusing to delete file without a generated marker")
	}
	return repositoryRemove(root, path)
}

func encodeManagedManifest(files []string) ([]byte, error) {
	data, err := json.MarshalIndent(managedManifest{Version: managedManifestVersion, Files: files}, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode managed file manifest: %w", err)
	}
	return append(data, '\n'), nil
}

func uniquePaths(paths []string) []string {
	seen := make(map[string]struct{}, len(paths))
	result := make([]string, 0, len(paths))
	for _, path := range paths {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		if _, ok := seen[path]; ok {
			continue
		}
		seen[path] = struct{}{}
		result = append(result, path)
	}
	sort.Strings(result)
	return result
}

func difference(previous, expected []string) []string {
	wanted := make(map[string]struct{}, len(expected))
	for _, path := range expected {
		wanted[path] = struct{}{}
	}
	stale := []string{}
	for _, path := range previous {
		if _, ok := wanted[path]; !ok {
			stale = append(stale, path)
		}
	}
	sort.Strings(stale)
	return stale
}
