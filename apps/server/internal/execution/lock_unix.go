//go:build linux || darwin

package execution

import (
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

func AcquireLock(dir string) (*os.File, error) {
	file, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("Another Camp instance is using this task directory: %w", err)
	}
	return file, nil
}
