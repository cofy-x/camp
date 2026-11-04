package cli

import (
	"errors"
	"os"
	"syscall"
)

// fsync each staging entry without flushing the entire device cache per file.
// syncRuntimeStage supplies the full flush after all bounded I/O has drained.
func syncRuntimeEntry(file *os.File) error {
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
