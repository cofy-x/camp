package storage

import "encoding/json"

// CoordinationIntent is the durable decision of one collaboration round.
// Transport adapters convert Runner decisions before storage validates them.
type CoordinationIntent struct {
	CallID        string   `json:"callId"`
	Kind          string   `json:"kind"`
	TargetAgentID string   `json:"targetAgentId,omitempty"`
	Instruction   string   `json:"instruction,omitempty"`
	Question      string   `json:"question,omitempty"`
	Options       []string `json:"options,omitempty"`
	Summary       string   `json:"summary,omitempty"`
	ArtifactPaths []string `json:"artifactPaths"`
}

type TaskParticipant struct {
	Agent      Agent  `json:"agent"`
	RunnerID   string `json:"runnerId"`
	RunnerName string `json:"runnerName"`
}
type TaskInput struct {
	Seq             int64  `json:"seq,omitempty"`
	ID              string `json:"id"`
	MessageID       string `json:"messageId,omitempty"`
	SourceEventID   string `json:"sourceEventId,omitempty"`
	PartIndex       int    `json:"partIndex,omitempty"`
	AuthorKind      string `json:"authorKind,omitempty"`
	ExternalActorID string `json:"externalActorId,omitempty"`
	AuthorUserID    string `json:"authorUserId"`
	AuthorName      string `json:"authorName"`
	Content         string `json:"content"`
	CreatedAt       string `json:"createdAt"`
}
type TaskQuestion struct {
	ID                        string   `json:"id"`
	Text                      string   `json:"text"`
	Options                   []string `json:"options"`
	Answer                    string   `json:"answer"`
	AnsweredByUserID          string   `json:"answeredByUserId"`
	AnswerSourceEventID       string   `json:"answerSourceEventId,omitempty"`
	AnsweredByExternalActorID string   `json:"answeredByExternalActorId,omitempty"`
}

// CollaborationTask owns its draft, execution fence, explicit inputs and finite
// authorization. Shared-resource publication is a separate durable transition.
type CollaborationTask struct {
	WorkItemID                 string            `json:"workItemId,omitempty"`
	WorkItemCompletionCriteria string            `json:"workItemCompletionCriteria,omitempty"`
	InputProtocol              int               `json:"inputProtocol,omitempty"`
	ID                         string            `json:"id"`
	RoomID                     string            `json:"roomId"`
	ThreadRootID               string            `json:"threadRootId"`
	OriginMessageID            string            `json:"originMessageId"`
	SourceEventID              string            `json:"sourceEventId,omitempty"`
	AutomationSourceEventID    string            `json:"automationSourceEventId,omitempty"`
	AutomationPolicyID         string            `json:"automationPolicyId,omitempty"`
	AutomationPolicyRevision   int64             `json:"automationPolicyRevision,omitempty"`
	AutomationScope            string            `json:"automationScope,omitempty"`
	ResourceID                 string            `json:"resourceId"`
	RequestedByUserID          string            `json:"requestedByUserId"`
	Goal                       string            `json:"goal"`
	Status                     string            `json:"status"`
	Revision                   int64             `json:"revision"`
	Participants               []TaskParticipant `json:"participants"`
	CurrentAgentID             string            `json:"currentAgentId"`
	ActiveRunID                string            `json:"activeRunId"`
	ActiveAttemptID            string            `json:"activeAttemptId"`
	DraftFence                 int64             `json:"draftFence"`
	BaseSharedCommit           string            `json:"baseSharedCommit"`
	CheckpointCommit           string            `json:"checkpointCommit"`
	CheckpointRevision         int64             `json:"checkpointRevision"`
	PreviousDraftCommit        string            `json:"previousDraftCommit,omitempty"`
	PublishedCommit            string            `json:"publishedCommit"`
	PublicationRunID           string            `json:"publicationRunId,omitempty"`
	Error                      string            `json:"error"`
	Summary                    string            `json:"summary"`
	TurnsUsed                  int               `json:"turnsUsed"`
	MaxTurns                   int               `json:"maxTurns"`
	ModelRequestsUsed          int               `json:"modelRequestsUsed"`
	MaxModelRequests           int               `json:"maxModelRequests"`
	Inputs                     []TaskInput       `json:"inputs"`
	InputRevision              int64             `json:"inputRevision"`
	InputBytes                 int64             `json:"inputBytes"`
	CoveredInputRevision       int64             `json:"coveredInputRevision"`
	ProcessedInputRevision     int64             `json:"processedInputRevision"`
	Question                   *TaskQuestion     `json:"question,omitempty"`
	Handoff                    string            `json:"handoff"`
	Environment                json.RawMessage   `json:"environment"`
	Budget                     Budget            `json:"budget"`
	References                 []Reference       `json:"references"`
	CreatedAt                  string            `json:"createdAt"`
	UpdatedAt                  string            `json:"updatedAt"`
}
type CreateTaskInput struct {
	InputProtocol       int              `json:"inputProtocol,omitempty"`
	PagedResponse       bool             `json:"-"`
	RequestID           string           `json:"requestId"`
	SourceEventID       string           `json:"sourceEventId,omitempty"`
	Goal                string           `json:"goal"`
	ParticipantAgentIDs []string         `json:"participantAgentIds"`
	FirstAgentID        string           `json:"firstAgentId"`
	ThreadRootID        string           `json:"threadRootId"`
	References          []ReferenceInput `json:"references"`
}
type TaskOperation struct {
	InputProtocol       int      `json:"inputProtocol,omitempty"`
	PagedResponse       bool     `json:"-"`
	RequestID           string   `json:"requestId"`
	Revision            int64    `json:"revision"`
	Content             string   `json:"content"`
	QuestionID          string   `json:"questionId"`
	AgentID             string   `json:"agentId"`
	AddTurns            int      `json:"addTurns"`
	AddModelRequests    int      `json:"addModelRequests"`
	FromLatest          bool     `json:"fromLatest"`
	ParticipantAgentIDs []string `json:"participantAgentIds,omitempty"`
}
