package nativeexecutor

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/layerfs"
	"github.com/cofy-x/camp/apps/server/internal/sandboxexec"
	"github.com/cofy-x/camp/apps/server/internal/workspace"
)

type layerWorkspace struct {
	provider *layerRuntime
	prepared layerfs.PreparedProject
	mounted  layerfs.Workspace
	sandbox  *layerfs.Sandbox
}

func (*layerRuntime) Facts() workspace.Facts {
	return workspace.Facts{Workspace: "layerfs", Sandbox: "linux-container", Checkpoint: "layerfs", LayerFSVersion: "1.1.0"}
}

func (m *layerRuntime) Prepare(ctx context.Context, spec workspace.Spec) (workspace.Instance, error) {
	w := &layerWorkspace{provider: m}
	var err error
	w.prepared, err = m.acquirePreparedProject(ctx, layerfs.PrepareRequest{SourceDir: spec.Source, BaselineKey: spec.BaselineKey, EnvironmentKey: spec.EnvironmentKey})
	if err != nil {
		return w, err
	}
	w.mounted, err = m.bridge.Create(ctx, layerfs.CreateRequest{ProjectID: w.prepared.ProjectID, RunID: spec.AttemptID})
	if err != nil {
		var known *layerfs.BridgeError
		var partial struct {
			Workspace layerfs.Workspace `json:"workspace"`
		}
		if errors.As(err, &known) && json.Unmarshal(known.Details, &partial) == nil && partial.Workspace.ProjectID == w.prepared.ProjectID && partial.Workspace.RunID == spec.AttemptID && partial.Workspace.WorkspaceID != "" {
			w.mounted = partial.Workspace
		} else {
			m.unsafe(err)
		}
		return w, err
	}
	w.sandbox, err = m.installSandbox(ctx, w.mounted, spec.References)
	return w, err
}

func (w *layerWorkspace) Directory() string { return "/workspace" }
func (w *layerWorkspace) Failure() error {
	if w.sandbox == nil {
		return errors.New("The execution workspace is not available")
	}
	return w.sandbox.Failure()
}
func (w *layerWorkspace) Call(ctx context.Context, request sandboxexec.Request, emit func(sandboxexec.Frame) error) (sandboxexec.Frame, error) {
	if w.sandbox == nil {
		return sandboxexec.Frame{}, errors.New("The execution workspace is not available")
	}
	return w.sandbox.Call(ctx, request, emit)
}
func (w *layerWorkspace) Capture(ctx context.Context, target, message string, excluded []string) (workspace.Archive, error) {
	if _, err := w.provider.bridge.Checkpoint(ctx, w.mounted.WorkspaceID, message); err != nil {
		return workspace.Archive{}, err
	}
	return w.provider.bridge.ExportWithPolicy(ctx, w.mounted.WorkspaceID, target, excluded)
}
func (w *layerWorkspace) Close(ctx context.Context) error {
	var err error
	if w.mounted.WorkspaceID != "" {
		err = w.provider.bridge.CloseWorkspace(ctx, w.mounted.WorkspaceID, true)
	}
	if err != nil && w.sandbox != nil {
		killCtx, stop := context.WithTimeout(context.Background(), 12*time.Second)
		err = errors.Join(err, w.provider.docker.Stop(killCtx, w.sandbox.ContainerID, 2*time.Second))
		stop()
	}
	if err == nil && w.prepared.ProjectID != "" && w.provider.Ready() == nil {
		err = w.provider.releasePreparedProject(ctx, w.prepared.ProjectID)
	}
	if err != nil {
		w.provider.unsafe(err)
	}
	return err
}
