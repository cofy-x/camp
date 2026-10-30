package nativeexecutor

import (
	"archive/tar"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/layerfs"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/runnertools"
	"github.com/cofy-x/camp/apps/server/internal/sandboxexec"
)

// HostChecks reports actual observations, not merely installed command names.
// The executable self-test below is the authority for execution readiness.
func HostChecks(config Config) p.ReadinessReport {
	report := p.ReadinessReport{CheckedAt: time.Now().UTC().Format(time.RFC3339Nano), Checks: []p.ReadinessCheck{}}
	add := func(id, label string, err error) {
		status, detail := "passed", ""
		if err != nil {
			status, detail = "failed", err.Error()
		}
		report.Checks = append(report.Checks, p.ReadinessCheck{ID: id, Label: label, Status: status, Detail: detail})
	}
	if runtime.GOOS == "darwin" {
		var platformErr error
		version, err := exec.Command("/usr/bin/sw_vers", "-productVersion").Output()
		major, parseErr := strconv.Atoi(strings.Split(strings.TrimSpace(string(version)), ".")[0])
		if err != nil || parseErr != nil || major < 15 || runtime.GOARCH != "arm64" {
			platformErr = errors.New("Native Mac Runner requires macOS 15 or later on ARM64")
		}
		add("platform", "Supported platform", platformErr)
		var accountErr error
		if os.Geteuid() == 0 {
			accountErr = errors.New("Run the Mac Runner as your unprivileged account; preserve the existing Runner data directory")
		}
		add("permissions", "Machine permissions", accountErr)
		for _, tool := range []struct{ id, label, name string }{{"seatbelt", "Seatbelt isolation", "/usr/bin/sandbox-exec"}, {"launchd", "Owned native jobs", "/bin/launchctl"}, {"git", "Git", "git"}, {"bash", "Bash", "/bin/bash"}} {
			_, err := exec.LookPath(tool.name)
			add(tool.id, tool.label, err)
		}
		return report
	}
	var platformErr error
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" {
		platformErr = errors.New("Camp Runner requires Ubuntu 24.04 amd64")
	}
	if platformErr == nil {
		release, err := os.ReadFile("/etc/os-release")
		values := map[string]string{}
		for _, line := range strings.Split(string(release), "\n") {
			if key, value, ok := strings.Cut(line, "="); ok {
				values[key] = strings.Trim(value, "\"")
			}
		}
		if err != nil || values["ID"] != "ubuntu" || values["VERSION_ID"] != "24.04" {
			platformErr = errors.New("This release is supported on Ubuntu 24.04 amd64")
		}
	}
	add("platform", "Supported platform", platformErr)
	var permissionsErr error
	if os.Geteuid() != 0 {
		permissionsErr = errors.New("Native execution requires a dedicated privileged Runner account (root). Use that account for setup, foreground execution and service installation; do not change ownership of existing Runner storage")
	}
	add("permissions", "Machine permissions", permissionsErr)
	for _, tool := range []struct{ id, label string }{{"bwrap", "Bubblewrap isolation"}, {"git", "Git"}, {"bash", "Bash"}} {
		_, err := exec.LookPath(tool.id)
		add(tool.id, tool.label, err)
	}

	for _, item := range []struct{ id, label, path string }{{"docker", "Local Docker socket", config.DockerSocket}, {"fuse", "FUSE device", "/dev/fuse"}} {
		name := item.path
		if name == "" && item.id == "docker" {
			name = "/var/run/docker.sock"
		}
		info, err := os.Stat(name)
		if err == nil && item.id == "docker" && info.Mode()&os.ModeSocket == 0 {
			err = errors.New("Docker must provide a local Unix socket")
		}
		if err == nil && item.id == "fuse" && info.Mode()&os.ModeCharDevice == 0 {
			err = errors.New("FUSE must provide a character device")
		}
		add(item.id, item.label, err)
	}
	return report
}

// SelfTest exercises the production workspace, command supervisor, artifact export
// and cleanup without a model request. It must run before admitting any work.
func (e *layerRuntime) SelfTest(ctx context.Context) (report p.ReadinessReport) {
	report = HostChecks(e.config)
	add := func(id, label string, err error) {
		status, detail := "passed", ""
		if err != nil {
			status, detail = "failed", err.Error()
		}
		report.Checks = append(report.Checks, p.ReadinessCheck{ID: id, Label: label, Status: status, Detail: detail})
	}
	var nonce [12]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		add("execution", "Isolated execution", err)
		return
	}
	id := "selftest-" + hex.EncodeToString(nonce[:])
	dir, err := os.MkdirTemp(e.config.DataDir, ".self-test-")
	if err != nil {
		add("storage", "Private storage", err)
		return
	}
	defer os.RemoveAll(dir)
	source := filepath.Join(dir, "project")
	err = os.Mkdir(source, 0700)
	if err == nil {
		err = os.WriteFile(filepath.Join(source, "source.txt"), []byte("Camp source stays unchanged\n"), 0600)
	}
	add("storage", "Private storage", err)
	if err != nil {
		return
	}
	var prepared layerfs.PreparedProject
	var workspace layerfs.Workspace
	var tools *runnertools.Tools
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 45*time.Second)
		defer cancel()
		var cleanupErr error
		if tools != nil {
			cleanupErr = tools.Close()
		}
		if workspace.WorkspaceID != "" {
			cleanupErr = errors.Join(cleanupErr, e.bridge.CloseWorkspace(cleanup, workspace.WorkspaceID, true))
		}
		if prepared.ProjectID != "" {
			cleanupErr = errors.Join(cleanupErr, e.bridge.RemoveProject(cleanup, prepared.ProjectID))
		}
		add("cleanup", "Execution cleanup", cleanupErr)
		if cleanupErr != nil {
			e.unsafe(cleanupErr)
		}
		report.Ready = true
		for _, check := range report.Checks {
			if check.Status != "passed" {
				report.Ready = false
			}
		}
	}()
	prepared, err = e.bridge.Prepare(ctx, layerfs.PrepareRequest{SourceDir: source, BaselineKey: id})
	if err == nil {
		workspace, err = e.bridge.Create(ctx, layerfs.CreateRequest{ProjectID: prepared.ProjectID, RunID: id})
	}
	add("workspace", "Isolated workspace", err)
	if err != nil {
		return
	}
	var sandbox *layerfs.Sandbox
	sandbox, err = e.installSandbox(ctx, workspace, "")
	if err == nil {
		tools, err = runnertools.NewTools(ctx, sandbox, false)
	}
	if err == nil {
		var frame sandboxexec.Frame
		frame, err = sandbox.Call(ctx, sandboxexec.Request{Operation: "command", Argv: []string{"bash", "-c", "test \"$(id -u)\" != 0 && test -z \"${DEEPSEEK_API_KEY+x}${CAMP_TOOL_TOKEN+x}\" && test \"$(cat source.txt)\" = 'Camp source stays unchanged' && printf 'Camp Runner self-test\\n' > result.txt && test \"$(cat result.txt)\" = 'Camp Runner self-test'"}, Cwd: "/workspace", TimeoutMS: 15000, MaxOutputBytes: 4096}, func(sandboxexec.Frame) error { return nil })
		if err == nil && fmt.Sprint(frame.Code) != "0" {
			err = errors.New("The isolated command did not complete successfully")
		}
	}
	add("execution", "Isolated file execution", err)
	if err != nil {
		return
	}
	if tools != nil {
		err = tools.Close()
		tools = nil
	}
	if err == nil {
		_, err = e.bridge.Checkpoint(ctx, workspace.WorkspaceID, "Camp Runner self-test")
	}
	var archive layerfs.ExportedArchive
	if err == nil {
		archive, err = e.bridge.ExportWithPolicy(ctx, workspace.WorkspaceID, filepath.Join(dir, "result.tar"), nil)
	}
	if err == nil {
		err = persistExport(archive)
	}
	if err == nil {
		err = checkSelfTestArchive(archive.ArchivePath)
	}
	if err == nil {
		var content []byte
		content, err = os.ReadFile(filepath.Join(source, "source.txt"))
		if err == nil && string(content) != "Camp source stays unchanged\n" {
			err = errors.New("The source snapshot changed")
		}
	}
	add("artifact", "Result capture and source isolation", err)
	return
}

func checkSelfTestArchive(name string) error {
	file, err := os.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()
	reader := tar.NewReader(file)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return errors.New("Self-test result was absent from the captured archive")
		}
		if err != nil {
			return err
		}
		if header.Name == "result.txt" || header.Name == "./result.txt" {
			data, err := io.ReadAll(io.LimitReader(reader, 128))
			if err != nil {
				return err
			}
			if string(data) != "Camp Runner self-test\n" {
				return errors.New("Self-test result content differs from the captured archive")
			}
			return nil
		}
	}
}
