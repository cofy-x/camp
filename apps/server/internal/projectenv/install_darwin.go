//go:build darwin

package projectenv

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/execution"
	"github.com/cofy-x/camp/apps/server/internal/nativejob"
	"github.com/cofy-x/camp/apps/server/internal/seatbelt"
)

type installOutput struct {
	mu      sync.Mutex
	options Options
	network *execution.PackageNetwork
	cancel  context.CancelFunc
	pending []byte
	err     error
}

func (w *installOutput) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return 0, w.err
	}
	w.pending = append(w.pending, data...)
	for {
		line, rest, found := bytes.Cut(w.pending, []byte{'\n'})
		if !found {
			break
		}
		if len(line) > 256<<10 {
			w.err = errors.New("Dependency output line exceeds its limit")
			break
		}
		w.err = emit(w.options, w.network.Redact(string(line))+"\n")
		w.pending = rest
		if w.err != nil {
			break
		}
	}
	if len(w.pending) > 256<<10 {
		w.err = errors.New("Dependency output line exceeds its limit")
	}
	if w.err != nil {
		w.cancel()
		return 0, w.err
	}
	return len(data), nil
}
func (w *installOutput) finish() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err == nil && len(w.pending) > 0 {
		w.err = emit(w.options, w.network.Redact(string(w.pending)))
	}
	return w.err
}

func runInstall(ctx context.Context, options Options, project, store string) (int, error) {
	if !filepath.IsAbs(options.HelperPath) || !filepath.IsAbs(options.JobRoot) {
		return -1, errors.New("Native dependency preparation requires owned absolute helper/job paths")
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	network, err := execution.OpenPackageNetwork(ctx)
	if err != nil {
		return -1, err
	}
	defer network.Close()
	home := filepath.Join(filepath.Dir(project), "installer-home")
	if err := os.Mkdir(home, 0700); err != nil {
		return -1, err
	}
	runtimeDirectory := filepath.Join(store, "runtime")
	if err := os.MkdirAll(runtimeDirectory, 0700); err != nil {
		return -1, err
	}
	ports, err := seatbelt.ControlPorts(network.Origin())
	if err != nil {
		return -1, err
	}
	policy := seatbelt.Policy{Read: []string{options.NodePath, options.PnpmPath}, Write: []string{project, store, home}, Executable: []string{options.NodePath, options.PnpmPath}, Ports: ports}
	if _, err := seatbelt.Profile(policy); err != nil {
		return -1, err
	}
	env := []string{"PATH=" + filepath.Dir(options.NodePath) + ":/usr/bin:/bin", "HOME=" + home, "TMPDIR=" + home, "XDG_CACHE_HOME=" + filepath.Join(home, "cache"), "XDG_DATA_HOME=" + home, "XDG_STATE_HOME=" + home, "CI=true", "NO_COLOR=1", "LANG=en_US.UTF-8", "npm_config_manage_package_manager_versions=false", "npm_config_update_notifier=false", "npm_config_userconfig=/dev/null", "npm_config_globalconfig=/dev/null", "npm_config_audit=false", "HTTPS_PROXY=" + network.URL(), "HTTP_PROXY=" + network.URL(), "npm_config_https_proxy=" + network.URL(), "npm_config_proxy=" + network.URL()}
	env = append(env, "XDG_RUNTIME_DIR="+runtimeDirectory)
	output := &installOutput{options: options, network: network, cancel: cancel}
	result, err := nativejob.Run(ctx, options.HelperPath, options.JobRoot, nativejob.Spec{Argv: []string{options.PnpmPath, "install", "--frozen-lockfile", "--ignore-scripts", "--ignore-pnpmfile", "--store-dir=" + store, "--package-import-method=copy", "--reporter=append-only"}, Directory: project, Env: env, Policy: policy, TimeoutMS: 180000, MaxOutput: 16 << 20}, strings.NewReader(""), output, output)
	cleanup, stop := context.WithTimeout(context.Background(), 10*time.Second)
	defer stop()
	confirmed := nativejob.Reconcile(cleanup, options.JobRoot)
	if err != nil || confirmed != nil || !result.Clean {
		return result.Code, &CleanupError{errors.Join(err, confirmed)}
	}
	return result.Code, errors.Join(output.finish(), ctx.Err())
}

var _ io.Writer = (*installOutput)(nil)
