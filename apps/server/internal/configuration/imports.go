package configuration

import (
	"errors"
	"os"
	"path/filepath"
)

// ImportPolicy is operator-controlled; browser administrators cannot broaden it.
type ImportPolicy struct {
	Roots []string `json:"importRoots"`
}

func NewImportPolicy(roots []string) (ImportPolicy, error) {
	result := ImportPolicy{Roots: []string{}}
	for _, root := range roots {
		if !filepath.IsAbs(root) {
			return result, errors.New("CAMP_IMPORT_ROOTS requires absolute directory paths")
		}
		real, err := filepath.EvalSymlinks(root)
		if err != nil {
			return result, errors.New("A configured import root is unavailable")
		}
		info, err := os.Stat(real)
		if err != nil || !info.IsDir() {
			return result, errors.New("Import roots must be existing directories")
		}
		result.Roots = append(result.Roots, real)
	}
	return result, nil
}
func (p ImportPolicy) Resolve(candidate string) (string, error) {
	if len(p.Roots) == 0 {
		return "", &Error{"import_disabled", "Server directory import is disabled. Upload files, or ask the operator to configure CAMP_IMPORT_ROOTS."}
	}
	if !filepath.IsAbs(candidate) {
		return "", &Error{"import_denied", "Choose an absolute directory inside a configured import root."}
	}
	real, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", &Error{"import_denied", "The selected import directory is unavailable."}
	}
	for _, root := range p.Roots {
		relative, err := filepath.Rel(root, real)
		if err == nil && (relative == "." || filepath.IsLocal(relative)) {
			return real, nil
		}
	}
	return "", &Error{"import_denied", "The selected directory is outside the operator's allowed import roots."}
}
