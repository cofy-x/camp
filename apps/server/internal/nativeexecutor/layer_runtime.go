package nativeexecutor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/execution"
	"github.com/cofy-x/camp/apps/server/internal/layerfs"
	"github.com/cofy-x/camp/apps/server/internal/projectenv"
)

type layerRuntime struct {
	config      Config
	bridge      *layerfs.Bridge
	docker      *layerfs.Docker
	helper      []byte
	mu          sync.Mutex
	unavailable error
	projects    map[string]*preparedUsage
	clock       uint64
	fingerprint string
}

func openLayerRuntime(ctx context.Context, config Config) (*layerRuntime, error) {
	var err error
	for _, value := range []*string{&config.DataDir, &config.RuntimeDir, &config.LayerFSRuntimeDir, &config.SandboxHelper} {
		if *value == "" {
			return nil, errors.New("Native execution requires the pinned DSH and LayerFS runtimes and sandbox helper")
		}
		*value, err = filepath.Abs(*value)
		if err != nil {
			return nil, err
		}
	}
	if config.NodePath == "" {
		config.NodePath = "node"
	}
	if !filepath.IsAbs(config.NodePath) {
		config.NodePath, err = exec.LookPath(config.NodePath)
		if err != nil {
			return nil, err
		}
	}
	config.NodePath, err = filepath.EvalSymlinks(config.NodePath)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(config.DataDir, 0700); err != nil {
		return nil, err
	}
	helper, err := os.ReadFile(config.SandboxHelper)
	if err != nil {
		return nil, err
	}
	if len(helper) == 0 {
		return nil, errors.New("The sandbox helper is empty")
	}
	manifestBytes, err := os.ReadFile(filepath.Join(config.RuntimeDir, "node_modules", "@deepseek-ai", "dsh", "package.json"))
	if err != nil {
		return nil, errors.New("Install the pinned DSH runtime before starting this Runner")
	}
	var manifest struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(manifestBytes, &manifest) != nil || manifest.Version != "0.2.1-rc.3" {
		return nil, errors.New("Native execution requires DSH 0.2.1-rc.3")
	}
	versionCmd := exec.CommandContext(ctx, config.NodePath, "--version")
	version, err := versionCmd.Output()
	if err != nil || strings.TrimSpace(string(version)) != "v"+projectenv.NodeVersion {
		return nil, errors.New("Native execution requires the pinned Node runtime")
	}
	layerManifest, err := os.ReadFile(filepath.Join(config.LayerFSRuntimeDir, "installed.json"))
	if err != nil {
		return nil, errors.New("Install the pinned LayerFS runtime before starting this Runner")
	}
	fingerprintHash := sha256.New()
	_, _ = fingerprintHash.Write(helper)
	_, _ = fingerprintHash.Write(manifestBytes)
	_, _ = fingerprintHash.Write(layerManifest)
	_, _ = fingerprintHash.Write([]byte("camp-coordination-v1"))
	for _, module := range []string{"coordination.mjs", "discovery.mjs", "filesystem.mjs", "shell.mjs", "transport.mjs"} {
		contents, readErr := os.ReadFile(filepath.Join(config.RuntimeDir, "camp-tools", module))
		if readErr != nil {
			return nil, errors.New("Install the complete Camp DSH tool providers before starting this Runner")
		}
		_, _ = fingerprintHash.Write([]byte(module))
		_, _ = fingerprintHash.Write(contents)
	}
	if config.DockerSocket == "" {
		config.DockerSocket = "/var/run/docker.sock"
	}
	docker, err := layerfs.NewDocker(config.DockerSocket)
	if err != nil {
		return nil, err
	}
	bridge, err := layerfs.OpenBridge(ctx, layerfs.BridgeConfig{Node: config.NodePath, RuntimeDir: config.LayerFSRuntimeDir, DataRoot: config.DataDir, Home: filepath.Join(config.DataDir, "layerfs"), Docker: config.DockerSocket})
	if err == nil {
		err = bridge.Reset(ctx)
	}
	if err == nil {
		var probeDir string
		probeDir, err = os.MkdirTemp(config.DataDir, ".probe-")
		if err == nil {
			defer os.RemoveAll(probeDir)
			err = os.Mkdir(filepath.Join(probeDir, "project"), 0700)
		}
		if err == nil {
			err = execution.Available(ctx, execution.Config{RuntimeDir: config.RuntimeDir, NodePath: config.NodePath}, probeDir, config.Lock)
		}
	}
	if err != nil {
		if bridge != nil {
			_ = bridge.Close()
		}
		docker.Close()
		return nil, err
	}
	return &layerRuntime{config: config, bridge: bridge, docker: docker, helper: helper, projects: map[string]*preparedUsage{}, fingerprint: hex.EncodeToString(fingerprintHash.Sum(nil))}, nil
}

func (e *layerRuntime) Fingerprint() string { return e.fingerprint }

func (e *layerRuntime) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var cleanupErr error
	for id, usage := range e.projects {
		if usage.active == 0 {
			cleanupErr = errors.Join(cleanupErr, e.bridge.RemoveProject(ctx, id))
		}
	}
	err := errors.Join(cleanupErr, e.bridge.Close())
	e.docker.Close()
	return err
}

func (e *layerRuntime) Ready() error { e.mu.Lock(); defer e.mu.Unlock(); return e.unavailable }
func (e *layerRuntime) unsafe(err error) {
	if err == nil {
		return
	}
	e.mu.Lock()
	e.unavailable = err
	e.mu.Unlock()
}
