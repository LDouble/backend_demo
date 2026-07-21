package generator

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func repositoryRelativePath(root, path string) (string, error) {
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

func withRepositoryRoot[T any](root string, work func(*os.Root) (T, error)) (T, error) {
	var zero T
	repository, err := os.OpenRoot(root)
	if err != nil {
		return zero, fmt.Errorf("open repository root: %w", err)
	}
	defer func() { _ = repository.Close() }()
	return work(repository)
}

func repositoryReadFile(root, path string) ([]byte, error) {
	relative, err := repositoryRelativePath(root, path)
	if err != nil {
		return nil, err
	}
	return withRepositoryRoot(root, func(repository *os.Root) ([]byte, error) {
		return repository.ReadFile(relative)
	})
}

func repositoryReadDir(root, path string) ([]fs.DirEntry, error) {
	relative, err := repositoryRelativePath(root, path)
	if err != nil {
		return nil, err
	}
	return withRepositoryRoot(root, func(repository *os.Root) ([]fs.DirEntry, error) {
		return fs.ReadDir(repository.FS(), relative)
	})
}

func repositoryStat(root, path string) (fs.FileInfo, error) {
	relative, err := repositoryRelativePath(root, path)
	if err != nil {
		return nil, err
	}
	return withRepositoryRoot(root, func(repository *os.Root) (fs.FileInfo, error) {
		return repository.Stat(relative)
	})
}

func repositoryRemove(root, path string) error {
	relative, err := repositoryRelativePath(root, path)
	if err != nil {
		return err
	}
	_, err = withRepositoryRoot(root, func(repository *os.Root) (struct{}, error) {
		return struct{}{}, repository.Remove(relative)
	})
	return err
}

func atomicWrite(root, path string, content []byte) error {
	relative, err := repositoryRelativePath(root, path)
	if err != nil {
		return err
	}
	_, err = withRepositoryRoot(root, func(repository *os.Root) (struct{}, error) {
		parent := filepath.Dir(relative)
		if err := repository.MkdirAll(parent, 0o750); err != nil {
			return struct{}{}, fmt.Errorf("create generated directory: %w", err)
		}
		temporaryName, temporary, err := createRepositoryTemp(repository, parent)
		if err != nil {
			return struct{}{}, err
		}
		defer func() { _ = repository.Remove(temporaryName) }()
		if _, err = temporary.Write(content); err != nil {
			_ = temporary.Close()
			return struct{}{}, fmt.Errorf("write generated temporary file: %w", err)
		}
		if err = temporary.Close(); err != nil {
			return struct{}{}, fmt.Errorf("close generated temporary file: %w", err)
		}
		if err = repository.Rename(temporaryName, relative); err != nil {
			return struct{}{}, fmt.Errorf("replace generated file: %w", err)
		}
		return struct{}{}, nil
	})
	return err
}

func createRepositoryTemp(repository *os.Root, parent string) (string, *os.File, error) {
	for range 10 {
		var random [12]byte
		if _, err := rand.Read(random[:]); err != nil {
			return "", nil, fmt.Errorf("generate temporary file name: %w", err)
		}
		name := filepath.Join(parent, ".campus-generate-"+hex.EncodeToString(random[:]))
		file, err := repository.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err == nil {
			return name, file, nil
		}
		if !os.IsExist(err) {
			return "", nil, fmt.Errorf("create generated temporary file: %w", err)
		}
	}
	return "", nil, fmt.Errorf("create generated temporary file: name collisions")
}
