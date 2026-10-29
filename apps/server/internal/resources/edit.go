package resources

import (
	"context"
	"errors"
	"strings"
)

func ValidateFilePath(name string) error {
	if !validPath(name) {
		return errors.New("Choose a safe relative file path without credentials or configuration files")
	}
	return nil
}

// WriteFile creates immutable content; it never changes the shared resource pointer.
func (manager *Manager) WriteFile(ctx context.Context, id, base, name string, content []byte) (string, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if !validPath(name) || len(content) > MaxFileBytes {
		return "", errors.New("The file path or size is not supported")
	}
	repo, err := manager.repository(id)
	if err != nil {
		return "", err
	}
	files, err := loadCommit(ctx, repo, base)
	if err != nil {
		return "", err
	}
	for other := range files {
		if other != name && (strings.HasPrefix(name, other+"/") || strings.HasPrefix(other, name+"/")) {
			return "", errors.New("A file conflicts with a directory at this path")
		}
	}
	if _, exists := files[name]; !exists && len(files) >= MaxFiles {
		return "", errors.New("The resource already contains the maximum number of files")
	}
	previous := files[name]
	files[name] = file{content: content, executable: previous.executable}
	total := 0
	for _, f := range files {
		total += len(f.content)
	}
	if total > MaxProjectBytes {
		return "", errors.New("The resource exceeds its total size limit")
	}
	if base == EmptyRevision {
		if err := materializeEmptyResource(ctx, repo); err != nil {
			return "", err
		}
	}
	return saveCommit(ctx, repo, base, files)
}

func (manager *Manager) Restore(ctx context.Context, id, base, revision string) (string, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	repo, err := manager.repository(id)
	if err != nil {
		return "", err
	}
	if _, err := listCommit(ctx, repo, base); err != nil {
		return "", err
	}
	files, err := loadCommit(ctx, repo, revision)
	if err != nil {
		return "", err
	}
	if base == EmptyRevision {
		if err := materializeEmptyResource(ctx, repo); err != nil {
			return "", err
		}
	}
	return saveCommit(ctx, repo, base, files)
}
