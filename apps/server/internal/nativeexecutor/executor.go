package nativeexecutor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/dsh"
	"github.com/cofy-x/camp/apps/server/internal/execution"
	"github.com/cofy-x/camp/apps/server/internal/projectenv"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/runnertools"
	"github.com/cofy-x/camp/apps/server/internal/workspace"
)

type Executor struct {
	config      Config
	backend     workspace.Provider
	mu          sync.Mutex
	unavailable error
}

func Open(ctx context.Context, config Config) (*Executor, error) {
	var err error
	config, err = normalizeConfig(config)
	if err != nil {
		return nil, err
	}
	backend, err := openWorkspaceProvider(ctx, config)
	if err != nil {
		return nil, err
	}
	return &Executor{config: config, backend: backend}, nil
}
func (e *Executor) Fingerprint() string                            { return e.backend.Fingerprint() }
func (e *Executor) Close() error                                   { return e.backend.Close() }
func (e *Executor) SelfTest(ctx context.Context) p.ReadinessReport { return e.backend.SelfTest(ctx) }
func (e *Executor) Ready() error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return errors.Join(e.unavailable, e.backend.Ready())
}
func (e *Executor) unsafe(err error) { e.mu.Lock(); defer e.mu.Unlock(); e.unavailable = err }

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
	var active workspace.Instance
	var tools *runnertools.Tools
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
		err = record("environment", "[Camp] Creating a private execution workspace. Agent commands have no network access.\n")
	}
	if err == nil {
		key := ""
		if environment != nil {
			key = environment.Key
		}
		allocationCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
		active, err = e.backend.Prepare(allocationCtx, workspace.Spec{Source: filepath.Join(dir, "project"), BaselineKey: input.ResourceID + ":" + input.BaselineCommit, EnvironmentKey: key, AttemptID: input.AttemptID, References: input.ReferencesDir, ReadOnly: input.Access == "read"})
		stop()
		if err != nil && e.backend.Ready() != nil {
			result.CleanupConfirmed = false
			e.unsafe(err)
		}
		if err == nil {
			err = ctx.Err()
		}
	}
	if err == nil {
		tools, err = runnertools.NewTools(ctx, active, input.Access == "read")
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
	dshSettled := true
	if err == nil {
		stateDir := filepath.Join(e.config.DataDir, "sessions", digest(input.SessionKey))
		err = os.MkdirAll(stateDir, 0700)
		var overlay []byte
		if err == nil {
			persona := input.Persona
			if active.Directory() != "/workspace" {
				persona += "\nThe current native workspace is " + active.Directory() + ". Use this turn's directory for file and command operations, including after session continuation."
				if roots, ok := active.(interface{ PathRoots() (string, string) }); ok {
					_, references := roots.PathRoots()
					if references != "" {
						persona += " Read-only reference evidence is at " + references + "."
					}
				}
			}
			overlay, err = dsh.RemoteOverlayAt(persona, input.Access == "read", executionPaths(active.Directory(), stateDir, e.config.RuntimeDir), input.Task != nil)
		}
		if err == nil {
			err = os.WriteFile(filepath.Join(dir, "runtime.patch.yml"), overlay, 0600)
		}
		if err == nil {
			config := execution.Config{RuntimeDir: e.config.RuntimeDir, NodePath: e.config.NodePath, APIKey: input.ModelToken, BaseURL: input.ModelURL, ReadOnlyProject: input.Access == "read", StateDir: stateDir, RemoteTools: true, ToolEndpoint: tools.Endpoint(), ToolToken: tools.Token(), CAFile: e.config.CAFile}
			if coordinator != nil {
				config.CoordinationEndpoint, config.CoordinationToken = coordinator.endpoint(), coordinator.token
			}
			config.Cwd, config.TimeoutMS = active.Directory(), input.Timeout.Milliseconds()
			cmd, finishCommand, commandErr := e.dshCommand(ctx, config, dir)
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
				code, runErr := dsh.Run(ctx, cmd, dsh.Config{Model: input.Model, MaxTokens: input.MaxTokens, MaxSteps: input.MaxSteps, Resume: input.Resume, Cwd: active.Directory()}, input.SessionID, input.Prompt, func(event dsh.Event) error { return record(event.Type, event.Text) })
				close(stopWatch)
				cleanupErr := finishCommand()
				if cleanupErr != nil {
					dshSettled = false
					result.CleanupConfirmed = false
					e.unsafe(cleanupErr)
				}
				result.ExitCode = &code
				err = errors.Join(runErr, cleanupErr)
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
	toolsSettled := dshSettled
	if tools != nil {
		closeErr := tools.Close()
		toolsSettled = toolsSettled && closeErr == nil
		err = errors.Join(err, closeErr)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil && input.Access == "write" && environment != nil && len(profile.Checks) > 0 && (input.Task == nil || result.Intent != nil && result.Intent.Kind == "finish") {
		err = e.verifyChecks(ctx, active, profile, &result, record)
	}
	// Export after tool shutdown even on cancellation, preserving only a confirmed
	// snapshot. The daemon decides whether it is still authorized to upload it.
	if active != nil && toolsSettled && active.Failure() == nil && input.Access == "write" {
		exportCtx, stop := context.WithTimeout(context.Background(), 45*time.Second)
		archive, exportErr := active.Capture(exportCtx, filepath.Join(dir, "result.tar"), "Camp attempt "+input.AttemptID, projectenv.SnapshotPolicy(profile).ExcludeNames)
		if exportErr == nil && archive.ArchivePath == filepath.Join(dir, "result.tar") && archive.Bytes >= 0 && len(archive.SHA256) == 64 {
			exportErr = persistExport(archive)
			if exportErr == nil {
				result.ArchivePath = archive.ArchivePath
				result.ArchiveSHA256 = archive.SHA256
				result.ArchiveBytes = archive.Bytes
			}
		} else if exportErr == nil {
			exportErr = errors.New("Workspace returned an invalid capture receipt")
		}
		stop()
		err = errors.Join(err, exportErr)
	}
	cleanupCtx, stop := context.WithTimeout(context.Background(), 30*time.Second)
	var cleanupErr error
	if active != nil {
		cleanupErr = active.Close(cleanupCtx)
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
