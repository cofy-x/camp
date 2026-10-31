//go:build darwin

package execution

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"

	"github.com/cofy-x/camp/apps/server/internal/nativejob"
	"github.com/cofy-x/camp/apps/server/internal/seatbelt"
)

func Command(c Config, dir string, lock *os.File, test bool) (*exec.Cmd, error) {
	if test || !c.RemoteTools || c.ToolToken == "" || c.ToolEndpoint == "" {
		return nil, errors.New("Native DSH requires authenticated remote tools")
	}
	if !filepath.IsAbs(c.HelperPath) || !filepath.IsAbs(c.JobRoot) || !filepath.IsAbs(c.Cwd) || !filepath.IsAbs(c.StateDir) {
		return nil, errors.New("Native DSH requires owned absolute runtime paths")
	}
	home := filepath.Join(dir, "dsh-home")
	if err := os.MkdirAll(home, 0700); err != nil {
		return nil, err
	}
	if err := prepareDSHStateScope(c.StateDir); err != nil {
		return nil, err
	}
	ports, err := seatbelt.ControlPorts(c.BaseURL, c.ToolEndpoint, c.CoordinationEndpoint)
	if err != nil {
		return nil, err
	}
	patch := filepath.Join(dir, "runtime.patch.yml")
	policy := seatbelt.Policy{Read: []string{c.RuntimeDir, c.NodePath, patch, c.Cwd}, List: []string{filepath.Dir(c.Cwd)}, Write: []string{home, c.StateDir}, Executable: []string{c.NodePath}, Ports: ports}
	if _, err := seatbelt.Profile(policy); err != nil {
		return nil, err
	}
	env := []string{"PATH=/usr/bin:/bin", "HOME=" + home, "TMPDIR=" + home, "XDG_DATA_HOME=" + home, "LANG=en_US.UTF-8", "DSH_HOME=" + c.StateDir, "DSH_TELEMETRY_DISABLED=1", "DEEPSEEK_API_KEY=" + c.APIKey, "DEEPSEEK_BASE_URL=" + c.BaseURL, "CAMP_TOOL_ENDPOINT=" + c.ToolEndpoint, "CAMP_TOOL_TOKEN=" + c.ToolToken}
	if c.CoordinationEndpoint != "" {
		env = append(env, "CAMP_COORDINATION_ENDPOINT="+c.CoordinationEndpoint, "CAMP_COORDINATION_TOKEN="+c.CoordinationToken)
	}
	cmd, _, err := nativejob.Command(c.HelperPath, c.JobRoot, nativejob.Spec{Argv: []string{c.NodePath, filepath.Join(c.RuntimeDir, "node_modules", "@deepseek-ai", "dsh", "lib", "bin.js"), "--profile", "sdk-minimal", "--patch", patch}, Directory: c.Cwd, Env: env, Policy: policy, TimeoutMS: c.TimeoutMS, MaxOutput: 16 << 20})
	return cmd, err
}

// DSH's profile resolver inspects its parent package scope. A private boundary
// keeps state below an arbitrary host checkout independent of that checkout's
// unreadable package.json, without granting the model process ancestor reads.
func prepareDSHStateScope(directory string) error {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return err
	}
	name := filepath.Join(directory, "package.json")
	const content = "{\"private\":true,\"type\":\"module\"}\n"
	file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if errors.Is(err, os.ErrExist) {
		file, err = os.OpenFile(name, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
		if err != nil {
			return err
		}
		defer file.Close()
		info, err := file.Stat()
		if err != nil || !info.Mode().IsRegular() || info.Size() != int64(len(content)) {
			return errors.New("The retained DSH state contains an incompatible package boundary; preserve it for explicit recovery")
		}
		buffer := make([]byte, len(content))
		if _, err := file.Read(buffer); err != nil || string(buffer) != content {
			return errors.New("The retained DSH state package boundary differs; no profile or session was replaced")
		}
		return nil
	}
	if err != nil {
		return err
	}
	_, writeErr := file.WriteString(content)
	return errors.Join(writeErr, file.Sync(), file.Close())
}

func ProjectCommand(ProjectConfig, *os.File) (*exec.Cmd, error) {
	return nil, errors.New("Native dependency preparation is not configured")
}

func Available(ctx context.Context, c Config, dir string, lock *os.File) error {
	return errors.New("Native readiness requires the workspace self-test")
}
