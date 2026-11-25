// Package serviceowner fences Server startup and offline maintenance.
package serviceowner

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// Ownership holds the Server data-directory lock until service shutdown or
// stopped maintenance completes.
type Ownership struct {
	mu       sync.Mutex
	lock     *os.File
	released bool
}

func Acquire(dataDir string) (*Ownership, error) {
	dir, err := filepath.Abs(filepath.Join(dataDir, "runs"))
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	if err := validateOwnershipDirectory(dir); err != nil {
		return nil, err
	}
	lock, err := AcquireDirectory(dir)
	if err != nil {
		return nil, err
	}
	return &Ownership{lock: lock}, nil
}

// Close releases ownership at most once.
func (o *Ownership) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.released {
		return nil
	}
	o.released = true
	if o.lock != nil {
		return o.lock.Close()
	}
	return nil
}

// AcquireExisting fences offline inspection without creating a directory or lock.
func AcquireExisting(dataDir string) (*Ownership, error) {
	dir, err := filepath.Abs(filepath.Join(dataDir, "runs"))
	if err != nil {
		return nil, err
	}
	if err = validateOwnershipDirectory(dir); err != nil {
		return nil, err
	}
	lock, err := acquireExistingDirectory(dir)
	if err != nil {
		return nil, err
	}
	return &Ownership{lock: lock}, nil
}

func validateOwnershipDirectory(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("The Server ownership directory must be a real directory")
	}
	return nil
}
