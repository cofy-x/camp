//go:build linux

package layerfs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"golang.org/x/sys/unix"
)

func openBridgeLock(home string) (*os.File, error) {
	file, err := os.OpenFile(filepath.Join(home, ".camp-bridge.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("LayerFS home already has an active bridge owner: %w", err)
	}
	return file, nil
}

func configureBridgeProcess(command *exec.Cmd, lock *os.File) {
	// The inherited open file description retains the lock if Camp dies before
	// SIGKILL reaches Node. A replacement server cannot reset while SDK calls
	// from the previous owner still run. The lock is released only by closing
	// every copy; never issue LOCK_UN explicitly while Node owns its copy.
	command.ExtraFiles = []*os.File{lock}
	command.SysProcAttr = &syscall.SysProcAttr{Pdeathsig: syscall.SIGKILL}
}
