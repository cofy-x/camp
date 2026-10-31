//go:build darwin

package layerfs

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"golang.org/x/sys/unix"
)

func openBridgeLock(home string) (*os.File, error) {
	file, err := os.OpenFile(filepath.Join(home, ".camp-bridge.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("LayerFS checkpoint home already has an owner: %w", err)
	}
	return file, nil
}

func configureBridgeProcess(command *exec.Cmd, lock *os.File) {
	// The inherited description retains ownership while Node drains an in-flight
	// checkpoint after Runner death. EOF closes admission; it never replays work.
	command.ExtraFiles = []*os.File{lock}
}
