//go:build !darwin

package nativeexecutor

import (
	"context"
	"github.com/cofy-x/camp/apps/server/internal/dsh"
	"github.com/cofy-x/camp/apps/server/internal/execution"
	"os/exec"
)

func executionPaths(string, string, string) dsh.Paths { return dsh.Paths{} }
func (e *Executor) dshCommand(ctx context.Context, config execution.Config, dir string) (*exec.Cmd, func() error, error) {
	cmd, err := execution.Command(config, dir, e.config.Lock, false)
	return cmd, func() error { return nil }, err
}
