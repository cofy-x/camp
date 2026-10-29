//go:build linux

package execution

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

func AcquireLock(dir string) (*os.File, error) {
	f, err := os.OpenFile(filepath.Join(dir, ".lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("Another Camp instance is using this task directory: %w", err)
	}
	return f, nil
}

func Command(c Config, dir string, lock *os.File, test bool) (*exec.Cmd, error) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, fmt.Errorf("Task execution requires Linux Bubblewrap (bwrap)")
	}
	args := []string{"--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts", "--die-with-parent", "--new-session", "--cap-drop", "ALL"}
	// Do not mount host directories, Camp source, databases, or private configuration.
	for _, p := range []string{"/usr", "/bin", "/lib", "/lib64"} {
		if _, err := os.Stat(p); err == nil {
			args = append(args, "--ro-bind", p, p)
		}
	}
	for _, p := range []string{"/etc/resolv.conf", "/etc/hosts", "/etc/nsswitch.conf", "/etc/ssl/certs"} {
		if _, err := os.Stat(p); err == nil {
			args = append(args, "--ro-bind", p, p)
		}
	}
	mountMode := "--bind"
	if test || c.ReadOnlyProject {
		mountMode = "--ro-bind"
	}
	args = append(args, "--dev", "/dev", "--proc", "/proc", "--tmpfs", "/tmp", "--tmpfs", "/home", "--dir", "/home/camp", "--ro-bind", c.NodePath, "/node")
	if c.RemoteTools && !test {
		if c.ToolEndpoint == "" || c.ToolToken == "" {
			return nil, fmt.Errorf("Remote tools require a private execution capability")
		}
		args = append(args, "--tmpfs", "/workspace", "--remount-ro", "/workspace")
	} else {
		args = append(args, mountMode, filepath.Join(dir, "project"), "/workspace")
	}
	args = append(args, "--chdir", "/workspace")
	if !test && c.CAFile != "" {
		info, err := os.Lstat(c.CAFile)
		if !filepath.IsAbs(c.CAFile) || err != nil || !info.Mode().IsRegular() || info.Size() > 4<<20 {
			return nil, fmt.Errorf("The model proxy CA must be a bounded regular file at an absolute path")
		}
		args = append(args, "--ro-bind", c.CAFile, "/camp-ca.pem")
	}
	if !test && c.StateDir != "" {
		info, err := os.Lstat(c.StateDir)
		if !filepath.IsAbs(c.StateDir) || err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("The agent session state must be an owned absolute directory")
		}
		args = append(args, "--bind", c.StateDir, "/state")
	}
	if !test && !c.RemoteTools && c.ReferencesDir != "" {
		info, err := os.Lstat(c.ReferencesDir)
		if !filepath.IsAbs(c.ReferencesDir) || err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			return nil, fmt.Errorf("Reference evidence must be an owned absolute directory")
		}
		args = append(args, "--ro-bind", c.ReferencesDir, "/references")
	}
	if !c.RemoteTools || test {
		args, err = projectTools(args, c.PnpmPath, c.Dependencies)
		if err != nil {
			return nil, err
		}
	}
	if lock != nil {
		args = append(args, "--sync-fd", "3")
	}
	if test {
		// Verification has no network or model credentials and mounts the project read-only.
		if c.Acceptance {
			args = append(args, "--ro-bind", filepath.Join(dir, "acceptance.mjs"), "/acceptance.mjs", "--unshare-net", "--", "/node", "--test", "/acceptance.mjs")
		} else {
			args = append(args, "--unshare-net", "--", "/node", "--test")
		}
	} else {
		args = append(args, "--ro-bind", c.RuntimeDir, "/runtime", "--ro-bind", filepath.Join(dir, "runtime.patch.yml"), "/runtime.patch.yml", "--", "/node", "/runtime/node_modules/@deepseek-ai/dsh/lib/bin.js", "--profile", "sdk-minimal", "--patch", "/runtime.patch.yml")
	}
	cmd := exec.Command(bwrap, args...)
	cmd.Env = []string{"PATH=/:/usr/local/bin:/usr/bin:/bin", "HOME=/home/camp", "TMPDIR=/tmp", "XDG_DATA_HOME=/tmp/camp-data", "LANG=C.UTF-8", "DSH_HOME=/home/camp/.dsh", "DSH_TELEMETRY_DISABLED=1"}
	if !test {
		cmd.Env = append(cmd.Env, "DEEPSEEK_API_KEY="+c.APIKey, "DEEPSEEK_BASE_URL="+c.BaseURL)
		if c.CAFile != "" {
			cmd.Env = append(cmd.Env, "NODE_EXTRA_CA_CERTS=/camp-ca.pem")
		}
		if c.RemoteTools {
			cmd.Env = append(cmd.Env, "CAMP_TOOL_ENDPOINT="+c.ToolEndpoint, "CAMP_TOOL_TOKEN="+c.ToolToken)
		}
		if c.CoordinationEndpoint != "" {
			cmd.Env = append(cmd.Env, "CAMP_COORDINATION_ENDPOINT="+c.CoordinationEndpoint, "CAMP_COORDINATION_TOKEN="+c.CoordinationToken)
		}
	}
	if lock != nil {
		cmd.ExtraFiles = []*os.File{lock}
	}
	return cmd, nil
}

func projectTools(args []string, pnpm string, mounts []Mount) ([]string, error) {
	if pnpm != "" {
		args = append(args, "--ro-bind", pnpm, "/pnpm")
	}
	for _, mount := range mounts {
		if !filepath.IsLocal(mount.Target) || filepath.ToSlash(mount.Target) != mount.Target || strings.Contains(mount.Target, "\\") || filepath.Base(mount.Target) != "node_modules" || !filepath.IsAbs(mount.Source) {
			return nil, fmt.Errorf("Invalid dependency mount")
		}
		args = append(args, "--ro-bind", mount.Source, "/workspace/"+mount.Target)
		// Build tools place disposable caches beside packages. Keep those writes
		// inside this process namespace while dependency content stays immutable.
		for _, cache := range []string{".vite-temp", ".vite", ".vitest-cache", ".cache"} {
			args = append(args, "--tmpfs", "/workspace/"+mount.Target+"/"+cache)
		}
	}
	return args, nil
}

// ProjectCommand shares the task isolation boundary, with explicit network and write access.
func ProjectCommand(c ProjectConfig, lock *os.File) (*exec.Cmd, error) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, fmt.Errorf("Project environments require Linux Bubblewrap (bwrap)")
	}
	if !filepath.IsAbs(c.ProjectDir) || len(c.Arguments) == 0 || (c.Directory != "" && (!filepath.IsLocal(c.Directory) || strings.Contains(c.Directory, "\\"))) {
		return nil, fmt.Errorf("Invalid project command paths or arguments")
	}
	args := []string{"--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts", "--die-with-parent", "--new-session", "--cap-drop", "ALL"}
	for _, p := range []string{"/usr", "/bin", "/lib", "/lib64", "/etc/resolv.conf", "/etc/hosts", "/etc/nsswitch.conf", "/etc/ssl/certs"} {
		if _, err := os.Stat(p); err == nil {
			args = append(args, "--ro-bind", p, p)
		}
	}
	mode := "--bind"
	if c.ReadOnly {
		mode = "--ro-bind"
	}
	args = append(args, "--dev", "/dev", "--proc", "/proc", "--tmpfs", "/tmp", "--tmpfs", "/home", "--dir", "/home/camp", "--ro-bind", c.NodePath, "/node", mode, c.ProjectDir, "/workspace")
	args, err = projectTools(args, c.PnpmPath, c.Dependencies)
	if err != nil {
		return nil, err
	}
	if c.CacheDir != "" {
		args = append(args, "--bind", c.CacheDir, "/store")
	}
	if !c.Network {
		args = append(args, "--unshare-net")
	}
	if lock != nil {
		args = append(args, "--sync-fd", "3")
	}
	args = append(args, "--chdir", filepath.ToSlash(filepath.Join("/workspace", c.Directory)), "--")
	args = append(args, c.Arguments...)
	cmd := exec.Command(bwrap, args...)
	cmd.Env = []string{"PATH=/:/usr/local/bin:/usr/bin:/bin", "HOME=/home/camp", "TMPDIR=/tmp", "XDG_DATA_HOME=/tmp/camp-data", "LANG=C.UTF-8", "CI=true", "NO_COLOR=1", "npm_config_manage_package_manager_versions=false", "npm_config_update_notifier=false", "npm_config_userconfig=/dev/null", "npm_config_globalconfig=/dev/null"}
	if c.CacheDir != "" {
		cmd.Env = append(cmd.Env, "XDG_CACHE_HOME=/store/cache", "XDG_STATE_HOME=/store/state")
	}
	if lock != nil {
		cmd.ExtraFiles = []*os.File{lock}
	}
	return cmd, nil
}

func Available(ctx context.Context, c Config, dir string, lock *os.File) error {
	cmd, err := Command(c, dir, lock, true)
	if err != nil {
		return err
	}
	cmd.Args = append(cmd.Args[:len(cmd.Args)-1], "--version")
	return Probe(ctx, cmd)
}
