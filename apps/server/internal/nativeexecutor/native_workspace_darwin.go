//go:build darwin

package nativeexecutor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/nativejob"
	"github.com/cofy-x/camp/apps/server/internal/projectenv"
	"github.com/cofy-x/camp/apps/server/internal/resources"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/runnertools"
	"github.com/cofy-x/camp/apps/server/internal/sandboxexec"
	"github.com/cofy-x/camp/apps/server/internal/seatbelt"
	"github.com/cofy-x/camp/apps/server/internal/workspace"
)

type nativeWorkspaceProvider struct {
	config            Config
	root, fingerprint string
	mu                sync.Mutex
	failure           error
}

type nativeWorkspace struct {
	provider                          *nativeWorkspaceProvider
	root, directory, references, jobs string
	readOnly, sealed, closed          bool
	mu                                sync.Mutex
	failure                           error
}

func openNativeWorkspace(ctx context.Context, config Config) (workspace.Provider, error) {
	if runtime.GOARCH != "arm64" || os.Geteuid() == 0 {
		return nil, errors.New("Native Mac execution requires an unprivileged ARM64 account")
	}
	if _, err := os.Stat("/usr/bin/sandbox-exec"); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(config.DataDir, 0700); err != nil {
		return nil, err
	}
	var err error
	config.DataDir, err = filepath.EvalSymlinks(config.DataDir)
	if err != nil {
		return nil, err
	}
	config.RuntimeDir, err = filepath.EvalSymlinks(config.RuntimeDir)
	if err != nil {
		return nil, err
	}
	if config.SandboxHelper == "" {
		return nil, errors.New("Native execution requires the trusted helper")
	}
	config.SandboxHelper, err = filepath.Abs(config.SandboxHelper)
	if err != nil {
		return nil, err
	}
	helper, err := os.ReadFile(config.SandboxHelper)
	if err != nil || len(helper) == 0 {
		return nil, errors.New("The native process owner is missing")
	}
	manifest, err := os.ReadFile(filepath.Join(config.RuntimeDir, "node_modules", "@deepseek-ai", "dsh", "package.json"))
	var version struct {
		Version string `json:"version"`
	}
	if err != nil || json.Unmarshal(manifest, &version) != nil || version.Version != "0.2.1-rc.3" {
		return nil, errors.New("Install the pinned DSH 0.2.1-rc.3 runtime")
	}
	output, err := exec.CommandContext(ctx, config.NodePath, "--version").Output()
	if err != nil || strings.TrimSpace(string(output)) != "v"+projectenv.NodeVersion {
		return nil, errors.New("Native execution requires the pinned Node runtime")
	}
	fingerprint := sha256.New()
	_, _ = fingerprint.Write(helper)
	_, _ = fingerprint.Write(manifest)
	_, _ = io.WriteString(fingerprint, "camp-native-local-v1\x00"+config.NodePath)
	for _, module := range []string{"coordination", "discovery", "filesystem", "shell", "transport"} {
		data, err := os.ReadFile(filepath.Join(config.RuntimeDir, "camp-tools", module+".mjs"))
		if err != nil {
			return nil, errors.New("Install the complete Camp DSH tool providers")
		}
		_, _ = fingerprint.Write(data)
	}
	for index, root := range config.ToolRoots {
		resolved, err := filepath.EvalSymlinks(root)
		if err != nil || !filepath.IsAbs(root) || containsPath(resolved, config.DataDir) || containsPath(config.DataDir, resolved) {
			return nil, errors.New("Approved tool roots cannot expose Runner storage or its ancestors")
		}
		if _, err := seatbelt.Profile(seatbelt.Policy{Read: []string{resolved}, Executable: []string{resolved}}); err != nil {
			return nil, err
		}
		config.ToolRoots[index] = resolved
		_, _ = io.WriteString(fingerprint, "\x00"+resolved)
	}
	provider := &nativeWorkspaceProvider{config: config, root: filepath.Join(config.DataDir, "native-workspaces"), fingerprint: hex.EncodeToString(fingerprint.Sum(nil))}
	if err := os.MkdirAll(provider.root, 0700); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(provider.root)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if !entry.IsDir() || !strings.HasPrefix(entry.Name(), "attempt-") {
			return nil, errors.New("Unrecognized native workspace storage")
		}
		// Recovery establishes cleanup for an interrupted attempt. It never
		// captures or republishes the previous mutable directory.
		if err := nativejob.Recover(ctx, filepath.Join(provider.root, entry.Name(), "jobs")); err != nil {
			return nil, err
		}
	}
	operations, err := os.ReadDir(filepath.Join(config.DataDir, "operations"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	for _, operation := range operations {
		if !operation.IsDir() {
			continue
		}
		if err := nativejob.Recover(ctx, filepath.Join(config.DataDir, "operations", operation.Name(), "native-dsh-jobs")); err != nil {
			return nil, err
		}
	}
	return provider, nil
}

func containsPath(parent, child string) bool {
	relative, err := filepath.Rel(parent, child)
	return err == nil && (relative == "." || filepath.IsLocal(relative))
}

func (p *nativeWorkspaceProvider) Fingerprint() string { return p.fingerprint }
func (p *nativeWorkspaceProvider) Ready() error        { p.mu.Lock(); defer p.mu.Unlock(); return p.failure }
func (p *nativeWorkspaceProvider) unsafe(err error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.failure = err
}
func (p *nativeWorkspaceProvider) Close() error { return p.Ready() }

func (p *nativeWorkspaceProvider) Prepare(ctx context.Context, spec workspace.Spec) (workspace.Instance, error) {
	if err := p.Ready(); err != nil {
		return nil, err
	}
	root, err := os.MkdirTemp(p.root, "attempt-")
	if err != nil {
		return nil, err
	}
	w := &nativeWorkspace{provider: p, root: root, directory: filepath.Join(root, "workspace"), references: spec.References, jobs: filepath.Join(root, "jobs"), readOnly: spec.ReadOnly}
	if err := copyDependencies(ctx, spec.Source, w.directory, w.directory); err != nil {
		return w, err
	}
	if w.references != "" {
		w.references, err = filepath.EvalSymlinks(w.references)
		if err != nil {
			return w, err
		}
	}
	return w, nil
}

func (w *nativeWorkspace) Directory() string           { return w.directory }
func (w *nativeWorkspace) PathRoots() (string, string) { return w.directory, w.references }
func (w *nativeWorkspace) Failure() error              { w.mu.Lock(); defer w.mu.Unlock(); return w.failure }

type nativeOutput struct {
	kind   string
	emit   func(sandboxexec.Frame) error
	cancel context.CancelFunc
}

func (s nativeOutput) Write(data []byte) (int, error) {
	if s.emit != nil {
		if err := s.emit(sandboxexec.Frame{Type: s.kind, Data: data}); err != nil {
			s.cancel()
			return 0, err
		}
	}
	return len(data), nil
}

func (w *nativeWorkspace) Call(ctx context.Context, request sandboxexec.Request, emit func(sandboxexec.Frame) error) (sandboxexec.Frame, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed || w.sealed || w.failure != nil {
		return sandboxexec.Frame{}, errors.New("The native workspace is sealed or unavailable")
	}
	if w.readOnly && (request.Operation == "command" || request.Operation == "write" || request.Operation == "mkdir" || request.Operation == "remove" || request.Operation == "rename") {
		return sandboxexec.Frame{}, &runnertools.BackendError{Code: "read_only", Message: "This workspace is read-only."}
	}
	if request.Operation != "command" {
		request.Roots = sandboxexec.Roots{Workspace: w.directory, References: w.references}
		frame, err := sandboxexec.FileCall(ctx, request)
		if frame.Type == "error" {
			return frame, &runnertools.BackendError{Code: fmt.Sprint(frame.Code), Message: frame.Message}
		}
		return frame, err
	}
	cwd := request.Cwd
	if cwd == "" {
		cwd = "/workspace"
	}
	if path.Clean(cwd) != cwd || cwd != "/workspace" && !strings.HasPrefix(cwd, "/workspace/") {
		return sandboxexec.Frame{}, &runnertools.BackendError{Code: "invalid_path", Message: "Commands require a working directory inside this workspace."}
	}
	cwd = w.directory + strings.TrimPrefix(cwd, "/workspace")
	resolved, err := filepath.EvalSymlinks(cwd)
	if err != nil || !containsPath(w.directory, resolved) {
		return sandboxexec.Frame{}, &runnertools.BackendError{Code: "invalid_path", Message: "The command directory is unavailable or escapes this workspace."}
	}
	if err := os.MkdirAll(filepath.Join(w.root, "temporary"), 0700); err != nil {
		return sandboxexec.Frame{}, err
	}
	temporary, err := os.MkdirTemp(filepath.Join(w.root, "temporary"), "command-")
	if err != nil {
		return sandboxexec.Frame{}, err
	}
	toolRoots := append([]string{filepath.Dir(w.provider.config.NodePath)}, w.provider.config.ToolRoots...)
	pnpm, findErr := projectenv.FindPnpm(w.provider.config.RuntimeDir)
	if findErr == nil {
		toolRoots = append(toolRoots, filepath.Dir(pnpm))
	}
	search := []string{filepath.Dir(w.provider.config.NodePath)}
	if findErr == nil {
		search = append(search, filepath.Dir(pnpm))
	}
	for _, root := range w.provider.config.ToolRoots {
		search = append(search, filepath.Join(root, "bin"), root)
	}
	search = append(search, "/usr/bin", "/bin", "/usr/sbin", "/sbin")
	argv := append([]string{}, request.Argv...)
	if request.Command != "" {
		argv = []string{"/bin/bash", "--noprofile", "--norc", "-c", request.Command}
	}
	if len(argv) == 0 {
		_ = os.RemoveAll(temporary)
		return sandboxexec.Frame{}, &runnertools.BackendError{Code: "invalid_command", Message: "Provide an executable or command."}
	}
	if !filepath.IsAbs(argv[0]) {
		found := ""
		for _, root := range search {
			candidate := filepath.Join(root, argv[0])
			info, err := os.Stat(candidate)
			if err == nil && info.Mode().IsRegular() && info.Mode()&0111 != 0 {
				found = candidate
				break
			}
		}
		if found == "" {
			_ = os.RemoveAll(temporary)
			return sandboxexec.Frame{}, &runnertools.BackendError{Code: "command_not_found", Message: "The executable is not in the approved tool search path."}
		}
		argv[0] = found
	}
	policy := seatbelt.Policy{Read: append([]string{w.directory}, toolRoots...), Write: []string{w.directory, temporary}, Executable: append([]string{w.directory, temporary}, toolRoots...)}
	if w.references != "" {
		policy.Read = append(policy.Read, w.references)
	}
	if _, err := seatbelt.Profile(policy); err != nil {
		_ = os.RemoveAll(temporary)
		return sandboxexec.Frame{}, &runnertools.BackendError{Code: "invalid_policy", Message: "The requested native tool grants are invalid."}
	}
	commandCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	env := []string{"PATH=" + strings.Join(search, ":"), "HOME=" + temporary, "TMPDIR=" + temporary, "XDG_CACHE_HOME=" + temporary, "GOCACHE=" + filepath.Join(temporary, "go-cache"), "LANG=en_US.UTF-8", "CI=true", "NO_COLOR=1"}
	result, err := nativejob.Run(commandCtx, w.provider.config.SandboxHelper, w.jobs, nativejob.Spec{Argv: argv, Directory: resolved, Env: env, Policy: policy, TimeoutMS: request.TimeoutMS, MaxOutput: request.MaxOutputBytes}, strings.NewReader(""), nativeOutput{"stdout", emit, cancel}, nativeOutput{"stderr", emit, cancel})
	if err != nil {
		w.failure = err
		w.provider.unsafe(err)
		return sandboxexec.Frame{}, err
	}
	_ = os.RemoveAll(temporary)
	reason := result.Reason
	if ctx.Err() != nil {
		reason = "cancelled"
	} else if strings.Contains(reason, "timed out") {
		reason = "timeout"
	}
	return sandboxexec.Frame{Type: "exit", Code: float64(result.Code), Clean: result.Clean, Reason: reason, Truncated: result.Truncated}, nil
}

func (w *nativeWorkspace) Capture(ctx context.Context, target, message string, excluded []string) (workspace.Archive, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.failure != nil || w.closed {
		return workspace.Archive{}, errors.New("Native workspace cleanup is unconfirmed")
	}
	w.sealed = true
	if err := nativejob.Reconcile(ctx, w.jobs); err != nil {
		w.failure = err
		w.provider.unsafe(err)
		return workspace.Archive{}, err
	}
	file, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return workspace.Archive{}, err
	}
	hash := sha256.New()
	err = resources.WriteArchiveWithPolicy(ctx, w.directory, io.MultiWriter(file, hash), resources.Policy{ExcludeNames: excluded})
	if err == nil {
		err = file.Sync()
	}
	stat, statErr := file.Stat()
	err = errors.Join(err, statErr, file.Close())
	if err != nil {
		_ = os.Remove(target)
		return workspace.Archive{}, err
	}
	return workspace.Archive{ArchivePath: target, Bytes: stat.Size(), SHA256: hex.EncodeToString(hash.Sum(nil))}, nil
}

func (w *nativeWorkspace) Close(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return w.failure
	}
	w.closed = true
	err := nativejob.Reconcile(ctx, w.jobs)
	if err != nil {
		w.failure = err
		w.provider.unsafe(err)
		return err
	}
	return os.RemoveAll(w.root)
}

func (provider *nativeWorkspaceProvider) SelfTest(ctx context.Context) (report p.ReadinessReport) {
	report = HostChecks(provider.config)
	add := func(id, label string, err error) {
		status, detail := "passed", ""
		if err != nil {
			status, detail = "failed", err.Error()
		}
		report.Checks = append(report.Checks, p.ReadinessCheck{ID: id, Label: label, Status: status, Detail: detail})
	}
	dir, err := os.MkdirTemp(provider.config.DataDir, ".native-selftest-")
	if err != nil {
		add("storage", "Private storage", err)
		return
	}
	defer os.RemoveAll(dir)
	source := filepath.Join(dir, "source")
	_ = os.Mkdir(source, 0700)
	err = os.WriteFile(filepath.Join(source, "source.txt"), []byte("Camp source stays unchanged\n"), 0600)
	if err != nil {
		add("storage", "Private storage", err)
		return
	}
	active, err := provider.Prepare(ctx, workspace.Spec{Source: source, AttemptID: "selftest"})
	add("workspace", "Native local workspace", err)
	if active == nil {
		return
	}
	defer func() {
		cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		add("cleanup", "Native process cleanup", active.Close(cleanup))
		report.Ready = true
		for _, check := range report.Checks {
			if check.Status != "passed" {
				report.Ready = false
			}
		}
	}()
	if err != nil {
		return
	}
	frame, err := active.Call(ctx, sandboxexec.Request{Operation: "command", Command: `test "$(uname -s)" = Darwin && test "$(uname -m)" = arm64 && test -z "${DEEPSEEK_API_KEY+x}${CAMP_TOOL_TOKEN+x}${SSH_AUTH_SOCK+x}" && test "$(cat source.txt)" = 'Camp source stays unchanged' && printf 'Camp Runner self-test\n' > result.txt`, Cwd: "/workspace", TimeoutMS: 15000, MaxOutputBytes: 4096}, nil)
	if err == nil && fmt.Sprint(frame.Code) != "0" {
		err = errors.New("The native isolated command did not succeed")
	}
	add("execution", "Native Seatbelt execution", err)
	if err != nil {
		return
	}
	archive, err := active.Capture(ctx, filepath.Join(dir, "result.tar"), "selftest", nil)
	if err == nil {
		err = checkSelfTestArchive(archive.ArchivePath)
	}
	if err == nil {
		var data []byte
		data, err = os.ReadFile(filepath.Join(source, "source.txt"))
		if err == nil && string(data) != "Camp source stays unchanged\n" {
			err = errors.New("The source snapshot changed")
		}
	}
	add("artifact", "Native capture and source isolation", err)
	return
}
