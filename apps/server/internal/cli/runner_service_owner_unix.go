//go:build linux || darwin

package cli

import (
	"os"
	"syscall"
)

func serviceDataOwner(directory string) (uint32, error) {
	info, err := os.Stat(directory)
	if err != nil {
		return 0, err
	}
	return info.Sys().(*syscall.Stat_t).Uid, nil
}
