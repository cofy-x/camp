//go:build !darwin

package nativeexecutor

import (
	"context"
	"errors"
	"github.com/cofy-x/camp/apps/server/internal/workspace"
)

func openHybridWorkspace(context.Context, Config) (workspace.Provider, error) {
	return nil, errors.New("The native checkpoint worker requires macOS ARM64")
}
