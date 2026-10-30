package runs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/layerfs"
	"github.com/cofy-x/camp/apps/server/internal/projectenv"
	"github.com/cofy-x/camp/apps/server/internal/runnertools"
	"github.com/cofy-x/camp/apps/server/internal/sandboxexec"
)

func (m *Manager) openLayerFS(ctx context.Context) string {
	if m.config.LayerFSRuntimeDir == "" || m.config.SandboxHelper == "" {
		return "Install the pinned LayerFS runtime and build camp-sandbox before enabling agent execution."
	}
	helper, err := os.ReadFile(m.config.SandboxHelper)
	if err != nil || len(helper) == 0 {
		return "The trusted sandbox helper is missing. Run make build or set CAMP_SANDBOX_HELPER."
	}
	node := m.config.NodePath
	if !filepath.IsAbs(node) {
		node, err = exec.LookPath(node)
	}
	if err != nil || node == "" {
		return "The configured Node executable could not be found."
	}
	node, err = filepath.EvalSymlinks(node)
	if err != nil {
		return "The configured Node executable could not be found."
	}
	socket := m.config.DockerSocket
	if socket == "" {
		socket = "/var/run/docker.sock"
	}
	docker, err := layerfs.NewDocker(socket)
	if err != nil {
		return "CAMP_DOCKER_SOCKET must identify a local Docker socket."
	}
	dataRoot, err := filepath.Abs(filepath.Dir(m.dir))
	if err != nil {
		docker.Close()
		return "The LayerFS data directory is invalid."
	}
	initCtx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	bridge, err := layerfs.OpenBridge(initCtx, layerfs.BridgeConfig{Node: node, RuntimeDir: m.config.LayerFSRuntimeDir, DataRoot: dataRoot, Home: filepath.Join(dataRoot, "layerfs"), Docker: socket})
	if err == nil {
		err = bridge.Reset(initCtx)
	}
	if err != nil {
		if bridge != nil {
			_ = bridge.Close()
		}
		docker.Close()
		return "LayerFS execution is unavailable: " + m.redact(err.Error()) + ". Install the pinned runtime and verify the local Docker service."
	}
	m.bridge, m.docker, m.helper = bridge, docker, helper
	m.layerMu.Lock()
	m.layerProjects = map[string]*preparedUsage{}
	m.layerMu.Unlock()
	return ""
}

func (m *Manager) prepareLayerFS(ctx context.Context, e *execution, dir, references string) error {
	m.mu.Lock()
	input := e.run
	m.mu.Unlock()
	if m.bridge == nil || m.docker == nil {
		return errors.New("LayerFS execution has not been initialized")
	}
	environmentKey := ""
	if e.environment != nil {
		environmentKey = e.environment.Key
		for _, mount := range e.environment.Mounts {
			if !filepath.IsLocal(mount.Target) {
				return errors.New("Invalid prepared dependency target")
			}
			if err := copyDependencies(ctx, mount.Source, filepath.Join(dir, "project", mount.Target), filepath.Join(dir, "project")); err != nil {
				return err
			}
		}
	}
	if err := m.record(e, "environment", "[Camp] Creating a private LayerFS workspace. Agent commands have no network access.\n"); err != nil {
		return err
	}
	// Cancellation must still receive the allocation receipt before cleanup.
	allocationCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 60*time.Second)
	defer cancel()
	prepared, err := m.acquirePreparedProject(allocationCtx, layerfs.PrepareRequest{SourceDir: filepath.Join(dir, "project"), BaselineKey: input.ResourceID + ":" + input.BaselineCommit, EnvironmentKey: environmentKey})
	if err != nil {
		return m.layerFSOutcome(err)
	}
	e.layerProject = prepared.ProjectID
	if err := ctx.Err(); err != nil {
		return err
	}
	e.workspace, err = m.bridge.Create(allocationCtx, layerfs.CreateRequest{ProjectID: prepared.ProjectID, RunID: input.ID})
	if err != nil {
		// A known SDK error may still carry a partially allocated Sandbox.
		// Retain that receipt so the normal close path can reclaim it.
		var known *layerfs.BridgeError
		var partial struct {
			Workspace layerfs.Workspace `json:"workspace"`
		}
		if errors.As(err, &known) && json.Unmarshal(known.Details, &partial) == nil && partial.Workspace.ProjectID == prepared.ProjectID && partial.Workspace.RunID == input.ID && partial.Workspace.WorkspaceID != "" {
			e.workspace = partial.Workspace
		} else {
			e.cleanupErr = err
			m.mu.Lock()
			m.unavailable = "A LayerFS allocation failed without a cleanup receipt. Recheck execution readiness before starting more work."
			m.mu.Unlock()
		}
		return m.layerFSOutcome(err)
	}
	id, err := m.docker.Resolve(ctx, e.workspace.ContainerName)
	if err != nil {
		return err
	}
	e.sandbox = &layerfs.Sandbox{Docker: m.docker, ContainerID: id, User: strconv.Itoa(e.workspace.UID) + ":" + strconv.Itoa(e.workspace.GID), Roots: sandboxexec.Roots{Workspace: e.workspace.MountPath, References: "/references"}}
	if err := e.sandbox.Setup(ctx, m.helper); err != nil {
		return err
	}
	if references != "" {
		if err := m.installReferences(ctx, e.sandbox, references); err != nil {
			return err
		}
	}
	e.tools, err = runnertools.NewTools(ctx, e.sandbox, input.AgentSnapshot.Access == "read")
	return err
}

func (m *Manager) layerFSOutcome(err error) error {
	var uncertain *layerfs.OutcomeUnknownError
	if errors.As(err, &uncertain) {
		m.mu.Lock()
		m.unavailable = "A LayerFS operation ended without a confirmed outcome. Restart Camp to reconcile owned sandboxes before starting more work."
		m.mu.Unlock()
	}
	return err
}

func (m *Manager) installReferences(ctx context.Context, sandbox *layerfs.Sandbox, directory string) error {
	return filepath.WalkDir(directory, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(directory, name)
		if err != nil {
			return err
		}
		target := path.Join("/references", filepath.ToSlash(relative))
		if entry.IsDir() {
			code, err := m.docker.Exec(ctx, sandbox.ContainerID, "0:0", []string{"/bin/mkdir", "-p", "-m", "0555", "--", target}, io.Discard, io.Discard)
			if err != nil || code != 0 {
				return errors.Join(err, fmt.Errorf("Cannot install reference directory (exit %d)", code))
			}
			return nil
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || info.Size() > 8<<20 {
			return errors.New("Reference evidence must be a bounded regular file")
		}
		data, err := os.ReadFile(name)
		if err != nil {
			return err
		}
		return m.docker.InstallFile(ctx, sandbox.ContainerID, target, data, 0444)
	})
}

// Dependency copies are trusted installer output, not model-created files.
// Preserve only relative links that still resolve within the owned project.
func copyDependencies(ctx context.Context, source, target, project string) error {
	var count int
	var total int64
	return filepath.WalkDir(source, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		count++
		if count > 200000 {
			return errors.New("Prepared dependencies exceed the file limit")
		}
		relative, err := filepath.Rel(source, name)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0755)
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, err := os.Readlink(name)
			if err != nil {
				return err
			}
			resolved, err := filepath.Rel(project, filepath.Clean(filepath.Join(filepath.Dir(destination), link)))
			if err != nil || filepath.IsAbs(link) || !filepath.IsLocal(resolved) {
				return errors.New("Prepared dependency link escapes the project")
			}
			return os.Symlink(link, destination)
		}
		if !info.Mode().IsRegular() || info.Size() > 256<<20 {
			return errors.New("Prepared dependencies contain an unsupported file")
		}
		total += info.Size()
		if total > 2<<30 {
			return errors.New("Prepared dependencies exceed 2 GiB")
		}
		input, err := os.Open(name)
		if err != nil {
			return err
		}
		defer input.Close()
		output, err := os.OpenFile(destination, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0644|info.Mode().Perm()&0111)
		if err != nil {
			return err
		}
		_, err = io.Copy(output, input)
		return errors.Join(err, output.Close())
	})
}

// An export receipt is written only after tool shutdown, checkpoint, and export
// all acknowledge completion. Recovery never reads partial or unconfirmed tar.
func (m *Manager) exportLayerFS(e *execution, dir string) error {
	m.mu.Lock()
	input := e.run
	m.mu.Unlock()
	if e.sandbox == nil || e.sandbox.Failure() != nil {
		return errors.New("The sandbox did not remain available for a confirmed result export")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if _, err := m.bridge.Checkpoint(ctx, e.workspace.WorkspaceID, "Camp run "+input.ID); err != nil {
		return m.layerFSOutcome(err)
	}
	var profile projectenv.Profile
	_ = json.Unmarshal(input.Environment, &profile)
	archive, err := m.bridge.ExportWithPolicy(ctx, e.workspace.WorkspaceID, filepath.Join(dir, "result.tar"), projectenv.SnapshotPolicy(profile).ExcludeNames)
	if err != nil {
		return m.layerFSOutcome(err)
	}
	if archive.ArchivePath != filepath.Join(dir, "result.tar") || archive.Bytes < 0 || len(archive.SHA256) != 64 {
		return errors.New("LayerFS returned an invalid export receipt")
	}
	encoded, err := json.Marshal(archive)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(filepath.Join(dir, "result.json.pending"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	_, writeErr := file.Write(encoded)
	err = errors.Join(writeErr, file.Sync(), file.Close())
	if err == nil {
		err = os.Rename(filepath.Join(dir, "result.json.pending"), filepath.Join(dir, "result.json"))
	}
	return err
}

func confirmedArchive(dir string) (*os.File, error) {
	encoded, err := os.ReadFile(filepath.Join(dir, "result.json"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var receipt layerfs.ExportedArchive
	if json.Unmarshal(encoded, &receipt) != nil || receipt.ArchivePath != filepath.Join(dir, "result.tar") || receipt.Bytes < 0 || len(receipt.SHA256) != 64 {
		return nil, errors.New("The saved result export receipt is invalid")
	}
	file, err := os.Open(receipt.ArchivePath)
	if err != nil {
		return nil, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != receipt.Bytes {
		_ = file.Close()
		return nil, errors.New("The saved result archive is incomplete")
	}
	digest := sha256.New()
	if _, err := io.Copy(digest, file); err != nil {
		_ = file.Close()
		return nil, err
	}
	if hex.EncodeToString(digest.Sum(nil)) != strings.ToLower(receipt.SHA256) {
		_ = file.Close()
		return nil, errors.New("The saved result archive failed its integrity check")
	}
	_, err = file.Seek(0, io.SeekStart)
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	return file, nil
}

func (m *Manager) closeLayerFS(e *execution) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	err := e.cleanupErr
	if e.workspace.WorkspaceID != "" {
		err = m.bridge.CloseWorkspace(ctx, e.workspace.WorkspaceID, true)
	}
	if err != nil && e.sandbox != nil {
		stopCtx, stop := context.WithTimeout(context.Background(), 12*time.Second)
		err = errors.Join(err, m.docker.Stop(stopCtx, e.sandbox.ContainerID, 2*time.Second))
		stop()
	}
	if err == nil && e.layerProject != "" {
		err = m.releasePreparedProject(ctx, e.layerProject)
	}
	if err != nil {
		m.mu.Lock()
		m.unavailable = "Sandbox cleanup could not be confirmed. Restart Camp to reconcile owned execution resources."
		m.mu.Unlock()
	}
	return err
}
