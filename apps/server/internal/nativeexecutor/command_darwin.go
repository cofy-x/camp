//go:build darwin

package nativeexecutor

import (
	"context"
	"errors"
	"github.com/cofy-x/camp/apps/server/internal/dsh"
	"github.com/cofy-x/camp/apps/server/internal/execution"
	"github.com/cofy-x/camp/apps/server/internal/nativejob"
	"os/exec"
	"path/filepath"
	"time"
)

func executionPaths(cwd, state, runtime string) dsh.Paths {
	return dsh.Paths{Workspace: cwd, State: state, Runtime: runtime}
}
func (e *Executor) dshCommand(ctx context.Context, config execution.Config, dir string) (*exec.Cmd, func() error, error) {
	relay, err := openModelRelay(ctx, config.BaseURL, config.APIKey, config.CAFile)
	if err != nil {
		return nil, nil, err
	}
	config.BaseURL, config.APIKey, config.CAFile = relay.endpoint(), relay.token, ""
	config.HelperPath, config.JobRoot = e.config.SandboxHelper, filepath.Join(dir, "native-dsh-jobs")
	cmd, err := execution.Command(config, dir, e.config.Lock, false)
	if err != nil {
		_ = relay.close()
		return nil, nil, err
	}
	finish := func() error {
		if cmd.Process == nil {
			return relay.close()
		}
		cleanup, stop := context.WithTimeout(context.Background(), 8*time.Second)
		defer stop()
		err := nativejob.Reconcile(cleanup, config.JobRoot)
		return errors.Join(err, relay.close())
	}
	return cmd, finish, nil
}
