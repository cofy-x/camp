//go:build darwin

package nativeexecutor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/cofy-x/camp/apps/server/internal/layerfs"
	protocol "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/workspace"
)

type hybridWorkspaceProvider struct {
	native                               *nativeWorkspaceProvider
	bridge                               *layerfs.Bridge
	fingerprint, storeID, layerFSVersion string
	mu                                   sync.Mutex
	failure                              error
}

type hybridWorkspace struct {
	*nativeWorkspace
	provider               *hybridWorkspaceProvider
	workspaceID, attemptID string
}

func openHybridWorkspace(ctx context.Context, config Config) (workspace.Provider, error) {
	base, err := openNativeWorkspace(ctx, config)
	if err != nil {
		return nil, err
	}
	native := base.(*nativeWorkspaceProvider)
	workerRoot := config.WorkerDataRoot
	if workerRoot == "" {
		workerRoot = native.config.DataDir
	}
	workerRoot, err = filepath.EvalSymlinks(workerRoot)
	if err != nil || !containsPath(workerRoot, native.config.DataDir) {
		return nil, errors.New("Worker storage must contain this private native execution directory")
	}
	workerRuntime, err := filepath.Abs(config.WorkerRuntimeDir)
	if err != nil || config.WorkerRuntimeDir == "" {
		return nil, errors.New("Install the pinned Camp checkpoint worker runtime")
	}
	dockerBinary, err := SelectedDockerClient(config.DockerBinary)
	if err != nil {
		return nil, errors.New("The LayerFS checkpoint worker requires the selected local Docker engine")
	}
	dockerBinary, err = filepath.Abs(dockerBinary)
	if err != nil {
		return nil, err
	}
	runnerID, storageID := config.RunnerID, config.StorageID
	if runnerID == "" {
		runnerID = "selftest-" + digest(workerRoot)[:24]
	}
	if storageID == "" {
		storageID = "selftest-" + digest(workerRoot)[:24]
	}
	bridge, err := layerfs.OpenBridge(ctx, layerfs.BridgeConfig{Mode: "checkpoint-worker", Node: config.NodePath, RuntimeDir: workerRuntime, DataRoot: workerRoot, Home: filepath.Join(workerRoot, "layerfs-worker"), Docker: config.DockerSocket, DockerBinary: dockerBinary, Installation: filepath.Join(workerRuntime, "installed.json"), RunnerID: runnerID, StorageID: storageID})
	if err != nil {
		return nil, err
	}
	status, err := bridge.Status(ctx)
	var observed struct {
		Ready        bool   `json:"ready"`
		StoreID      string `json:"storeId"`
		SourceCommit string `json:"sourceCommit"`
		Image        string `json:"image"`
		Protocol     int    `json:"protocol"`
		SDKVersion   string `json:"sdkVersion"`
	}
	if err == nil {
		err = json.Unmarshal(status, &observed)
	}
	if err != nil || !observed.Ready || observed.StoreID == "" || observed.Protocol != 1 {
		_ = bridge.Close()
		return nil, errors.Join(err, errors.New("The worker is not ready; retained receipts require explicit recovery"))
	}
	hash := sha256.New()
	_, _ = fmt.Fprintf(hash, "%s\x00camp-mac-worker-v1\x00%s\x00%s\x00%s", native.Fingerprint(), observed.StoreID, observed.Image, observed.SourceCommit)
	for _, name := range []string{"bridge.mjs", "checkpoint.mjs", "client.mjs", "contracts.mjs", "home.mjs", "protocol.mjs", "state.mjs", "transfer.mjs", "tree.mjs"} {
		data, readErr := os.ReadFile(filepath.Join(workerRuntime, name))
		if readErr != nil {
			_ = bridge.Close()
			return nil, readErr
		}
		_, _ = hash.Write(data)
	}
	return &hybridWorkspaceProvider{native: native, bridge: bridge, storeID: observed.StoreID, layerFSVersion: observed.SDKVersion, fingerprint: hex.EncodeToString(hash.Sum(nil))}, nil
}

func (p *hybridWorkspaceProvider) Fingerprint() string { return p.fingerprint }

func (p *hybridWorkspaceProvider) Facts() workspace.Facts {
	return workspace.Facts{Workspace: "native-layerfs-worker", Sandbox: "seatbelt", Checkpoint: "layerfs-disposable+native-capture", LayerFSVersion: p.layerFSVersion}
}

func (p *hybridWorkspaceProvider) Ready() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	return errors.Join(p.failure, p.native.Ready(), p.bridge.Ready())
}

func (p *hybridWorkspaceProvider) Close() error {
	return errors.Join(p.bridge.Close(), p.native.Close())
}

func (p *hybridWorkspaceProvider) Prepare(ctx context.Context, spec workspace.Spec) (workspace.Instance, error) {
	if err := p.Ready(); err != nil {
		return nil, err
	}
	if spec.NamespaceKey != "" {
		if err := p.bridge.CheckpointWorkspace(ctx, spec.NamespaceKey); err != nil {
			p.mu.Lock()
			p.failure = err
			p.mu.Unlock()
			return nil, err
		}
	}
	instance, err := p.native.Prepare(ctx, spec)
	if instance == nil {
		return nil, err
	}
	native := instance.(*nativeWorkspace)
	id := spec.NamespaceKey
	if id == "" {
		id = digest(native.root)
	}
	attempt := spec.AttemptID
	if attempt == "" {
		attempt = "native-selftest"
	}
	return &hybridWorkspace{nativeWorkspace: native, provider: p, workspaceID: id, attemptID: attempt}, err
}

func (p *hybridWorkspaceProvider) SelfTest(ctx context.Context) protocol.ReadinessReport {
	report := p.native.SelfTest(ctx)
	if !report.Ready {
		return report
	}
	_, err := p.bridge.CheckpointSelfTest(ctx)
	check := protocol.ReadinessCheck{ID: "worker-checkpoint", Label: "Full working-tree checkpoint and restore", Status: "passed"}
	if err != nil {
		check.Status = "failed"
		check.Detail = err.Error()
		report.Ready = false
		p.mu.Lock()
		p.failure = err
		p.mu.Unlock()
	}
	report.Checks = append(report.Checks, check)
	return report
}

func (w *hybridWorkspace) Capture(ctx context.Context, target, message string, excluded []string) (workspace.Archive, error) {
	if err := w.nativeWorkspace.seal(ctx); err != nil {
		return workspace.Archive{}, err
	}
	w.nativeWorkspace.mu.Lock()
	w.nativeWorkspace.retain = true
	w.nativeWorkspace.mu.Unlock()
	checkpoint, err := w.provider.bridge.CaptureTree(ctx, w.directory, w.workspaceID, w.attemptID)
	if err == nil && checkpoint.StoreID != w.provider.storeID {
		err = errors.New("The checkpoint Store incarnation changed during this native attempt")
	}
	if err != nil {
		w.provider.mu.Lock()
		w.provider.failure = err
		w.provider.mu.Unlock()
		return workspace.Archive{}, err
	}
	view, err := w.provider.bridge.PublicationView(ctx, checkpoint, excluded)
	if err != nil {
		return workspace.Archive{}, err
	}
	if !containsPath(filepath.Join(checkpoint.CaptureRoot, "..", "..", "publication"), view.Directory) {
		return workspace.Archive{}, errors.New("The publication view escaped its owned staging directory")
	}
	defer os.RemoveAll(view.Directory)
	archive, err := w.nativeWorkspace.captureDirectory(ctx, view.Directory, target, excluded)
	if err == nil {
		w.nativeWorkspace.mu.Lock()
		w.nativeWorkspace.retain = false
		w.nativeWorkspace.mu.Unlock()
	}
	return archive, err
}
