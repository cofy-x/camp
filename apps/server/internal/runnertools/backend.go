// Package runnertools exposes Run-scoped filesystem and command capabilities.
// Authorization and publication stay with Runner/Server; the backend owns only
// its sandbox and workspace. No Docker or LayerFS dependency is required here.
package runnertools

import (
	"context"

	"github.com/cofy-x/camp/apps/server/internal/sandboxexec"
)

type Backend interface {
	Call(context.Context, sandboxexec.Request, func(sandboxexec.Frame) error) (sandboxexec.Frame, error)
	Failure() error
}

// BackendError is a known refusal before command admission, or a framed file
// operation error. Infrastructure/unknown cleanup errors must use another type.
type BackendError struct {
	Code    string
	Message string
}

func (e *BackendError) Error() string { return e.Message }
