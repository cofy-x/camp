// Package runnerprotocol defines the versioned, storage-independent daemon wire contract.
package runnerprotocol

import "encoding/json"

const Version = 2
const MaxFrameBytes = 1 << 20
const MaxArchiveBytes = 256 << 20

type ReadinessCheck struct {
	ID     string `json:"id"`
	Label  string `json:"label"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}
type ReadinessReport struct {
	Ready     bool             `json:"ready"`
	CheckedAt string           `json:"checkedAt"`
	Checks    []ReadinessCheck `json:"checks"`
}

type Capabilities struct {
	Platform       string           `json:"platform,omitempty"`
	Workspace      string           `json:"workspace,omitempty"`
	Sandbox        string           `json:"sandbox,omitempty"`
	Checkpoint     string           `json:"checkpoint,omitempty"`
	Readiness      *ReadinessReport `json:"readiness,omitempty"`
	Executor       string           `json:"executor"`
	Ready          bool             `json:"ready"`
	Reason         string           `json:"reason,omitempty"`
	Slots          int              `json:"slots"`
	Fingerprint    string           `json:"fingerprint"`
	DSHVersion     string           `json:"dshVersion,omitempty"`
	LayerFSVersion string           `json:"layerfsVersion,omitempty"`
}
type Source struct {
	ID        string `json:"id"`
	Alias     string `json:"alias"`
	Revision  string `json:"revision"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}
type Hello struct {
	Version      int          `json:"version"`
	StorageID    string       `json:"storageId"`
	BootID       string       `json:"bootId"`
	Capabilities Capabilities `json:"capabilities"`
	Sources      []Source     `json:"sources"`
	Active       []string     `json:"active"`
}

// Message is a small control frame. Artifact bytes never travel over this socket.
type Message struct {
	Type        string   `json:"type"`
	RequestID   string   `json:"requestId,omitempty"` // grant echoes its causal hello, heartbeat or acceptance
	Version     int      `json:"version,omitempty"`
	Epoch       int64    `json:"epoch,omitempty"`
	RunnerID    string   `json:"runnerId,omitempty"`
	Hello       *Hello   `json:"hello,omitempty"`
	Command     *Command `json:"command,omitempty"`
	Event       *Event   `json:"event,omitempty"`
	OperationID string   `json:"operationId,omitempty"`
	CommandID   string   `json:"commandId,omitempty"`
	Sequence    int64    `json:"sequence,omitempty"`
	Disposition string   `json:"disposition,omitempty"`
	LeaseMS     int64    `json:"leaseMs,omitempty"`
	LeaseUntil  string   `json:"leaseUntil,omitempty"`
	Reason      string   `json:"reason,omitempty"`
	Active      []string `json:"active,omitempty"`
}
type Command struct {
	ID          string         `json:"id"`
	Kind        string         `json:"kind"` // run.start or source.capture
	OperationID string         `json:"operationId"`
	Deadline    string         `json:"deadline"`
	Spec        *ExecutionSpec `json:"spec,omitempty"`
	Source      *SourceCapture `json:"source,omitempty"`
}
type Artifact struct {
	URL    string `json:"url,omitempty"`
	SHA256 string `json:"sha256"`
	Bytes  int64  `json:"bytes"`
}
type ExecutionSpec struct {
	Task           *Collaboration  `json:"task,omitempty"`
	RunID          string          `json:"runId"`
	AttemptID      string          `json:"attemptId"`
	ResourceID     string          `json:"resourceId"`
	BaselineCommit string          `json:"baselineCommit"`
	Fence          int64           `json:"fence"`
	AgentName      string          `json:"agentName"`
	Instructions   string          `json:"instructions"`
	Access         string          `json:"access"`
	Prompt         string          `json:"prompt"`
	Request        string          `json:"request"` // current user request, without reconstructed history or evidence
	Environment    json.RawMessage `json:"environment"`
	Model          string          `json:"model"`
	MaxTokens      int             `json:"maxTokens"`
	MaxSteps       int             `json:"maxSteps"`
	TimeoutSeconds int             `json:"timeoutSeconds"`
	ConversationID string          `json:"conversationId"`
	SessionID      string          `json:"sessionId"`
	Generation     int64           `json:"generation"`
	Resume         bool            `json:"resume"`
	ModelURL       string          `json:"modelUrl"`
	ModelToken     string          `json:"modelToken"`
	Project        Artifact        `json:"project"`
	References     *Artifact       `json:"references,omitempty"`
}
type SourceCapture struct {
	SourceID string `json:"sourceId"`
	Revision string `json:"revision"`
}
type Event struct {
	OperationID string `json:"operationId"`
	Sequence    int64  `json:"sequence"`
	Kind        string `json:"kind"`
	Text        string `json:"text"`
}
type CheckResult struct {
	Name       string `json:"name"`
	Command    string `json:"command"`
	Status     string `json:"status"`
	ExitCode   *int   `json:"exitCode"`
	Output     string `json:"output"`
	StartedAt  string `json:"startedAt"`
	FinishedAt string `json:"finishedAt"`
}
type Result struct {
	Intent           *CoordinationIntent `json:"intent,omitempty"`
	CompletionID     string              `json:"completionId"`
	Status           string              `json:"status"`
	Error            string              `json:"error,omitempty"`
	Summary          string              `json:"summary,omitempty"`
	ExitCode         *int                `json:"exitCode,omitempty"`
	TestExitCode     *int                `json:"testExitCode,omitempty"`
	Checks           []CheckResult       `json:"checks"`
	Archive          *Artifact           `json:"archive,omitempty"`
	CleanupConfirmed bool                `json:"cleanupConfirmed"`
	Resumable        bool                `json:"resumable"`
}
type Receipt struct {
	Disposition string `json:"disposition"` // committed, rejected, accepted
	Reason      string `json:"reason,omitempty"`
	RunID       string `json:"runId,omitempty"`
	Status      string `json:"status,omitempty"`
	LeaseMS     int64  `json:"leaseMs,omitempty"`
	LeaseUntil  string `json:"leaseUntil,omitempty"`
}
type Enrollment struct {
	Token          string `json:"token"`
	RegistrationID string `json:"registrationId"`
	Credential     string `json:"credential"`
	Name           string `json:"name"`
	StorageID      string `json:"storageId"`
}
type Joined struct {
	RunnerID string `json:"runnerId"`
}
