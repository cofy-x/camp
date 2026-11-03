package documenttext

import (
	"errors"
	"os"
	"path/filepath"
)

// A reader runtime is a recursive read grant, unlike its exact executable
// files. Never let an operator path mistake expose private Server/job state.
func readerRuntimeDirectory(config Config) (string, error) {
	root, err := filepath.Abs(config.RuntimeDir)
	if err == nil && config.RuntimeDir != "" {
		root, err = filepath.EvalSymlinks(root)
	}
	if err != nil || config.RuntimeDir == "" {
		return "", failure("unavailable", "The fixed document runtime is unavailable.", true)
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return "", failure("unavailable", "The document runtime must be an existing directory.", true)
	}
	if config.JobRoot != "" {
		jobRoot, err := filepath.Abs(config.JobRoot)
		if err != nil {
			return "", failure("unavailable", "Private document reader storage is unavailable.", true)
		}
		for _, protected := range []string{filepath.Dir(jobRoot), jobRoot} {
			contains, err := runtimeContainsPrivateRoot(info, protected)
			if err != nil {
				return "", failure("unavailable", "Private document reader storage could not be verified.", true)
			}
			if contains {
				return "", failure("unavailable", "The document runtime cannot contain private Server data or reader jobs. Select a separate reader runtime directory.", true)
			}
		}
	}
	entry, err := os.Lstat(filepath.Join(root, "extract.mjs"))
	if err != nil || !entry.Mode().IsRegular() {
		return "", failure("unavailable", "The fixed PDF reader entrypoint is missing or linked.", true)
	}
	return root, nil
}

func runtimeContainsPrivateRoot(runtime os.FileInfo, protected string) (bool, error) {
	current, err := filepath.EvalSymlinks(protected)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	// Compare actual directory identity, not spelling: case-insensitive APFS and
	// Unicode aliases can name the same parent using different strings.
	for {
		info, err := os.Stat(current)
		if err != nil {
			return false, err
		}
		if os.SameFile(runtime, info) {
			return true, nil
		}
		parent := filepath.Dir(current)
		if parent == current {
			return false, nil
		}
		current = parent
	}
}
