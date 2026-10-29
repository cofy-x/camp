//go:build linux

package documenttext

import (
	"os"
	"os/exec"
	"path/filepath"
)

func readerCommand(config Config, format string) (*exec.Cmd, error) {
	bwrap, err := exec.LookPath("bwrap")
	if err != nil {
		return nil, failure("unavailable", "Document preparation requires Linux Bubblewrap (bwrap). The original remains available to download.", true)
	}
	args := []string{"--unshare-user", "--unshare-pid", "--unshare-ipc", "--unshare-uts", "--unshare-net", "--die-with-parent", "--new-session", "--cap-drop", "ALL"}
	for _, path := range []string{"/usr", "/bin", "/lib", "/lib64"} {
		if _, err := os.Stat(path); err == nil {
			args = append(args, "--ro-bind", path, path)
		}
	}
	args = append(args, "--dev", "/dev", "--proc", "/proc", "--tmpfs", "/tmp", "--dir", "/home", "--dir", "/home/camp", "--chdir", "/tmp")
	if format == "pdf" {
		node, err := executable(config.NodePath)
		if err != nil {
			return nil, err
		}
		root, err := filepath.Abs(config.RuntimeDir)
		if err == nil {
			root, err = filepath.EvalSymlinks(root)
		}
		if err != nil || config.RuntimeDir == "" {
			return nil, failure("unavailable", "The PDF text runtime is not installed. Run make install-document-runtime.", true)
		}
		if info, err := os.Stat(filepath.Join(root, "extract.mjs")); err != nil || !info.Mode().IsRegular() {
			return nil, failure("unavailable", "The PDF text runtime is not installed. Run make install-document-runtime.", true)
		}
		args = append(args, "--ro-bind", node, "/node", "--ro-bind", root, "/runtime", "--", "/node", "--max-old-space-size=128", "--max-semi-space-size=8", "/runtime/extract.mjs")
	} else {
		program, err := executable(config.ExecutablePath)
		if err != nil {
			return nil, err
		}
		args = append(args, "--ro-bind", program, "/camp", "--", "/camp", "document-worker")
	}
	cmd := exec.Command(bwrap, args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/home/camp", "TMPDIR=/tmp", "LANG=C.UTF-8", "GOMEMLIMIT=96MiB", "GOMAXPROCS=1"}
	return cmd, nil
}
