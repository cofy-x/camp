// Package projectenv prepares reproducible Node dependencies and independent checks.
package projectenv

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"

	"github.com/cofy-x/camp/apps/server/internal/resources"
	"go.yaml.in/yaml/v3"
)

const NodeVersion = "24.16.0"
const PackageManager = "pnpm@12.11.2"

type Check struct {
	Name      string   `json:"name"`
	Script    string   `json:"script"`
	Directory string   `json:"directory,omitempty"`
	Arguments []string `json:"arguments,omitempty"`
}

type Profile struct {
	Kind           string  `json:"kind"`
	NodeVersion    string  `json:"nodeVersion"`
	PackageManager string  `json:"packageManager"`
	Checks         []Check `json:"checks"`
}

type manifest struct {
	Name                 string            `json:"name"`
	PackageManager       string            `json:"packageManager"`
	Scripts              map[string]string `json:"scripts"`
	Dependencies         map[string]string `json:"dependencies"`
	DevDependencies      map[string]string `json:"devDependencies"`
	OptionalDependencies map[string]string `json:"optionalDependencies"`
	DependenciesMeta     map[string]struct {
		Injected bool `json:"injected"`
	} `json:"dependenciesMeta"`
	PNPM struct {
		PatchedDependencies map[string]json.RawMessage `json:"patchedDependencies"`
	} `json:"pnpm"`
}

var scriptPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9:_-]{0,79}$`)

func safeDirectory(value string) bool {
	return value == "" || (fs.ValidPath(value) && value != "." && !strings.Contains(value, "\\") && strings.IndexFunc(value, unicode.IsControl) < 0 && len(value) <= 512)
}

func Validate(profile Profile) error {
	if profile.Kind != "node" && profile.Kind != "command" {
		return errors.New("Choose a Node or command environment")
	}
	if profile.Kind == "node" && (profile.NodeVersion != NodeVersion || (profile.PackageManager != "node" && profile.PackageManager != PackageManager)) {
		return errors.New("Project environments require Node 24.16.0 and node or pnpm@12.11.2")
	}
	if len(profile.Checks) > 8 {
		return errors.New("Configure at most eight checks")
	}
	names := map[string]bool{}
	for _, check := range profile.Checks {
		if !scriptPattern.MatchString(check.Name) || names[check.Name] || !safeDirectory(check.Directory) || (check.Script != "" && !scriptPattern.MatchString(check.Script)) || (profile.PackageManager == "node" && check.Script != "") {
			return errors.New("Check names must be unique, scripts must name a package script, and directories must stay within the project")
		}
		if profile.Kind == "command" {
			if check.Script != "" || len(check.Arguments) == 0 || len(check.Arguments) > 64 || strings.TrimSpace(check.Arguments[0]) == "" {
				return errors.New("Command checks require an executable and at most 64 arguments")
			}
			for _, argument := range check.Arguments {
				if len(argument) > 8192 || strings.ContainsRune(argument, '\x00') {
					return errors.New("Command arguments must be bounded and cannot contain NUL")
				}
			}
		} else if len(check.Arguments) != 0 {
			return errors.New("Use a command environment for explicit command arguments")
		}
		names[check.Name] = true
	}
	return nil
}

// SnapshotPolicy excludes tool-managed dependencies and caches only when that
// environment is explicitly selected. Generic resources keep these names valid.
func SnapshotPolicy(profile Profile) resources.Policy {
	if profile.Kind == "node" {
		return resources.Policy{ExcludeNames: []string{"node_modules", ".pnpm-store", ".cache", ".next", ".data"}}
	}
	return resources.Policy{}
}

// Detect reads project declarations, never executes a package script or config file.
func Detect(projectDir string) (Profile, error) {
	root, err := os.OpenRoot(projectDir)
	if err != nil {
		return Profile{}, err
	}
	defer root.Close()
	data, err := readRegular(root, "package.json", 1<<20)
	if err != nil {
		return Profile{}, errors.New("The project must contain a regular package.json")
	}
	var config manifest
	if err := json.Unmarshal(data, &config); err != nil {
		return Profile{}, errors.New("package.json is not a valid project manifest")
	}
	profile := Profile{Kind: "node", NodeVersion: NodeVersion, PackageManager: "node", Checks: []Check{{Name: "test"}}}
	dependencyCount := len(config.Dependencies) + len(config.DevDependencies) + len(config.OptionalDependencies)
	_, lockErr := root.Stat("pnpm-lock.yaml")
	if config.PackageManager != "" || dependencyCount > 0 || lockErr == nil {
		if config.PackageManager != PackageManager {
			return Profile{}, fmt.Errorf("Set packageManager to %s before importing this project", PackageManager)
		}
		if _, err := readRegular(root, "pnpm-lock.yaml", 8<<20); err != nil {
			return Profile{}, errors.New("A pnpm project requires a regular pnpm-lock.yaml")
		}
		profile.PackageManager = PackageManager
		profile.Checks = nil
		for _, name := range []string{"test", "typecheck", "build"} {
			if config.Scripts[name] != "" {
				profile.Checks = append(profile.Checks, Check{Name: name, Script: name})
			}
		}
		if len(profile.Checks) == 0 {
			profile.Checks = []Check{{Name: "test"}}
		}
	}
	return profile, Validate(profile)
}

func readRegular(root *os.Root, name string, limit int64) ([]byte, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > limit {
		return nil, fmt.Errorf("%s must be a regular file within its size limit", name)
	}
	data, err := root.ReadFile(name)
	if err != nil || int64(len(data)) > limit {
		return nil, fmt.Errorf("Cannot safely read %s", name)
	}
	return data, nil
}

// validateManifests rejects dependency paths that could refer outside the imported
// source and package-manager hooks that could run during dependency preparation.
func validateManifests(projectDir string) error {
	root, err := os.OpenRoot(projectDir)
	if err != nil {
		return err
	}
	defer root.Close()
	return filepath.WalkDir(projectDir, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() && entry.Name() == "node_modules" {
			return fs.SkipDir
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return errors.New("Project configuration cannot contain symbolic links")
		}
		if entry.Name() == ".pnpmfile.cjs" || entry.Name() == ".pnpmfile.js" {
			return errors.New("pnpm hook files are not supported in project environments")
		}
		if entry.Name() == "pnpm-workspace.yaml" {
			relative, _ := filepath.Rel(projectDir, name)
			data, err := readRegular(root, relative, 1<<20)
			if err != nil {
				return err
			}
			var config map[string]any
			if yaml.Unmarshal(data, &config) != nil {
				return errors.New("pnpm-workspace.yaml is invalid")
			}
			for _, key := range []string{"globalPnpmfile", "pnpmfile", "globalDir", "globalBinDir", "storeDir", "virtualStoreDir", "modulesDir", "useNodeVersion", "nodeVersion", "managePackageManagerVersions"} {
				if _, exists := config[key]; exists {
					return fmt.Errorf("Project environment controls the pnpm workspace setting %s", key)
				}
			}
			if packages, ok := config["packages"].([]any); ok {
				for _, item := range packages {
					pattern, ok := item.(string)
					if !ok || strings.Contains(pattern, "..") || strings.HasPrefix(pattern, "/") || strings.Contains(pattern, "\\") {
						return errors.New("pnpm workspace package patterns must stay within the project")
					}
				}
			}
		}
		if entry.Name() != "package.json" || entry.IsDir() {
			return nil
		}
		relative, _ := filepath.Rel(projectDir, name)
		data, err := readRegular(root, relative, 1<<20)
		if err != nil {
			return err
		}
		var config manifest
		if json.Unmarshal(data, &config) != nil {
			return fmt.Errorf("Invalid package manifest: %s", name)
		}
		for _, dependencies := range []map[string]string{config.Dependencies, config.DevDependencies, config.OptionalDependencies} {
			for _, value := range dependencies {
				if strings.HasPrefix(value, "file:") || strings.HasPrefix(value, "link:") {
					relative := value[strings.IndexByte(value, ':')+1:]
					resolved := filepath.Clean(filepath.Join(filepath.Dir(name), relative))
					rel, err := filepath.Rel(projectDir, resolved)
					if err != nil || !filepath.IsLocal(rel) {
						return errors.New("Local dependencies must stay within the imported project")
					}
				}
			}
		}
		return nil
	})
}
