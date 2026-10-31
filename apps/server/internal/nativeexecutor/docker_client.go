package nativeexecutor

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
)

// SelectedDockerClient resolves an operator's administrative executable once.
// Persist its absolute invocation path so launchd does not use shell PATH.
// Preserve the symlink spelling: OrbStack's Docker entry is a multicall binary
// whose dispatch depends on argv[0], so executing its resolved target is wrong.
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
	info, err := os.Stat(name)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return "", errors.New("The Docker executable must be a regular executable file")
	}
	return filepath.Abs(name)
}
