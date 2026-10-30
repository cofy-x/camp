// Package workspace defines rebuildable execution state. It has no enrollment,
// permission, transport journal or Server publication authority.
package workspace

import (
	"context"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/runnertools"
)

type Archive struct {
	ArchivePath string `json:"archivePath"`
	Bytes       int64  `json:"bytes"`
	SHA256      string `json:"sha256"`
}

type Spec struct {
	NamespaceKey                                               string
	Source, BaselineKey, EnvironmentKey, AttemptID, References string
	ReadOnly                                                   bool
}

type Instance interface {
	runnertools.Backend
	// Directory is the model's path namespace, not permission to read host files.
	Directory() string
	Capture(context.Context, string, string, []string) (Archive, error)
	Close(context.Context) error
}

type Provider interface {
	Prepare(context.Context, Spec) (Instance, error)
	SelfTest(context.Context) p.ReadinessReport
	Fingerprint() string
	Ready() error
	Close() error
}
