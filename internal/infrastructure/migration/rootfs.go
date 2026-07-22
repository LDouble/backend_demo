package migration

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func lifecycleRelativePath(root, path string) (string, error) {
	root, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve repository root: %w", err)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(root, filepath.FromSlash(path))
	}
	path, err = filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve repository path: %w", err)
	}
	relative, err := filepath.Rel(root, path)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return "", fmt.Errorf("path escapes repository root: %s", path)
	}
	return relative, nil
}

func withLifecycleRoot[T any](root string, work func(*os.Root) (T, error)) (T, error) {
	var zero T
	repository, err := os.OpenRoot(root)
	if err != nil {
		return zero, fmt.Errorf("open repository root: %w", err)
	}
	defer func() { _ = repository.Close() }()
	return work(repository)
}

func readLifecycleFile(root, path string) ([]byte, error) {
	relative, err := lifecycleRelativePath(root, path)
	if err != nil {
		return nil, err
	}
	return withLifecycleRoot(root, func(repository *os.Root) ([]byte, error) {
		return repository.ReadFile(relative)
	})
}

func readLifecycleDir(root, path string) ([]fs.DirEntry, error) {
	relative, err := lifecycleRelativePath(root, path)
	if err != nil {
		return nil, err
	}
	return withLifecycleRoot(root, func(repository *os.Root) ([]fs.DirEntry, error) {
		return fs.ReadDir(repository.FS(), relative)
	})
}

func removeLifecycleFile(root, path string) error {
	relative, err := lifecycleRelativePath(root, path)
	if err != nil {
		return err
	}
	_, err = withLifecycleRoot(root, func(repository *os.Root) (struct{}, error) {
		return struct{}{}, repository.Remove(relative)
	})
	return err
}

func lifecycleFileExists(root, path string) bool {
	relative, err := lifecycleRelativePath(root, path)
	if err != nil {
		return false
	}
	_, err = withLifecycleRoot(root, func(repository *os.Root) (fs.FileInfo, error) {
		return repository.Stat(relative)
	})
	return err == nil
}

func atomicWriteLifecycle(root, path string, data []byte) error {
	return atomicFileLifecycle(root, path, data, false)
}

func atomicReplaceLifecycle(root, path string, data []byte) error {
	return atomicFileLifecycle(root, path, data, true)
}

func atomicFileLifecycle(root, path string, data []byte, replace bool) error {
	relative, err := lifecycleRelativePath(root, path)
	if err != nil {
		return err
	}
	_, err = withLifecycleRoot(root, func(repository *os.Root) (struct{}, error) {
		parent := filepath.Dir(relative)
		if err := repository.MkdirAll(parent, 0o750); err != nil {
			return struct{}{}, fmt.Errorf("create migration directory: %w", err)
		}
		if !replace {
			if _, statErr := repository.Stat(relative); statErr == nil {
				return struct{}{}, fmt.Errorf("refusing to overwrite %s", path)
			} else if !os.IsNotExist(statErr) {
				return struct{}{}, fmt.Errorf("inspect migration file: %w", statErr)
			}
		}
		temporaryName, temporary, err := createLifecycleTemp(repository, parent)
		if err != nil {
			return struct{}{}, err
		}
		defer func() { _ = repository.Remove(temporaryName) }()
		if _, err = temporary.Write(data); err != nil {
			_ = temporary.Close()
			return struct{}{}, err
		}
		if err = temporary.Sync(); err != nil {
			_ = temporary.Close()
			return struct{}{}, err
		}
		if err = temporary.Close(); err != nil {
			return struct{}{}, err
		}
		if err = repository.Rename(temporaryName, relative); err != nil {
			return struct{}{}, fmt.Errorf("promote migration file: %w", err)
		}
		return struct{}{}, nil
	})
	return err
}

func createLifecycleTemp(repository *os.Root, parent string) (string, *os.File, error) {
	for range 10 {
		var random [12]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", nil, fmt.Errorf("generate migration temporary name: %w", err)
		}
		name := filepath.Join(parent, ".campus-migration-"+hex.EncodeToString(random[:]))
		file, err := repository.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o640)
		if err == nil {
			return name, file, nil
		}
		if !os.IsExist(err) {
			return "", nil, fmt.Errorf("create migration temporary file: %w", err)
		}
	}
	return "", nil, fmt.Errorf("create migration temporary file: name collisions")
}
