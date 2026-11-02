// Package orchestration defines source events and deterministic admission rules.
// Storage applies the plan with current authority and records it atomically with
// the work. This package owns no sessions, database or execution lifecycle.
package orchestration

const PolicyVersion = "camp-explicit-v1"
const MaxRecipients = 16

type Kind string

const (
	RoomMessage  Kind = "room.message"
	TaskStart    Kind = "task.start"
	TaskInput    Kind = "task.inputs"
	TaskAnswer   Kind = "task.answers"
	TaskContinue Kind = "task.continue"
	TaskCancel   Kind = "task.cancel"
)

// Trigger identifies an immutable source event. Content remains with its owning
// Message or task request; source metadata cannot widen an execution grant.
type Trigger struct {
	Source        string      `json:"source"`
	EventID       string      `json:"eventId"`
	Kind          Kind        `json:"kind"`
	ActorKind     string      `json:"actorKind"`
	ActorID       string      `json:"actorId"`
	RoomID        string      `json:"roomId"`
	ThreadRootID  string      `json:"threadRootId"`
	MessageID     string      `json:"messageId,omitempty"`
	TaskID        string      `json:"taskId,omitempty"`
	Request       *RequestRef `json:"request,omitempty"`
	InputRevision int64       `json:"inputRevision,omitempty"`
	OccurredAt    string      `json:"occurredAt"`
}

// RequestRef locates the original idempotency receipt without copying its body.
// Hash binds the canonical request accepted under that actor and scope.
type RequestRef struct {
	Scope string `json:"scope"`
	ID    string `json:"id"`
	Hash  string `json:"hash"`
}

type Work struct {
	Kind     string `json:"kind"`
	ID       string `json:"id"`
	Revision int64  `json:"revision,omitempty"`
}

type Decision struct {
	PolicyVersion     string   `json:"policyVersion"`
	Outcome           string   `json:"outcome"`
	Reason            string   `json:"reason"`
	RecipientAgentIDs []string `json:"recipientAgentIds"`
	Work              []Work   `json:"work"`
}

type Participant struct {
	AgentID       string `json:"agentId"`
	AgentRevision int64  `json:"agentRevision"`
	RunnerID      string `json:"runnerId"`
	Access        string `json:"access"`
}

// Mandate is an admission-time summary of a Run or CollaborationTask grant, not
// a capability. The owning work record and current permissions remain authority.
// Context delivery and answers reuse existing grants and create no Mandate.
type Mandate struct {
	Work              Work          `json:"work"`
	RequestedByUserID string        `json:"requestedByUserId"`
	ResourceID        string        `json:"resourceId"`
	BaseCommit        string        `json:"baseCommit,omitempty"`
	Participants      []Participant `json:"participants"`
	MaxRounds         int           `json:"maxRounds"`
	MaxModelRequests  int           `json:"maxModelRequests,omitempty"`
	MaxTokens         int           `json:"maxTokens"`
	MaxSteps          int           `json:"maxSteps"`
	TimeoutSeconds    int           `json:"timeoutSeconds"`
}

// Record is append-only. Seq orders committed admissions independently of source
// timestamps. Replays return the existing work without appending another record.
type Record struct {
	Seq      int64     `json:"seq,omitempty"`
	Trigger  Trigger   `json:"trigger"`
	Decision Decision  `json:"decision"`
	Mandates []Mandate `json:"mandates"`
}
