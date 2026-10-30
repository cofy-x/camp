//go:build linux

package sandboxexec

import (
	"golang.org/x/sys/unix"
	"os"
)

const resolveFlags = unix.RESOLVE_BENEATH | unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS
const pathOnly = unix.O_PATH

func openRoot(root string) (int, error) {
	return unix.Openat2(unix.AT_FDCWD, root, &unix.OpenHow{Flags: unix.O_PATH | unix.O_DIRECTORY | unix.O_CLOEXEC, Resolve: unix.RESOLVE_NO_SYMLINKS | unix.RESOLVE_NO_MAGICLINKS})
}
func renameAt(from int, old string, to int, next string, exclusive bool) error {
	flags := uint(0)
	if exclusive {
		flags = unix.RENAME_NOREPLACE
	}
	return unix.Renameat2(from, old, to, next, flags)
}

func openRelative(root int, name string, flags int) (*os.File, error) {
	flags |= unix.O_CLOEXEC | unix.O_NOFOLLOW
	if flags&unix.O_PATH == 0 {
		flags |= unix.O_NONBLOCK
	}
	fd, err := unix.Openat2(root, name, &unix.OpenHow{Flags: uint64(flags), Resolve: resolveFlags})
	if err != nil {
		return nil, fileError(err)
	}
	f := os.NewFile(uintptr(fd), name)
	info, err := f.Stat()
	if err != nil || (!info.Mode().IsRegular() && !info.IsDir()) {
		_ = f.Close()
		return nil, problem("unsupported_file", "Only regular files and directories are supported; symbolic links are not followed.")
	}
	return f, nil
}

func openMetadata(root int, name string) (*os.File, error) {
	// O_PATH + O_NOFOLLOW permits inspecting the final link itself. Resolution
	// still rejects links in every ancestor and never reads a link target.
	fd, err := unix.Openat2(root, name, &unix.OpenHow{Flags: unix.O_PATH | unix.O_NOFOLLOW | unix.O_CLOEXEC, Resolve: resolveFlags})
	if err != nil {
		return nil, fileError(err)
	}
	return os.NewFile(uintptr(fd), name), nil
}
