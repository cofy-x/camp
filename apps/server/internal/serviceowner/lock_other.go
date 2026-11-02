//go:build !linux && !darwin

package serviceowner

import (
	"errors"
	"os"
)

func AcquireDirectory(string) (*os.File, error) {
	return nil, errors.New("Server storage ownership requires Linux or macOS")
}

func acquireExistingDirectory(dir string) (*os.File, error) { return AcquireDirectory(dir) }
