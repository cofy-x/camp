package nativeexecutor

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cofy-x/camp/apps/server/internal/seatbelt"
)

// ResolveToolRoots validates operator-approved read/execute trees. Tools never
// gain write access or the operator's ambient environment from these grants.
func ResolveToolRoots(directory string, roots []string) ([]string, error) {
	if len(roots) > 32 {
		return nil, errors.New("A Runner supports at most 32 approved tool roots")
	}
	data, err := canonicalFuturePath(directory)
	if err != nil {
		return nil, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, err
	}
	home, err = filepath.EvalSymlinks(home)
	if err != nil {
		return nil, err
	}
	protected := []string{data, "/Library/Keychains"}
	for _, name := range []string{".ssh", ".aws", ".azure", ".gnupg", ".config", ".docker", ".kube", ".npmrc", ".netrc", ".pypirc", ".git-credentials", "Library/Keychains"} {
		protected = append(protected, filepath.Join(home, name))
	}
	resolved := make([]string, 0, len(roots))
	for _, root := range roots {
		if err := seatbelt.ValidateRoots([]string{root}); err != nil {
			return nil, err
		}
		root, err := filepath.EvalSymlinks(root)
		if err != nil {
			return nil, err
		}
		info, err := os.Stat(root)
		if err != nil || !info.IsDir() {
			return nil, errors.New("Approved tool roots must be existing directories")
		}
		for _, secret := range protected {
			if pathsOverlap(root, secret) {
				return nil, errors.New("Approved tool roots cannot expose Runner storage or private credential directories")
			}
		}
		resolved = append(resolved, root)
	}
	slices.Sort(resolved)
	return slices.Compact(resolved), nil
}

func pathsOverlap(a, b string) bool {
	return a == b || strings.HasPrefix(a, b+string(filepath.Separator)) || strings.HasPrefix(b, a+string(filepath.Separator))
}

// A first setup may not yet have created its private directory. Resolve the
// nearest existing ancestor so OS aliases cannot bypass the overlap check.
func canonicalFuturePath(name string) (string, error) {
	name, err := filepath.Abs(name)
	if err != nil {
		return "", err
	}
	var missing []string
	for {
		resolved, err := filepath.EvalSymlinks(name)
		if err == nil {
			for i := len(missing) - 1; i >= 0; i-- {
				resolved = filepath.Join(resolved, missing[i])
			}
			return resolved, nil
		}
		if !errors.Is(err, os.ErrNotExist) || name == filepath.Dir(name) {
			return "", err
		}
		missing = append(missing, filepath.Base(name))
		name = filepath.Dir(name)
	}
}
