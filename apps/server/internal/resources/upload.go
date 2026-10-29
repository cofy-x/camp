package resources

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

// ValidateUploadName accepts a single filename, never a client-supplied path.
// Browser uploads use the same credential boundary as imported directories.
func ValidateUploadName(name string) error {
	if len(name) > 255 || strings.TrimSpace(name) == "" || strings.Contains(name, "/") || !validPath(name) {
		return &storage.Error{Code: "invalid_upload", Message: "Choose a filename of at most 255 UTF-8 bytes without paths, control characters, or credential names."}
	}
	return nil
}

// Upload preserves original bytes as one immutable, nonexecutable Git snapshot.
// Archives are ordinary files: their contents are never extracted or executed.
func (manager *Manager) Upload(ctx context.Context, id, name string, content []byte) (ImportResult, error) {
	if err := ValidateUploadName(name); err != nil {
		return ImportResult{}, err
	}
	if len(content) > MaxFileBytes {
		return ImportResult{}, &storage.Error{Code: "file_too_large", Message: "Attachments must be no larger than 8 MiB."}
	}
	manager.mu.Lock()
	defer manager.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	repository, err := manager.repository(id)
	if err != nil {
		return ImportResult{}, err
	}
	if _, err := os.Lstat(repository); !errors.Is(err, os.ErrNotExist) {
		return ImportResult{}, errors.New("The upload resource already exists or cannot be accessed")
	}
	if err := os.MkdirAll(manager.root, 0700); err != nil {
		return ImportResult{}, err
	}
	temporary, err := os.MkdirTemp(manager.root, ".upload-")
	if err != nil {
		return ImportResult{}, err
	}
	defer os.RemoveAll(temporary)
	if _, err := git(ctx, temporary, nil, "init", "--bare", "--template=", "--object-format=sha1", "--initial-branch=main", temporary); err != nil {
		return ImportResult{}, err
	}
	commit, err := saveCommit(ctx, temporary, "", snapshot{name: file{content: content}})
	if err != nil {
		return ImportResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return ImportResult{}, err
	}
	if err := os.Rename(temporary, repository); err != nil {
		return ImportResult{}, err
	}
	return ImportResult{Commit: commit, Files: 1}, nil
}
