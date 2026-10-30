//go:build !darwin

package projectenv

import (
	"context"
	"github.com/cofy-x/camp/apps/server/internal/execution"
)

func runInstall(ctx context.Context, options Options, project, store string) (int, error) {
	cmd, err := execution.ProjectCommand(execution.ProjectConfig{NodePath: options.NodePath, PnpmPath: options.PnpmPath, ProjectDir: project, CacheDir: store, Network: true, Arguments: []string{"/pnpm", "install", "--frozen-lockfile", "--ignore-scripts", "--ignore-pnpmfile", "--store-dir=/store", "--package-import-method=copy", "--reporter=append-only"}}, options.Lock)
	if err != nil {
		return -1, err
	}
	return execution.RunTests(ctx, cmd, func(line string) error { return emit(options, line) })
}
