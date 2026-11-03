// Package serviceowner fences Server startup and offline maintenance.
package serviceowner

import (
	"errors"
	"os"
	"path/filepath"
	"sync"
)

// Ownership fences service startup, migrations, and run execution with the same
// lock inherited by sandbox processes. NewWithOwnership consumes it exactly once.
type Ownership struct {
	dir      string
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
	return &Ownership{dir: dir, lock: lock}, nil
}

// Close releases ownership before transfer. After transfer the manager owns it.
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

func (o *Ownership) Take() (string, *os.File, error) {
	if o == nil {
		return "", nil, errors.New("Execution ownership is required")
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.released || o.dir == "" {
		return "", nil, errors.New("Execution ownership was already released or transferred")
	}
	o.released = true
	return o.dir, o.lock, nil
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
	return &Ownership{dir: dir, lock: lock}, nil
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
