package orchestration

// Policy is a versioned delegation by a Camp administrator. Source actors remain
// independent identities; OwnerUserID is the member whose authority is checked.
type Policy struct {
	ID                   string   `json:"id"`
	RoomID               string   `json:"roomId"`
	SourceID             string   `json:"sourceId"`
	Revision             int64    `json:"revision"`
	OwnerUserID          string   `json:"ownerUserId"`
	Enabled              bool     `json:"enabled"`
	Mode                 string   `json:"mode"`
	Rules                string   `json:"rules"`
	AllowedActorIDs      []string `json:"allowedActorIds"`
	ParticipantAgentIDs  []string `json:"participantAgentIds"`
	FirstAgentID         string   `json:"firstAgentId"`
	MaxTurns             int      `json:"maxTurns"`
	MaxModelRequests     int      `json:"maxModelRequests"`
	DailyEvaluations     int      `json:"dailyEvaluations"`
	DailyTasks           int      `json:"dailyTasks"`
	AllowAnswers         bool     `json:"allowAnswers"`
	AllowComments        bool     `json:"allowComments"`
	CredentialConfigured bool     `json:"credentialConfigured"`
	CommentActorID       int64    `json:"commentActorId,omitempty"`
	CredentialRef        string   `json:"-"`
	StartAfter           int64    `json:"startAfter"`
}

type PolicyInput struct {
	RequestID           string   `json:"requestId"`
	Revision            int64    `json:"revision"`
	Enabled             bool     `json:"enabled"`
	Mode                string   `json:"mode"`
	Rules               string   `json:"rules"`
	AllowedActorIDs     []string `json:"allowedActorIds"`
	ParticipantAgentIDs []string `json:"participantAgentIds"`
	FirstAgentID        string   `json:"firstAgentId"`
	MaxTurns            int      `json:"maxTurns"`
	MaxModelRequests    int      `json:"maxModelRequests"`
	DailyEvaluations    int      `json:"dailyEvaluations"`
	DailyTasks          int      `json:"dailyTasks"`
	AllowAnswers        bool     `json:"allowAnswers"`
	AllowComments       bool     `json:"allowComments"`
}

// SourceEvent retains normalized context and a reference to the immutable
// original. Status is processing state, never execution authority.
type SourceEvent struct {
	Seq               int64       `json:"seq"`
	ID                string      `json:"id"`
	RoomID            string      `json:"roomId"`
	SourceID          string      `json:"sourceId"`
	ThreadKey         string      `json:"threadKey"`
	Kind              string      `json:"kind"`
	ActorID           string      `json:"actorId"`
	ActorLogin        string      `json:"actorLogin"`
	ActorKind         string      `json:"actorKind"`
	Request           *RequestRef `json:"request,omitempty"`
	MessageID         string      `json:"messageId,omitempty"`
	ExternalEventID   string      `json:"externalEventId,omitempty"`
	Repository        string      `json:"repository,omitempty"`
	RepositoryID      int64       `json:"repositoryId,omitempty"`
	Number            int64       `json:"number,omitempty"`
	Content           string      `json:"content"`
	OccurredAt        string      `json:"occurredAt"`
	Status            string      `json:"status"`
	Reason            string      `json:"reason,omitempty"`
	EvaluationID      string      `json:"evaluationId,omitempty"`
	TaskID            string      `json:"taskId,omitempty"`
	PolicyRevision    int64       `json:"policyRevision,omitempty"`
	Preview           string      `json:"preview,omitempty"`
	AssessmentOutcome string      `json:"assessmentOutcome,omitempty"`
	AssessmentReason  string      `json:"assessmentReason,omitempty"`
}

type TaskSummary struct {
	ReceivesInputs   bool     `json:"receivesInputs"`
	ReceivedEventIDs []string `json:"receivedEventIds,omitempty"`
	ID               string   `json:"id"`
	Goal             string   `json:"goal"`
	Status           string   `json:"status"`
	Revision         int64    `json:"revision"`
}

type Candidate struct {
	ID             string          `json:"id"`
	Policy         Policy          `json:"policy"`
	Events         []SourceEvent   `json:"events"`
	Tasks          []TaskSummary   `json:"tasks"`
	ModelRevision  string          `json:"modelRevision"`
	TaskFrontier   string          `json:"taskFrontier"`
	Budget         ExecutionBudget `json:"budget"`
	ContextLimited bool            `json:"contextLimited,omitempty"`
	TotalTaskCount int             `json:"totalTaskCount,omitempty"`
}

type ExecutionBudget struct {
	Model          string `json:"model"`
	RuntimeKey     string `json:"runtimeKey"`
	MaxTokens      int    `json:"maxTokens"`
	MaxSteps       int    `json:"maxSteps"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
}

// Assessment is advisory. Only accept/ignore are model outcomes; unavailable
// records transport, timeout, parse or recovery failure without retrying it.
type Assessment struct {
	Outcome string `json:"outcome"`
	Goal    string `json:"goal,omitempty"`
	Reason  string `json:"reason"`
}

type Subscription struct {
	ID        string `json:"id"`
	TaskID    string `json:"taskId"`
	SourceID  string `json:"sourceId"`
	ThreadKey string `json:"threadKey"`
	Enabled   bool   `json:"enabled"`
	Revision  int64  `json:"revision"`
}

type SubscriptionInput struct {
	RequestID string `json:"requestId"`
	Revision  int64  `json:"revision"`
	SourceID  string `json:"sourceId"`
	ThreadKey string `json:"threadKey"`
	Enabled   bool   `json:"enabled"`
}

type Delivery struct {
	ID                          string            `json:"id"`
	TaskID                      string            `json:"taskId"`
	SourceID                    string            `json:"sourceId"`
	PolicyID                    string            `json:"policyId"`
	Revision                    int64             `json:"revision"`
	Repository                  string            `json:"repository"`
	RepositoryID                int64             `json:"repositoryId"`
	Number                      int64             `json:"number"`
	Body                        string            `json:"body"`
	Marker                      string            `json:"marker"`
	ActorID                     int64             `json:"actorId,omitempty"`
	CredentialRef               string            `json:"-"`
	Reconcile                   bool              `json:"-"`
	QuestionID                  string            `json:"questionId,omitempty"`
	Status                      string            `json:"status"`
	Reason                      string            `json:"reason,omitempty"`
	CommentID                   int64             `json:"commentId,omitempty"`
	URL                         string            `json:"url,omitempty"`
	CreatedAt                   string            `json:"createdAt"`
	Attempts                    []DeliveryAttempt `json:"attempts,omitempty"`
	Receipts                    []DeliveryReceipt `json:"receipts,omitempty"`
	Withdrawn                   bool              `json:"withdrawn,omitempty"`
	ReceiptScanLimited          bool              `json:"receiptScanLimited,omitempty"`
	ReconciliationCredentialRef string            `json:"-"`
}

// Attempts preserve dispatch identities across credential rotation and explicit
// resends. A dispatch can fail before POST; its presence never proves delivery.
type DeliveryAttempt struct {
	Revision      int64  `json:"revision,omitempty"`
	ActorID       int64  `json:"actorId"`
	StartedAt     string `json:"startedAt,omitempty"`
	Outcome       string `json:"outcome"`
	CredentialRef string `json:"-"`
}

type DeliveryReceipt struct {
	CommentID int64  `json:"commentId"`
	ActorID   int64  `json:"actorId"`
	URL       string `json:"url"`
}

type DeliveryResult struct {
	ClaimRevision      int64             `json:"-"`
	AttemptOutcome     string            `json:"-"`
	Status             string            `json:"status"`
	Reason             string            `json:"reason,omitempty"`
	CommentID          int64             `json:"commentId,omitempty"`
	URL                string            `json:"url,omitempty"`
	Receipts           []DeliveryReceipt `json:"receipts,omitempty"`
	ReceiptScanLimited bool              `json:"receiptScanLimited,omitempty"`
}
