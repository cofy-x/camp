// Package processowner establishes kernel-observed ownership of native process
// trees. A shell process group is not an ownership boundary.
package processowner

import (
	"context"
	"errors"
)

var ErrUnavailable = errors.New("native process ownership is unavailable")

// Identity includes the kernel PID generation. Signals must match that
// generation atomically, rather than checking a PID and signalling it later.
type Identity struct {
	PID       int    `json:"pid"`
	Version   uint32 `json:"version"`
	UniqueID  uint64 `json:"uniqueId"`
	Coalition uint64 `json:"coalition"`
	Boot      string `json:"boot"`
}

// Owner identifies one launchd job's resource coalition. The trusted job root
// stays alive until its descendants have stopped, including detached children.
type Owner struct {
	Root Identity `json:"root"`
}

func (o Owner) Terminate(ctx context.Context) error { return terminate(ctx, o) }
