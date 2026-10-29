package projectenv

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"go.yaml.in/yaml/v3"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/execution"
	"github.com/cofy-x/camp/apps/server/internal/resources"
)

type Options struct {
	Profile                                  *Profile
	NodePath, PnpmPath, ProjectDir, CacheDir string
	Lock                                     *os.File
	Output                                   func(string) error
}

type Environment struct {
	Profile  Profile
	Key      string
	Mounts   []execution.Mount
	PnpmPath string
}

// Attach prepares empty mount points in an owned run directory. Dependencies
// themselves remain outside the source tree and are mounted only by execution.
func (environment *Environment) Attach(projectDir string) error {
	return mountDirectories(projectDir, environment.Mounts)
}

// FindPnpm resolves the separately installed, pinned standalone package manager.
// runtimeDir is tools/dsh-runtime; its sibling is tools/project-runtime.
func FindPnpm(runtimeDir string) (string, error) {
	directory := filepath.Join(filepath.Dir(runtimeDir), "project-runtime", "node_modules", "pnpm")
	directory, err := filepath.Abs(directory)
	if err != nil {
		return "", err
	}
	directory, err = filepath.EvalSymlinks(directory)
	if err != nil {
		return "", errors.New("Install the pinned project runtime with make install-project-runtime")
	}
	candidate := filepath.Join(filepath.Dir(directory), "@pnpm", "exe."+runtime.GOOS+"-"+runtime.GOARCH, "pnpm")
	if runtime.GOARCH == "amd64" {
		candidate = filepath.Join(filepath.Dir(directory), "@pnpm", "exe."+runtime.GOOS+"-x64", "pnpm")
	}
	// pnpm keeps optional platform packages alongside the package in its virtual store.
	if _, err := os.Stat(candidate); err != nil {
		candidate = filepath.Join(directory, "pnpm")
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", errors.New("The pinned project runtime executable is missing")
	}
	return resolved, nil
}

func emit(options Options, message string) error {
	if options.Output != nil {
		return options.Output(message)
	}
	return nil
}

func toolVersion(ctx context.Context, executable, expected string) error {
	cmd := exec.CommandContext(ctx, executable, "--version")
	cmd.Dir = os.TempDir()
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "npm_config_manage_package_manager_versions=false"}
	data, err := cmd.Output()
	if err != nil || strings.TrimSpace(string(data)) != expected {
		return fmt.Errorf("The configured executable must report version %s", expected)
	}
	return nil
}

// Prepare publishes only successful installs. All package installation executes
// inside Bubblewrap without install scripts, host npm configuration, or secrets.
func Prepare(ctx context.Context, options Options) (*Environment, error) {
	if options.Profile != nil && options.Profile.Kind == "command" {
		if err := Validate(*options.Profile); err != nil {
			return nil, err
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return &Environment{Profile: *options.Profile}, nil
	}
	profile, err := Detect(options.ProjectDir)
	if err != nil {
		return nil, err
	}
	if options.Profile != nil {
		if err := Validate(*options.Profile); err != nil {
			return nil, err
		}
		if options.Profile.PackageManager != profile.PackageManager {
			return nil, errors.New("The project package manager changed; update its environment settings")
		}
		profile = *options.Profile
	}
	environment := &Environment{Profile: profile}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if profile.PackageManager == "node" {
		return environment, nil
	}
	if err := validateManifests(options.ProjectDir); err != nil {
		return nil, err
	}
	if options.PnpmPath == "" {
		options.PnpmPath, err = FindPnpm("tools/dsh-runtime")
		if err != nil {
			return nil, err
		}
	}
	if err := toolVersion(ctx, options.NodePath, "v"+NodeVersion); err != nil {
		return nil, err
	}
	if err := toolVersion(ctx, options.PnpmPath, strings.TrimPrefix(PackageManager, "pnpm@")); err != nil {
		return nil, err
	}
	environment.PnpmPath = options.PnpmPath
	if !filepath.IsAbs(options.CacheDir) {
		return nil, errors.New("The project environment cache requires an absolute directory")
	}
	if err := os.MkdirAll(options.CacheDir, 0700); err != nil {
		return nil, err
	}
	temporary, err := os.MkdirTemp(options.CacheDir, ".prepare-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(temporary)
	project := filepath.Join(temporary, "project")
	if err := resources.CopySourceWithPolicy(ctx, options.ProjectDir, project, SnapshotPolicy(profile)); err != nil {
		return nil, err
	}
	key, err := fingerprint(ctx, project)
	if err != nil {
		return nil, err
	}
	environment.Key = key
	target := filepath.Join(options.CacheDir, key)
	if marker, err := os.ReadFile(filepath.Join(target, "ready")); err == nil && string(marker) == key {
		mounts, err := dependencyMounts(filepath.Join(target, "project"))
		if err != nil {
			return nil, err
		}
		environment.Mounts = mounts
		if err := mountDirectories(options.ProjectDir, mounts); err != nil {
			return nil, err
		}
		return environment, emit(options, "Reusing the prepared pnpm environment.\n")
	}
	if _, err := os.Lstat(target); !errors.Is(err, os.ErrNotExist) {
		return nil, errors.New("The saved dependency environment is incomplete; remove its cache entry before retrying")
	}
	store := filepath.Join(options.CacheDir, "store")
	if err := os.MkdirAll(store, 0700); err != nil {
		return nil, err
	}
	if err := emit(options, "Installing locked dependencies with pnpm 12.11.2; package install scripts are disabled.\n"); err != nil {
		return nil, err
	}
	cmd, err := execution.ProjectCommand(execution.ProjectConfig{NodePath: options.NodePath, PnpmPath: options.PnpmPath, ProjectDir: project, CacheDir: store, Network: true, Arguments: []string{"/pnpm", "install", "--frozen-lockfile", "--ignore-scripts", "--ignore-pnpmfile", "--store-dir=/store", "--package-import-method=copy", "--reporter=append-only"}}, options.Lock)
	if err != nil {
		return nil, err
	}
	installContext, cancel := context.WithTimeout(ctx, 180*time.Second)
	defer cancel()
	code, err := execution.RunTests(installContext, cmd, func(line string) error { return emit(options, line) })
	if err != nil || code != 0 {
		return nil, fmt.Errorf("Dependency preparation failed (exit %d): %w", code, err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(temporary, "ready"), []byte(key), 0600); err != nil {
		return nil, err
	}
	if err := os.Rename(temporary, target); err != nil {
		return nil, fmt.Errorf("Cannot publish dependency environment: %w", err)
	}
	environment.Mounts, err = dependencyMounts(filepath.Join(target, "project"))
	if err != nil {
		return nil, err
	}
	return environment, mountDirectories(options.ProjectDir, environment.Mounts)
}

func fingerprint(ctx context.Context, directory string) (string, error) {
	hash := sha256.New()
	io.WriteString(hash, NodeVersion+"\x00"+PackageManager+"\x00"+runtime.GOOS+"\x00"+runtime.GOARCH+"\x00v2\x00")
	allSource := false
	err := filepath.WalkDir(directory, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if entry.Name() == "pnpm-workspace.yaml" {
			data, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			var settings struct {
				PatchedDependencies     map[string]any `yaml:"patchedDependencies"`
				InjectWorkspacePackages bool           `yaml:"injectWorkspacePackages"`
			}
			if err := yaml.Unmarshal(data, &settings); err != nil {
				return errors.New("Invalid workspace dependency settings")
			}
			if len(settings.PatchedDependencies) > 0 || settings.InjectWorkspacePackages {
				allSource = true
			}
		}
		if entry.Name() == "package.json" {
			data, err := os.ReadFile(name)
			if err != nil {
				return err
			}
			var config manifest
			if json.Unmarshal(data, &config) != nil {
				return errors.New("Invalid dependency manifest")
			}
			for _, dependencies := range []map[string]string{config.Dependencies, config.DevDependencies, config.OptionalDependencies} {
				for _, value := range dependencies {
					if strings.HasPrefix(value, "file:") {
						allSource = true
					}
				}
			}
			if len(config.PNPM.PatchedDependencies) > 0 {
				allSource = true
			}
			for _, metadata := range config.DependenciesMeta {
				if metadata.Injected {
					allSource = true
				}
			}
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	err = filepath.WalkDir(directory, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if !allSource && entry.Name() != "package.json" && entry.Name() != "pnpm-lock.yaml" && entry.Name() != "pnpm-workspace.yaml" {
			return nil
		}
		if !entry.Type().IsRegular() {
			return errors.New("Dependency inputs must be regular files")
		}
		relative, _ := filepath.Rel(directory, name)
		io.WriteString(hash, relative+"\x00")
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		fmt.Fprintf(hash, "%d\x00", len(data))
		hash.Write(data)
		return nil
	})
	return hex.EncodeToString(hash.Sum(nil)), err
}

func dependencyMounts(project string) ([]execution.Mount, error) {
	mounts := []execution.Mount{}
	err := filepath.WalkDir(project, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.Name() != "node_modules" {
			return nil
		}
		if !entry.IsDir() {
			return errors.New("A dependency mount must be a directory")
		}
		for _, cache := range []string{".vite-temp", ".vite", ".vitest-cache", ".cache"} {
			if err := os.MkdirAll(filepath.Join(name, cache), 0700); err != nil {
				return err
			}
		}
		relative, _ := filepath.Rel(project, name)
		mounts = append(mounts, execution.Mount{Source: name, Target: filepath.ToSlash(relative)})
		return fs.SkipDir
	})
	return mounts, err
}

func mountDirectories(project string, mounts []execution.Mount) error {
	root, err := os.OpenRoot(project)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, mount := range mounts {
		if err := root.MkdirAll(mount.Target, 0700); err != nil {
			return err
		}
		info, err := root.Lstat(mount.Target)
		if err != nil || !info.IsDir() {
			return errors.New("A dependency mount target is not a regular directory")
		}
	}
	return nil
}
