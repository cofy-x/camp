package layerfs

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/cofy-x/camp/apps/server/internal/workspace"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

// BridgeConfig contains only trusted runtime settings. Model configuration is
// never inherited by this process or passed to LayerFS containers.
type BridgeConfig struct {
	Mode            string `json:"mode,omitempty"`
	RunnerID        string `json:"runnerId,omitempty"`
	StorageID       string `json:"storageId,omitempty"`
	DockerBinary    string `json:"dockerBinary,omitempty"`
	Node            string `json:"-"`
	RuntimeDir      string `json:"-"`
	DataRoot        string `json:"dataRoot"`
	Home            string `json:"home"`
	Installation    string `json:"installation,omitempty"`
	Docker          string `json:"docker,omitempty"`
	Volumes         string `json:"volumes,omitempty"`
	MaxArchiveBytes int64  `json:"maxArchiveBytes,omitempty"`
}

type PrepareRequest struct {
	SourceDir      string `json:"sourceDir"`
	BaselineKey    string `json:"baselineKey"`
	EnvironmentKey string `json:"environmentKey,omitempty"`
}

type PreparedProject struct {
	ProjectID      string `json:"projectId"`
	BaselineKey    string `json:"baselineKey"`
	EnvironmentKey string `json:"environmentKey"`
	Reused         bool   `json:"reused"`
	Image          string `json:"image"`
}

type CreateRequest struct {
	ProjectID  string `json:"projectId"`
	BaseCommit string `json:"baseCommit,omitempty"`
	RunID      string `json:"runId"`
}

// Workspace is a receipt obtained from LayerFS public APIs, never Docker labels
// or private Store tables. ContainerName must be resolved to a full Docker ID
// before admitting tools; UID and GID are observed before admission.
type Workspace struct {
	WorkspaceID   string `json:"workspaceId"`
	ProjectID     string `json:"projectId"`
	RunID         string `json:"runId"`
	BaseCommit    string `json:"baseCommit"`
	Branch        string `json:"branch"`
	Status        string `json:"status"`
	Image         string `json:"image"`
	SandboxID     string `json:"sandboxId"`
	ContainerName string `json:"containerName"`
	MountPath     string `json:"mountPath"`
	UID           int    `json:"uid"`
	GID           int    `json:"gid"`
}

type Checkpoint struct {
	WorkspaceID string          `json:"workspaceId"`
	Kind        string          `json:"kind"`
	Head        string          `json:"head"`
	Commit      json.RawMessage `json:"commit"`
}

type ExportedArchive = workspace.Archive

type BridgeError struct {
	Code    string          `json:"code"`
	Message string          `json:"message"`
	Details json.RawMessage `json:"details,omitempty"`
}

func (e *BridgeError) Error() string { return e.Code + ": " + e.Message }

// OutcomeUnknownError means a dispatched operation did not produce a confirmed
// response before the caller stopped waiting. It must not be replayed blindly.
type OutcomeUnknownError struct {
	Method string
	Cause  error
}

func (e *OutcomeUnknownError) Error() string {
	return "LayerFS " + e.Method + " outcome is unknown: " + e.Cause.Error()
}
func (e *OutcomeUnknownError) Unwrap() error { return e.Cause }

type bridgeResponse struct {
	ID     string          `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *BridgeError    `json:"error"`
}

type bridgeReply struct {
	data json.RawMessage
	err  error
}

// Bridge owns one exclusive JSON-lines SDK process. A cancelled request keeps
// its place until its actual response arrives, so later calls cannot consume a
// stale result. Cancellation never repeats an operation or claims cleanup.
type Bridge struct {
	cmd          *exec.Cmd
	stdin        io.WriteCloser
	responses    chan bridgeResponse
	lease        chan struct{}
	done         chan struct{}
	lock         *os.File
	config       string
	docker       string
	dataRoot     string
	mu           sync.Mutex
	sequence     uint64
	stopping     bool
	exitErr      error
	closeOnce    sync.Once
	closeErr     error
	closeTimeout time.Duration
}

func OpenBridge(ctx context.Context, config BridgeConfig) (*Bridge, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if config.Mode != "" && config.Mode != "checkpoint-worker" {
		return nil, errors.New("Unknown LayerFS bridge mode")
	}
	if config.Mode == "checkpoint-worker" && (runtime.GOOS != "darwin" || runtime.GOARCH != "arm64") {
		return nil, errors.New("The checkpoint worker bridge requires native ARM64 Mac execution")
	}
	if config.Node == "" {
		var err error
		config.Node, err = exec.LookPath("node")
		if err != nil {
			return nil, err
		}
	}
	for _, value := range []string{config.Node, config.RuntimeDir, config.DataRoot, config.Home} {
		if !filepath.IsAbs(value) {
			return nil, errors.New("LayerFS runtime paths must be absolute")
		}
	}
	dataRoot, err := filepath.EvalSymlinks(config.DataRoot)
	if err != nil {
		return nil, err
	}
	// macOS spells its system temporary directory through /var and /private/var.
	// Rebase an in-root lexical home before checking its resolved ancestors;
	// user-created symlink escapes are still rejected before any mkdir.
	if bridgePathInside(config.DataRoot, config.Home) {
		relative, _ := filepath.Rel(config.DataRoot, config.Home)
		config.Home = filepath.Join(dataRoot, relative)
	}
	if !bridgePathInside(dataRoot, config.Home) {
		return nil, errors.New("LayerFS home must be inside the Camp data root")
	}
	// Validate existing ancestors before creating anything, then resolve again.
	ancestor := config.Home
	for {
		resolved, resolveErr := filepath.EvalSymlinks(ancestor)
		if resolveErr == nil {
			if !bridgePathInside(dataRoot, resolved) {
				return nil, errors.New("LayerFS home has an ancestor outside the Camp data root")
			}
			break
		}
		if !errors.Is(resolveErr, os.ErrNotExist) {
			return nil, resolveErr
		}
		ancestor = filepath.Dir(ancestor)
	}
	if err = os.MkdirAll(config.Home, 0700); err != nil {
		return nil, err
	}
	config.Home, err = filepath.EvalSymlinks(config.Home)
	if err != nil || !bridgePathInside(dataRoot, config.Home) {
		return nil, errors.New("LayerFS home escapes the Camp data root")
	}
	config.DataRoot = dataRoot
	lock, err := openBridgeLock(config.Home)
	if err != nil {
		return nil, err
	}
	file, err := os.CreateTemp(config.Home, ".camp-bridge-*.json")
	if err != nil {
		_ = lock.Close()
		return nil, err
	}
	configPath := file.Name()
	err = json.NewEncoder(file).Encode(config)
	err = errors.Join(err, file.Close())
	if err != nil {
		_ = os.Remove(configPath)
		_ = lock.Close()
		return nil, err
	}
	cmd := exec.Command(config.Node, filepath.Join(config.RuntimeDir, "bridge.mjs"), configPath)
	configureBridgeProcess(cmd, lock)
	cmd.Dir = config.RuntimeDir
	cmd.Env = []string{"HOME=" + config.Home, "LANG=C.UTF-8", "PATH=" + filepath.Dir(config.Node) + ":/usr/local/bin:/usr/bin:/bin"}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = os.Remove(configPath)
		_ = lock.Close()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		_ = os.Remove(configPath)
		_ = lock.Close()
		return nil, err
	}
	stderr := &limitedBuffer{limit: 8192}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = os.Remove(configPath)
		_ = lock.Close()
		return nil, err
	}
	dockerSocket := config.Docker
	if dockerSocket == "" {
		dockerSocket = "/var/run/docker.sock"
	}
	b := &Bridge{cmd: cmd, stdin: stdin, responses: make(chan bridgeResponse, 1), lease: make(chan struct{}, 1), done: make(chan struct{}), lock: lock, config: configPath, docker: dockerSocket}
	b.dataRoot = dataRoot
	if config.Mode == "checkpoint-worker" {
		b.closeTimeout = 30 * time.Second
	}
	b.lease <- struct{}{}
	readDone := make(chan error, 1)
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 4096), 8<<20)
		for scanner.Scan() {
			var response bridgeResponse
			decoder := json.NewDecoder(bytes.NewReader(scanner.Bytes()))
			decoder.DisallowUnknownFields()
			decodeErr := decoder.Decode(&response)
			var trailing any
			if decodeErr != nil || response.ID == "" || (response.Error == nil) == (response.Result == nil) || decoder.Decode(&trailing) != io.EOF {
				_ = cmd.Process.Kill()
				readDone <- errors.New("LayerFS bridge returned an invalid response")
				return
			}
			select {
			case b.responses <- response:
			default:
				_ = cmd.Process.Kill()
				readDone <- errors.New("LayerFS bridge returned unsolicited responses")
				return
			}
		}
		readDone <- scanner.Err()
	}()
	go func() {
		readErr := <-readDone
		waitErr := cmd.Wait()
		b.mu.Lock()
		b.exitErr = errors.Join(readErr, waitErr)
		if b.exitErr != nil && stderr.Len() > 0 {
			b.exitErr = fmt.Errorf("%w: %s", b.exitErr, strings.TrimSpace(stderr.String()))
		}
		b.mu.Unlock()
		close(b.done)
	}()
	var status json.RawMessage
	if err := b.call(ctx, "runtime.status", nil, &status, false); err != nil {
		_ = b.Close()
		return nil, err
	}
	return b, nil
}

func bridgePathInside(root, target string) bool {
	relative, err := filepath.Rel(root, target)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative)
}

func (b *Bridge) failure() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return errors.Join(errors.New("LayerFS bridge exited"), b.exitErr)
}

func (b *Bridge) call(ctx context.Context, method string, params, result any, closing bool) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-b.done:
		return b.failure()
	case <-b.lease:
	}
	b.mu.Lock()
	if b.stopping && !closing {
		b.mu.Unlock()
		b.lease <- struct{}{}
		return errors.New("LayerFS bridge is closing")
	}
	b.sequence++
	id := strconv.FormatUint(b.sequence, 10)
	b.mu.Unlock()
	request := struct {
		ID     string `json:"id"`
		Method string `json:"method"`
		Params any    `json:"params,omitempty"`
	}{id, method, params}
	data, err := json.Marshal(request)
	if err != nil || len(data) > 64*1024 {
		b.lease <- struct{}{}
		return errors.Join(err, errors.New("LayerFS bridge request is invalid or exceeds 64 KiB"))
	}
	reply := make(chan bridgeReply, 1)
	go func() {
		defer func() { b.lease <- struct{}{} }()
		if _, err := b.stdin.Write(append(data, '\n')); err != nil {
			reply <- bridgeReply{err: err}
			return
		}
		select {
		case response := <-b.responses:
			if response.ID != id {
				_ = b.cmd.Process.Kill()
				reply <- bridgeReply{err: errors.New("LayerFS bridge response ID does not match its request")}
			} else if response.Error != nil {
				reply <- bridgeReply{err: response.Error}
			} else {
				reply <- bridgeReply{data: response.Result}
			}
		case <-b.done:
			reply <- bridgeReply{err: b.failure()}
		}
	}()
	select {
	case <-ctx.Done():
		return &OutcomeUnknownError{Method: method, Cause: ctx.Err()}
	case response := <-reply:
		if response.err != nil {
			var known *BridgeError
			if errors.As(response.err, &known) {
				return response.err
			}
			return &OutcomeUnknownError{Method: method, Cause: response.err}
		}
		if result == nil {
			return nil
		}
		if err := json.Unmarshal(response.data, result); err != nil {
			return &OutcomeUnknownError{Method: method, Cause: err}
		}
		return nil
	}
}

func (b *Bridge) Status(ctx context.Context) (json.RawMessage, error) {
	var result json.RawMessage
	err := b.call(ctx, "runtime.status", nil, &result, false)
	return result, err
}

func (b *Bridge) Ready() error {
	select {
	case <-b.done:
		return b.failure()
	default:
		return nil
	}
}

func (b *Bridge) Prepare(ctx context.Context, request PrepareRequest) (PreparedProject, error) {
	var result PreparedProject
	err := b.call(ctx, "project.prepare", request, &result, false)
	return result, err
}

func (b *Bridge) Create(ctx context.Context, request CreateRequest) (Workspace, error) {
	var result Workspace
	err := b.call(ctx, "workspace.create", request, &result, false)
	return result, err
}

// Checkpoint requires the caller to have stopped admitting tools and confirmed
// that all command descendants have settled. The SDK cannot prove this itself.
func (b *Bridge) Checkpoint(ctx context.Context, workspaceID, message string) (Checkpoint, error) {
	var result Checkpoint
	err := b.call(ctx, "workspace.checkpoint", map[string]any{"workspaceId": workspaceID, "message": message, "quiescent": true}, &result, false)
	return result, err
}

// Export has the same quiescence requirement as Checkpoint. The returned tar is
// untrusted until Camp validates its paths, entry types and size budget.
func (b *Bridge) Export(ctx context.Context, workspaceID, archivePath string) (ExportedArchive, error) {
	return b.ExportWithPolicy(ctx, workspaceID, archivePath, nil)
}

// ExportWithPolicy excludes environment-managed basenames before archive bytes
// are produced. On any failure the Run owner must stop the Sandbox and discard
// its result; no partial archive is retained or treated as published output.
func (b *Bridge) ExportWithPolicy(ctx context.Context, workspaceID, archivePath string, excludeNames []string) (ExportedArchive, error) {
	var result ExportedArchive
	if excludeNames == nil {
		excludeNames = []string{}
	}
	err := b.call(ctx, "workspace.export", map[string]any{"workspaceId": workspaceID, "targetArchive": archivePath, "quiescent": true, "excludeNames": excludeNames}, &result, false)
	return result, err
}

func (b *Bridge) CloseWorkspace(ctx context.Context, workspaceID string, discard bool) error {
	return b.call(ctx, "workspace.close", map[string]any{"workspaceId": workspaceID, "discard": discard}, nil, false)
}

func (b *Bridge) Workspaces(ctx context.Context) ([]Workspace, error) {
	var result []Workspace
	err := b.call(ctx, "workspace.list", nil, &result, false)
	return result, err
}

func (b *Bridge) History(ctx context.Context, projectID string) (json.RawMessage, error) {
	var result json.RawMessage
	err := b.call(ctx, "project.history", map[string]string{"projectId": projectID}, &result, false)
	return result, err
}

// RemoveProject deletes only an idle project in this bridge's private home.
// Active or incomplete workspace receipts prevent removal.
func (b *Bridge) RemoveProject(ctx context.Context, projectID string) error {
	return b.call(ctx, "project.remove", map[string]string{"projectId": projectID}, nil, false)
}

// Reset discards this private home's Disposable Stores before any project work
// is admitted. It uses public Sandbox names, freezes Docker IDs, confirms stop,
// and then asks the public SDK to remove idle projects and owned leftovers.
// It never opens a potentially crashed Store or recovers uncommitted work.
func (b *Bridge) Reset(ctx context.Context) error {
	var sandboxes []struct {
		Name    string `json:"name"`
		Running bool   `json:"running"`
	}
	if err := b.call(ctx, "runtime.reset.begin", nil, &sandboxes, false); err != nil {
		return err
	}
	docker, err := NewDocker(b.docker)
	if err != nil {
		return err
	}
	defer docker.Close()
	for _, sandbox := range sandboxes {
		if !sandbox.Running {
			continue
		}
		containerID, err := docker.Resolve(ctx, sandbox.Name)
		if err != nil {
			return fmt.Errorf("LayerFS startup cleanup cannot resolve %s: %w", sandbox.Name, err)
		}
		if err := docker.Stop(ctx, containerID, 2*time.Second); err != nil {
			return fmt.Errorf("LayerFS startup cleanup cannot stop %s: %w", sandbox.Name, err)
		}
	}
	return b.call(ctx, "runtime.reset", nil, nil, false)
}

// Close stops the SDK process and reaps it. Per-Run Sandbox cleanup remains the
// caller's responsibility; terminating the bridge never proves it happened.
func (b *Bridge) Close() error {
	b.closeOnce.Do(func() {
		b.mu.Lock()
		b.stopping = true
		b.mu.Unlock()
		timeout := b.closeTimeout
		if timeout == 0 {
			timeout = 5 * time.Second
		}
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		err := b.call(ctx, "shutdown", nil, nil, true)
		cancel()
		_ = b.stdin.Close()
		select {
		case <-b.done:
		case <-time.After(2 * time.Second):
			_ = b.cmd.Process.Kill()
			<-b.done
		}
		b.mu.Lock()
		exitErr := b.exitErr
		b.mu.Unlock()
		// A clean exit can race the shutdown acknowledgement on its pipe.
		if exitErr == nil {
			err = nil
		}
		b.closeErr = errors.Join(err, exitErr, os.Remove(b.config), b.lock.Close())
	})
	return b.closeErr
}
