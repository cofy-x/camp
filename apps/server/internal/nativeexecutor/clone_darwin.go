//go:build darwin

package nativeexecutor

import (
	"errors"
	"golang.org/x/sys/unix"
	"os"
)

// APFS clones share clean extents while keeping every attempt's future writes
// private. Filesystems without cloning retain the bounded ordinary copy path.
func cloneDependencyFile(source, target string, mode os.FileMode) (bool, error) {
	err := unix.Clonefile(source, target, unix.CLONE_NOFOLLOW)
	if errors.Is(err, unix.EXDEV) || errors.Is(err, unix.ENOTSUP) || errors.Is(err, unix.ENOSYS) || errors.Is(err, unix.EINVAL) {
		return false, nil
	}
	if err != nil {
		return true, err
	}
	return true, os.Chmod(target, mode)
}
