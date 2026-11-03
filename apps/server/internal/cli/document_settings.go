package cli

import (
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/cofy-x/camp/apps/server/internal/documenttext"
)

// Reader paths have a separate process-only owner from private model settings.
func resolveDocumentSettings(getenv func(string) string, cwd, program string) documenttext.Config {
	config, packaged, err := documenttext.PackagedConfig(program)
	if err != nil {
		config.InstallationError = err
		return config
	}
	if !packaged {
		config.RuntimeDir = documentRuntime(getenv, cwd)
		config.NodePath, _ = exec.LookPath("node")
		config.HelperPath = filepath.Join(filepath.Dir(program), "camp-sandbox")
	}
	return applyDocumentOverrides(config, getenv, cwd)
}

func applyDocumentOverrides(config documenttext.Config, getenv func(string) string, cwd string) documenttext.Config {
	if getenv("CAMP_DOCUMENT_RUNTIME") != "" {
		config.RuntimeDir = documentRuntime(getenv, cwd)
	}
	if node := getenv("CAMP_DOCUMENT_NODE"); node != "" {
		config.NodePath = node
		if !filepath.IsAbs(node) && strings.ContainsRune(node, filepath.Separator) {
			node = filepath.Join(cwd, node)
		}
		selected, err := exec.LookPath(node)
		if err != nil {
			config.InstallationError = &documenttext.Failure{Code: "unavailable", Message: "CAMP_DOCUMENT_NODE does not identify an executable. Fix that process setting before preparing documents.", Retryable: true}
			return config
		}
		config.NodePath = selected
	}
	return config
}
