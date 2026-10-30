//go:build darwin

package sandboxexec

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

const pathOnly = unix.O_EVTONLY

// Pin every directory descriptor and refuse symlinks at every component.
// The native sandbox prevents commands moving these directories outside the
// owned tree. The file service also serializes calls against command admission.
func openComponents(root int, name string, flags int) (int, error) {
	current, err := unix.Openat(root, ".", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	parts := strings.Split(name, "/")
	for index, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			_ = unix.Close(current)
			return -1, unix.EPERM
		}
		options := unix.O_RDONLY | unix.O_DIRECTORY | unix.O_CLOEXEC | unix.O_NOFOLLOW
		if index == len(parts)-1 {
			options = flags | unix.O_CLOEXEC | unix.O_NOFOLLOW | unix.O_NONBLOCK
		}
		next, err := unix.Openat(current, part, options, 0)
		_ = unix.Close(current)
		if err != nil {
			return -1, err
		}
		current = next
	}
	return current, nil
}

func openRoot(root string) (int, error) {
	initial, err := os.Lstat(root)
	if err != nil {
		return -1, err
	}
	if !initial.IsDir() {
		return -1, unix.ENOTDIR
	}
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return -1, err
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return -1, err
	}
	defer unix.Close(fd)
	result, err := openComponents(fd, strings.TrimPrefix(resolved, "/"), unix.O_RDONLY|unix.O_DIRECTORY)
	if err != nil {
		return -1, err
	}
	file := os.NewFile(uintptr(result), root)
	current, err := file.Stat()
	if err != nil || !os.SameFile(initial, current) {
		_ = file.Close()
		return -1, unix.EPERM
	}
	// Keep the os.File alive until its descriptor is duplicated for the caller.
	duplicate, err := unix.FcntlInt(file.Fd(), unix.F_DUPFD_CLOEXEC, 0)
	_ = file.Close()
	return duplicate, err
}

func openRelative(root int, name string, flags int) (*os.File, error) {
	fd, err := openComponents(root, name, flags)
	if err != nil {
		return nil, fileError(err)
	}
	file := os.NewFile(uintptr(fd), name)
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() && !info.IsDir() {
		_ = file.Close()
		return nil, problem("unsupported_file", "Only regular files and directories are supported; symbolic links are not followed.")
	}
	return file, nil
}

func openMetadata(root int, name string) (*os.File, error) {
	fd, err := openComponents(root, name, unix.O_SYMLINK|unix.O_EVTONLY)
	if err != nil {
		return nil, fileError(err)
	}
	return os.NewFile(uintptr(fd), name), nil
}

func renameAt(from int, old string, to int, next string, exclusive bool) error {
	flags := uint32(0)
	if exclusive {
		flags = unix.RENAME_EXCL
	}
	return unix.RenameatxNp(from, old, to, next, flags)
}
