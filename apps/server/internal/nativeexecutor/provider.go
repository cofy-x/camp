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
		return openNativeWorkspace(ctx, config)
	}
	if runtime.GOOS != "linux" {
		return nil, fmt.Errorf("Native execution is unsupported on %s", runtime.GOOS)
	}
	return openLayerRuntime(ctx, config)
}

func normalizeConfig(config Config) (Config, error) {
	for _, field := range []*string{&config.DataDir, &config.RuntimeDir} {
		if *field == "" {
			return config, fmt.Errorf("Native execution requires private storage and a pinned DSH runtime")
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
