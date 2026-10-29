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

	"github.com/cofy-x/camp/apps/server/internal/dsh"
	"github.com/cofy-x/camp/apps/server/internal/execution"
	"github.com/cofy-x/camp/apps/server/internal/layerfs"
	"github.com/cofy-x/camp/apps/server/internal/projectenv"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

type Executor struct {
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

func Open(ctx context.Context, config Config) (*Executor, error) {
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
	return &Executor{config: config, bridge: bridge, docker: docker, helper: helper, projects: map[string]*preparedUsage{}, fingerprint: hex.EncodeToString(fingerprintHash.Sum(nil))}, nil
}

func (e *Executor) Fingerprint() string { return e.fingerprint }

func (e *Executor) Close() error {
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

func (e *Executor) Ready() error { e.mu.Lock(); defer e.mu.Unlock(); return e.unavailable }
func (e *Executor) unsafe(err error) {
	if err == nil {
		return
	}
	e.mu.Lock()
	e.unavailable = err
	e.mu.Unlock()
}

// Execute receives a private prepared snapshot. It never reads Camp's database,
// Git authority, server model configuration, or a host source directory.
func (e *Executor) Execute(ctx context.Context, input Request, emit Emit) (result Result) {
	result.Status = "failed"
	result.Checks = []projectenv.CheckResult{}
	result.CleanupConfirmed = true
	if emit == nil {
		emit = func(string, string) error { return nil }
	}
	record := func(kind, text string) error {
		text = strings.ReplaceAll(text, input.ModelToken, "[redacted]")
		if kind == "assistant" {
			result.Assistant = bounded(result.Assistant+text, 32<<10)
		}
		return emit(kind, text)
	}
	if err := e.Ready(); err != nil {
		result.Error = err.Error()
		return
	}
	var err error
	var prepared layerfs.PreparedProject
	var workspace layerfs.Workspace
	var sandbox *layerfs.Sandbox
	var tools *layerfs.Tools
	var coordinator *coordination
	var environment *projectenv.Environment
	var profile projectenv.Profile
	if input.Timeout <= 0 {
		input.Timeout = 180 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, input.Timeout)
	defer cancel()
	dir := input.Directory
	if input.ModelToken == "" || input.ModelURL == "" {
		result.Error = "An attempt-scoped model capability is required"
		return
	}
	if !filepath.IsAbs(dir) || !filepath.IsLocal(input.AttemptID) || input.SessionID == "" {
		result.Error = "Invalid private execution paths or session identity"
		return
	}
	if len(input.Environment) > 0 && string(input.Environment) != "null" && string(input.Environment) != "{}" {
		err = json.Unmarshal(input.Environment, &profile)
		if err == nil {
			err = projectenv.Validate(profile)
		}
		if err == nil {
			options := projectenv.Options{Profile: &profile, NodePath: e.config.NodePath, ProjectDir: filepath.Join(dir, "project"), CacheDir: filepath.Join(e.config.DataDir, "environments", digest(input.ResourceID)), Lock: e.config.Lock, Output: func(text string) error { return record("environment", text) }}
			if profile.Kind == "node" && profile.PackageManager != "node" {
				options.PnpmPath, err = projectenv.FindPnpm(e.config.RuntimeDir)
			}
			if err == nil {
				environment, err = projectenv.Prepare(ctx, options)
			}
		}
	} else {
		err = record("environment", "[Camp] Checks: Not configured.\n")
	}
	if err == nil && environment != nil {
		for _, mount := range environment.Mounts {
			if !filepath.IsLocal(mount.Target) {
				err = errors.New("Invalid prepared dependency target")
				break
			}
			if err = copyDependencies(ctx, mount.Source, filepath.Join(dir, "project", mount.Target), filepath.Join(dir, "project")); err != nil {
				break
			}
		}
	}
	if err == nil {
		err = record("environment", "[Camp] Creating a private LayerFS workspace. Agent commands have no network access.\n")
	}
	if err == nil {
		key := ""
		if environment != nil {
			key = environment.Key
		}
		allocationCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
		prepared, err = e.acquirePreparedProject(allocationCtx, layerfs.PrepareRequest{SourceDir: filepath.Join(dir, "project"), BaselineKey: input.ResourceID + ":" + input.BaselineCommit, EnvironmentKey: key})
		if err == nil && ctx.Err() == nil {
			workspace, err = e.bridge.Create(allocationCtx, layerfs.CreateRequest{ProjectID: prepared.ProjectID, RunID: input.AttemptID})
			if err != nil {
				var known *layerfs.BridgeError
				var partial struct {
					Workspace layerfs.Workspace `json:"workspace"`
				}
				if errors.As(err, &known) && json.Unmarshal(known.Details, &partial) == nil && partial.Workspace.ProjectID == prepared.ProjectID && partial.Workspace.RunID == input.AttemptID && partial.Workspace.WorkspaceID != "" {
					workspace = partial.Workspace
				} else {
					result.CleanupConfirmed = false
					e.unsafe(err)
				}
			}
		} else if err == nil {
			err = ctx.Err()
		}
		stop()
	}
	if err == nil {
		sandbox, tools, err = e.installSandbox(ctx, workspace, input.ReferencesDir, input.Access == "read")
	}
	if err == nil && input.Task != nil {
		persist := input.PersistIntent
		if persist != nil {
			persist = func(ctx context.Context, intent p.CoordinationIntent) error {
				err := input.PersistIntent(ctx, intent)
				if err != nil {
					cancel()
				}
				return err
			}
		}
		coordinator, err = newCoordination(ctx, *input.Task, persist, tools.Seal)
	}
	nativeCompleted := false
	if err == nil {
		stateDir := filepath.Join(e.config.DataDir, "sessions", digest(input.SessionKey))
		err = os.MkdirAll(stateDir, 0700)
		var overlay []byte
		if err == nil {
			overlay, err = dsh.RemoteOverlay(input.Persona, input.Access == "read", input.Task != nil)
		}
		if err == nil {
			err = os.WriteFile(filepath.Join(dir, "runtime.patch.yml"), overlay, 0600)
		}
		if err == nil {
			config := execution.Config{RuntimeDir: e.config.RuntimeDir, NodePath: e.config.NodePath, APIKey: input.ModelToken, BaseURL: input.ModelURL, ReadOnlyProject: input.Access == "read", StateDir: stateDir, RemoteTools: true, ToolEndpoint: tools.Endpoint(), ToolToken: tools.Token(), CAFile: e.config.CAFile}
			if coordinator != nil {
				config.CoordinationEndpoint, config.CoordinationToken = coordinator.endpoint(), coordinator.token
			}
			cmd, commandErr := execution.Command(config, dir, e.config.Lock, false)
			err = commandErr
			if err == nil {
				stopWatch := make(chan struct{})
				go func() {
					select {
					case <-tools.Failed():
						cancel()
					case <-stopWatch:
					}
				}()
				code, runErr := dsh.Run(ctx, cmd, dsh.Config{Model: input.Model, MaxTokens: input.MaxTokens, MaxSteps: input.MaxSteps, Resume: input.Resume}, input.SessionID, input.Prompt, func(event dsh.Event) error { return record(event.Type, event.Text) })
				close(stopWatch)
				result.ExitCode = &code
				err = runErr
				nativeCompleted = err == nil
			}
		}
	}
	if coordinator != nil {
		err = errors.Join(err, coordinator.close())
		result.Intent = coordinator.result()
		if err == nil && result.Intent == nil {
			err = errors.New("Collaboration ended without a coordination decision")
		}
	}
	toolsSettled := true
	if tools != nil {
		closeErr := tools.Close()
		toolsSettled = closeErr == nil
		err = errors.Join(err, closeErr)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil && input.Access == "write" && environment != nil && len(profile.Checks) > 0 && (input.Task == nil || result.Intent != nil && result.Intent.Kind == "finish") {
		err = e.check(ctx, sandbox, profile, &result, record)
	}
	// Export after tool shutdown even on cancellation, preserving only a confirmed
	// snapshot. The daemon decides whether it is still authorized to upload it.
	if workspace.WorkspaceID != "" && toolsSettled && sandbox != nil && sandbox.Failure() == nil && input.Access == "write" {
		exportCtx, stop := context.WithTimeout(context.Background(), 45*time.Second)
		_, exportErr := e.bridge.Checkpoint(exportCtx, workspace.WorkspaceID, "Camp attempt "+input.AttemptID)
		if exportErr == nil {
			var archive layerfs.ExportedArchive
			archive, exportErr = e.bridge.ExportWithPolicy(exportCtx, workspace.WorkspaceID, filepath.Join(dir, "result.tar"), projectenv.SnapshotPolicy(profile).ExcludeNames)
			if exportErr == nil && archive.ArchivePath == filepath.Join(dir, "result.tar") && archive.Bytes >= 0 && len(archive.SHA256) == 64 {
				exportErr = persistExport(archive)
				if exportErr == nil {
					result.ArchivePath = archive.ArchivePath
					result.ArchiveSHA256 = archive.SHA256
					result.ArchiveBytes = archive.Bytes
				}
			} else if exportErr == nil {
				exportErr = errors.New("LayerFS returned an invalid export receipt")
			}
		}
		stop()
		err = errors.Join(err, exportErr)
	}
	cleanupCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	var cleanupErr error
	if workspace.WorkspaceID != "" {
		cleanupErr = e.bridge.CloseWorkspace(cleanupCtx, workspace.WorkspaceID, true)
	}
	if cleanupErr != nil && sandbox != nil {
		killCtx, kill := context.WithTimeout(context.Background(), 12*time.Second)
		cleanupErr = errors.Join(cleanupErr, e.docker.Stop(killCtx, sandbox.ContainerID, 2*time.Second))
		kill()
	}
	if cleanupErr == nil && prepared.ProjectID != "" && result.CleanupConfirmed {
		cleanupErr = e.releasePreparedProject(cleanupCtx, prepared.ProjectID)
	}
	stop()
	if cleanupErr != nil {
		result.CleanupConfirmed = false
		e.unsafe(cleanupErr)
	}
	err = errors.Join(err, cleanupErr)
	if err == nil {
		result.Status = "succeeded"
	} else {
		result.Error = bounded(strings.ReplaceAll(err.Error(), input.ModelToken, "[redacted]"), 4096)
	}
	if ctx.Err() != nil {
		result.Status = "interrupted"
		result.Error = "Execution stopped before completion. No model request will be replayed."
	}
	if !result.CleanupConfirmed {
		result.Status = "interrupted"
		result.Error = "Sandbox cleanup could not be confirmed. This Runner is suspended until restart and reconciliation."
	}
	result.SessionResumable = nativeCompleted && err == nil && result.CleanupConfirmed
	return
}

func digest(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func bounded(value string, limit int) string {
	value = strings.ToValidUTF8(value, "�")
	if len(value) > limit {
		return strings.ToValidUTF8(value[:limit-40], "�") + "\n[Output truncated]"
	}
	return value
}

func (e *Executor) check(ctx context.Context, sandbox *layerfs.Sandbox, profile projectenv.Profile, result *Result, record Emit) error {
	return e.verifyChecks(ctx, sandbox, profile, result, record)
}
