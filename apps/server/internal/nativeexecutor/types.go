// Package nativeexecutor owns platform DSH and workspace execution without business
// persistence. The daemon owns delivery, authorization, and durable receipts.
package nativeexecutor

import (
	"encoding/json"
	"os"
	"time"

	"context"
	"github.com/cofy-x/camp/apps/server/internal/projectenv"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

type Config struct {
	DataDir, RuntimeDir, LayerFSRuntimeDir, SandboxHelper, DockerSocket, NodePath, CAFile string
	Lock                                                                                  *os.File
}

type Request struct {
	Task                                         *p.Collaboration
	PersistIntent                                func(context.Context, p.CoordinationIntent) error
	AttemptID, RunID, ResourceID, BaselineCommit string
	Directory, ReferencesDir                     string
	Persona, Access, Prompt                      string
	Model, ModelURL, ModelToken                  string
	MaxTokens, MaxSteps                          int
	Timeout                                      time.Duration
	Environment                                  json.RawMessage
	SessionID, SessionKey                        string
	Resume                                       bool
}

type Result struct {
	Intent           *p.CoordinationIntent    `json:"intent,omitempty"`
	Status           string                   `json:"status"`
	Error            string                   `json:"error,omitempty"`
	Assistant        string                   `json:"assistant,omitempty"`
	ExitCode         *int                     `json:"exitCode,omitempty"`
	TestExitCode     *int                     `json:"testExitCode,omitempty"`
	Checks           []projectenv.CheckResult `json:"checks"`
	ArchivePath      string                   `json:"archivePath,omitempty"`
	ArchiveSHA256    string                   `json:"archiveSha256,omitempty"`
	ArchiveBytes     int64                    `json:"archiveBytes,omitempty"`
	CleanupConfirmed bool                     `json:"cleanupConfirmed"`
	SessionResumable bool                     `json:"sessionResumable"`
}

type Emit func(kind, text string) error
