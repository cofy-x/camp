package fsdurability

import (
	"errors"
	"os"
	"syscall"
)

// Entry submits this file's data and metadata without a drive-wide cache flush.
// Stage must follow all entries, including directories, after their handles close.
func Entry(file *os.File) error {
	connection, err := file.SyscallConn()
	if err != nil {
		return err
	}
	var syncErr error
	err = connection.Control(func(fd uintptr) {
		for {
			syncErr = syscall.Fsync(int(fd))
			if !errors.Is(syncErr, syscall.EINTR) {
				break
			}
		}
	})
	return errors.Join(err, syncErr)
}
