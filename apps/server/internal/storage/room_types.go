package storage

import "encoding/json"

const (
	MaxRunPromptSize      = 8192
	MaxRunOutputSize      = 256 << 10
	MaxRunFilesSize       = 1 << 20
	MaxRunErrorSize       = 8192
	MaxRunEventSize       = 64 << 10
	MaxReferences         = 8
	MaxReferenceTextSize  = 16 << 10
	MaxReferencesTextSize = 64 << 10
)

// ReferenceInput identifies immutable evidence. Descriptions and excerpts are
// resolved by the server when the message is admitted.
type ReferenceInput struct {
	Kind       string `json:"kind"`
	MessageID  string `json:"messageId,omitempty"`
	ResourceID string `json:"resourceId,omitempty"`
	Commit     string `json:"commit,omitempty"`
	Path       string `json:"path,omitempty"`
	DocumentID string `json:"documentId,omitempty"`
}

type Reference struct {
	SourceRunnerID   string           `json:"sourceRunnerId,omitempty"`
	SourceImportID   string           `json:"sourceImportId,omitempty"`
	UploadedByUserID string           `json:"uploadedByUserId,omitempty"`
	Document         *DocumentSummary `json:"document,omitempty"`
	Kind             string           `json:"kind"`
	MessageID        string           `json:"messageId,omitempty"`
	ResourceID       string           `json:"resourceId,omitempty"`
	Commit           string           `json:"commit,omitempty"`
	Path             string           `json:"path,omitempty"`
	Title            string           `json:"title"`
	Author           string           `json:"author,omitempty"`
	Content          string           `json:"content,omitempty"`
	CreatedAt        string           `json:"createdAt,omitempty"`
	ThreadRootID     string           `json:"threadRootId,omitempty"`
	Truncated        bool             `json:"truncated,omitempty"`
	Size             *int             `json:"size,omitempty"`
	Origin           string           `json:"origin,omitempty"`
	SourceRunID      string           `json:"sourceRunId,omitempty"`
	SourceStatus     string           `json:"sourceStatus,omitempty"`
	Published        *bool            `json:"published,omitempty"`
	ResultCommit     string           `json:"resultCommit,omitempty"`
	Checks           string           `json:"checks,omitempty"`
}

type Agent struct {
	OwnerUserID       string `json:"ownerUserId"`
	PreferredRunnerID string `json:"preferredRunnerId"`
	ID                string `json:"id"`
	Name              string `json:"name"`
	Handle            string `json:"handle"`
	Icon              string `json:"icon"`
	Avatar            string `json:"avatar"`
	Instructions      string `json:"instructions"`
	Access            string `json:"access"`
	Enabled           bool   `json:"enabled"`
	Revision          int64  `json:"revision"`
	CreatedAt         string `json:"createdAt"`
	UpdatedAt         string `json:"updatedAt"`
}
type Room struct {
	RunnerID       string `json:"runnerId"`
	RunnerRevision int64  `json:"runnerRevision"`
	LastMessageSeq int64  `json:"lastMessageSeq"`
	ID             string `json:"id"`
	Name           string `json:"name"`
	Description    string `json:"description"`
	ResourceID     string `json:"resourceId"`
	DefaultAgentID string `json:"defaultAgentId"`
	ActivitySeq    int64  `json:"activitySeq"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
}
type Resource struct {
	ID            string          `json:"id"`
	Name          string          `json:"name"`
	SourcePath    string          `json:"-"`
	CurrentCommit string          `json:"currentCommit"`
	Environment   json.RawMessage `json:"environment"`
	CreatedAt     string          `json:"createdAt"`
	UpdatedAt     string          `json:"updatedAt"`
}
type Message struct {
	TaskID       string      `json:"taskId,omitempty"`
	ReviewID     string      `json:"reviewId,omitempty"`
	AuthorUserID string      `json:"authorUserId"`
	AuthorName   string      `json:"authorName"`
	References   []Reference `json:"references"`
	ID           string      `json:"id"`
	RoomID       string      `json:"roomId"`
	ThreadRootID string      `json:"threadRootId"`
	AuthorKind   string      `json:"authorKind"`
	AgentID      string      `json:"agentId"`
	RunID        string      `json:"runId"`
	Content      string      `json:"content"`
	RequestID    string      `json:"requestId"`
	Seq          int64       `json:"seq"`
	CreatedAt    string      `json:"createdAt"`
	UpdatedAt    string      `json:"updatedAt"`
}
type Run struct {
	Executor            string              `json:"executor,omitempty"`
	ContextThroughSeq   int64               `json:"contextThroughSeq,omitempty"`
	PreviousDraftCommit string              `json:"previousDraftCommit,omitempty"`
	TaskID              string              `json:"taskId,omitempty"`
	InputCommit         string              `json:"inputCommit,omitempty"`
	TaskInputRevision   int64               `json:"taskInputRevision,omitempty"`
	ChecksDeferred      bool                `json:"checksDeferred,omitempty"`
	Intent              *CoordinationIntent `json:"intent,omitempty"`
	RunnerID            string              `json:"runnerId"`
	RunnerName          string              `json:"runnerName"`
	AttemptID           string              `json:"attemptId"`
	WaitingReason       string              `json:"waitingReason,omitempty"`
	WriterFence         int64               `json:"writerFence,omitempty"`
	RequestedByUserID   string              `json:"requestedByUserId"`
	References          []Reference         `json:"references"`
	Model               string              `json:"model"`
	RuntimeKey          string              `json:"runtimeKey"`
	ID                  string              `json:"id"`
	RoomID              string              `json:"roomId"`
	ThreadRootID        string              `json:"threadRootId"`
	OriginMessageID     string              `json:"originMessageId"`
	AgentID             string              `json:"agentId"`
	AgentSnapshot       Agent               `json:"agentSnapshot"`
	ResourceID          string              `json:"resourceId"`
	Environment         json.RawMessage     `json:"environment"`
	Prompt              string              `json:"prompt"`
	Status              string              `json:"status"`
	CancellationReason  string              `json:"cancellationReason,omitempty"`
	CreatedAt           string              `json:"createdAt"`
	UpdatedAt           string              `json:"updatedAt"`
	FinishedAt          string              `json:"finishedAt"`
	Error               string              `json:"error"`
	Output              string              `json:"output"`
	SessionID           string              `json:"sessionId"`
	SessionMode         string              `json:"sessionMode"`
	SessionGeneration   int64               `json:"sessionGeneration"`
	BaselineCommit      string              `json:"baselineCommit"`
	ResultCommit        string              `json:"resultCommit"`
	Files               []RunFile           `json:"files"`
	Checks              json.RawMessage     `json:"checks"`
	ExitCode            *int                `json:"exitCode"`
	TestExitCode        *int                `json:"testExitCode"`
	MaxTokens           int                 `json:"maxTokens"`
	MaxSteps            int                 `json:"maxSteps"`
	TimeoutSeconds      int                 `json:"timeoutSeconds"`
	Published           bool                `json:"published"`
}
type RunFile struct {
	Path   string `json:"path"`
	Status string `json:"status"`
	Before string `json:"before"`
	After  string `json:"after"`
}
type RunEvent struct {
	RunID     string `json:"runId"`
	Seq       int64  `json:"seq"`
	Type      string `json:"type"`
	Text      string `json:"text"`
	CreatedAt string `json:"createdAt"`
}
type Conversation struct {
	RuntimeKey    string `json:"runtimeKey"`
	ID            string `json:"id"`
	AgentID       string `json:"agentId"`
	RoomID        string `json:"roomId"`
	ThreadRootID  string `json:"threadRootId"`
	ResourceID    string `json:"resourceId"`
	SessionID     string `json:"sessionId"`
	AgentRevision int64  `json:"agentRevision"`
	Generation    int64  `json:"generation"`
	ContextSeq    int64  `json:"contextSeq"`
	Resumable     bool   `json:"resumable"`
}
type WorkspaceEvent struct {
	ID        int64  `json:"id"`
	Type      string `json:"type"`
	RoomID    string `json:"roomId"`
	EntityID  string `json:"entityId"`
	CreatedAt string `json:"createdAt"`
}
type Budget struct {
	RequireRunner  bool   `json:"-"`
	Model          string `json:"model"`
	RuntimeKey     string `json:"runtimeKey"`
	MaxTokens      int    `json:"maxTokens"`
	MaxSteps       int    `json:"maxSteps"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}
type SendMessageInput struct {
	RunnerRevision    *int64           `json:"runnerRevision,omitempty"`
	References        []ReferenceInput `json:"references,omitempty"`
	RequestID         string           `json:"requestId"`
	Content           string           `json:"content"`
	ThreadRootID      string           `json:"threadRootId"`
	RecipientAgentIDs []string         `json:"recipientAgentIds"`
	PostOnly          bool             `json:"postOnly"`
}
type SendResult struct {
	Message  Message `json:"message"`
	Runs     []Run   `json:"runs"`
	Replayed bool    `json:"replayed"`
}
