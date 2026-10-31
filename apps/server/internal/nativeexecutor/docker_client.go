package nativeexecutor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

// SelectedDockerClient resolves an operator's administrative executable once.
// Persist its canonical path during setup so launchd does not use shell PATH.
func SelectedDockerClient(selected string) (string, error) {
	if selected == "" {
		selected = "docker"
	} else if !filepath.IsAbs(selected) {
		return "", errors.New("Choose an absolute Docker executable")
	}
	name, err := exec.LookPath(selected)
	if err != nil {
		return "", err
	}
	name, err = filepath.EvalSymlinks(name)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(name)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", errors.New("The Docker executable must be a regular executable file")
	}
	return filepath.Abs(name)
}
