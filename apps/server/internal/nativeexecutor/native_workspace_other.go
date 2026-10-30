//go:build !darwin

package nativeexecutor

import (
	"context"
	"errors"

	"github.com/cofy-x/camp/apps/server/internal/workspace"
)

func openNativeWorkspace(context.Context, Config) (workspace.Provider, error) {
	return nil, errors.New("Mac native workspaces require Darwin")
}
