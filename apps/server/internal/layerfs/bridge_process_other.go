//go:build !linux

package layerfs

import (
	"errors"
	"os"
	"os/exec"
)

func openBridgeLock(string) (*os.File, error) {
	return nil, errors.New("Camp LayerFS requires Linux")
}

func configureBridgeProcess(*exec.Cmd, *os.File) {}
