//go:build linux || darwin

package serviceowner

import (
	"errors"
	"fmt"
	"golang.org/x/sys/unix"
	"os"
	"path/filepath"
)

func AcquireDirectory(dir string) (*os.File, error) {
	return acquireDirectory(dir, true)
}
func acquireExistingDirectory(dir string) (*os.File, error) { return acquireDirectory(dir, false) }
func acquireDirectory(dir string, create bool) (*os.File, error) {
	flags := os.O_RDWR | unix.O_NOFOLLOW
	if create {
		flags |= os.O_CREATE
	}
	file, err := os.OpenFile(filepath.Join(dir, ".lock"), flags, 0600)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, errors.New("The Server ownership lock must be a regular file")
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		if errors.Is(err, unix.EWOULDBLOCK) {
			return nil, fmt.Errorf("%w: %w", ErrDirectoryInUse, err)
		}
		return nil, fmt.Errorf("Another Camp instance is using this task directory: %w", err)
	}
	return file, nil
}
