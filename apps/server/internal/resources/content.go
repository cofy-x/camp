package resources

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
)

// Create provisions a resource without a source directory or native runtime.
// Its canonical empty Git revision is materialized only before the first capture.
func (manager *Manager) Create(ctx context.Context, id string) (ImportResult, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return ImportResult{}, err
	}
	repository, err := manager.repository(id)
	if err != nil {
		return ImportResult{}, err
	}
	if _, err := os.Lstat(repository); !errors.Is(err, os.ErrNotExist) {
		return ImportResult{}, errors.New("The resource already exists or cannot be accessed")
	}
	if err := os.MkdirAll(manager.root, 0700); err != nil {
		return ImportResult{}, err
	}
	temporary, err := os.MkdirTemp(manager.root, ".import-")
	if err != nil {
		return ImportResult{}, err
	}
	defer os.RemoveAll(temporary)
	if err := os.WriteFile(filepath.Join(temporary, emptyResourceMarker), []byte(EmptyRevision+"\n"), 0600); err != nil {
		return ImportResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ImportResult{}, err
	}
	if err := os.Rename(temporary, repository); err != nil {
		return ImportResult{}, err
	}
	return ImportResult{Commit: EmptyRevision}, nil
}

func (manager *Manager) ListFiles(ctx context.Context, id, commit string) ([]string, error) {
	repository, err := manager.repository(id)
	if err != nil {
		return nil, err
	}
	files, err := listCommit(ctx, repository, commit)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(files))
	for name := range files {
		names = append(names, name)
	}
	slices.Sort(names)
	return names, nil
}

func (manager *Manager) ReadFile(ctx context.Context, id, commit, name string) ([]byte, error) {
	if !validPath(name) || filepath.ToSlash(name) != name {
		return nil, errors.New("The resource file path is invalid")
	}
	repository, err := manager.repository(id)
	if err != nil {
		return nil, err
	}
	files, err := listCommit(ctx, repository, commit)
	if err != nil {
		return nil, err
	}
	file, ok := files[name]
	if !ok {
		return nil, os.ErrNotExist
	}
	return readBlob(ctx, repository, file.object)
}
