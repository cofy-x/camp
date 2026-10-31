package nativeexecutor

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/cofy-x/camp/apps/server/internal/workspace"
)

func openWorkspaceProvider(ctx context.Context, config Config) (workspace.Provider, error) {
	if runtime.GOOS == "darwin" {
		if config.WorkspaceBackend == "layerfs-worker" {
			return openHybridWorkspace(ctx, config)
		}
		if config.WorkspaceBackend != "" && config.WorkspaceBackend != "native-local" {
			return nil, fmt.Errorf("Unknown Mac workspace backend %q", config.WorkspaceBackend)
		}
		return openNativeWorkspace(ctx, config)
	}
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("Native execution is unsupported on %s", runtime.GOOS)
	}
	if config.WorkspaceBackend != "" && config.WorkspaceBackend != "layerfs" {
		return nil, fmt.Errorf("The selected workspace backend is unsupported on Linux")
	}
	return openLayerRuntime(ctx, config)
}

func normalizeConfig(config Config) (Config, error) {
	if err := ValidateWorkspaceBackend(config.WorkspaceBackend); err != nil {
		return config, err
	}
	for _, field := range []*string{&config.DataDir, &config.RuntimeDir, &config.SandboxHelper} {
		if *field == "" {
			return config, fmt.Errorf("Native execution requires private storage, a pinned DSH runtime and the trusted helper")
		}
		var err error
		*field, err = filepath.Abs(*field)
		if err != nil {
			return config, err
		}
	}
	if config.NodePath == "" {
		config.NodePath = "node"
	}
	if !filepath.IsAbs(config.NodePath) {
		var err error
		config.NodePath, err = exec.LookPath(config.NodePath)
		if err != nil {
			return config, err
		}
	}
	var err error
	config.NodePath, err = filepath.EvalSymlinks(config.NodePath)
	if err != nil {
		return config, err
	}
	return config, nil
}

func ValidateWorkspaceBackend(value string) error {
	if value == "" {
		return nil
	}
	if runtime.GOOS == "darwin" && (value == "native-local" || value == "layerfs-worker") {
		return nil
	}
	if runtime.GOOS == "linux" && value == "layerfs" {
		return nil
	}
	return fmt.Errorf("Unsupported workspace backend %q on %s", value, runtime.GOOS)
}
