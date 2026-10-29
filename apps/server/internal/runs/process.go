package runs

import (
	"context"
	"os"

	runner "github.com/cofy-x/camp/apps/server/internal/execution"
)

func acquireLock(dir string) (*os.File, error) { return runner.AcquireLock(dir) }
func sandboxAvailable(ctx context.Context, c Config, dir string, lock *os.File) error {
	return runner.Available(ctx, runner.Config{RuntimeDir: c.RuntimeDir, NodePath: c.NodePath, APIKey: c.APIKey, BaseURL: c.BaseURL}, dir, lock)
}
