package orchestration

// WorkItem owns a matter across finite execution grants. Its state and evidence
// never constitute an execution capability.
type WorkItem struct {
	QuestionKind             WorkQuestionKind       `json:"questionKind,omitempty"`
	StopRequested            bool                   `json:"stopRequested,omitempty"`
	Execution                *WorkExecution         `json:"execution,omitempty"`
	Result                   *WorkResult            `json:"result,omitempty"`
	CompletionMode           string                 `json:"completionMode"`
	PendingCompletion        *PendingWorkCompletion `json:"pendingCompletion,omitempty"`
	DueAt                    string                 `json:"dueAt,omitempty"`
	ExpiresAt                string                 `json:"expiresAt"`
	DelegationStartAfter     int64                  `json:"delegationStartAfter,omitempty"`
	DelegationPolicyRevision int64                  `json:"delegationPolicyRevision,omitempty"`
	Seq                      int64                  `json:"seq"`
	ID                       string                 `json:"id"`
	RoomID                   string                 `json:"roomId"`
	OwnerUserID              string                 `json:"ownerUserId"`
	PolicyID                 string                 `json:"policyId,omitempty"`
	Goal                     string                 `json:"goal"`
	CompletionCriteria       string                 `json:"completionCriteria"`
	Status                   string                 `json:"status"`
	Revision                 int64                  `json:"revision"`
	ActiveTaskID             string                 `json:"activeTaskId,omitempty"`
	Question                 string                 `json:"question,omitempty"`
	WaitCondition            string                 `json:"waitCondition,omitempty"`
	Deadline                 string                 `json:"deadline"`
	Budget                   WorkBudget             `json:"budget"`
	Usage                    WorkUsage              `json:"usage"`
	Evidence                 []WorkEvidence         `json:"evidence"`
	Decisions                []WorkDecision         `json:"decisions"`
	Tasks                    []WorkTask             `json:"tasks"`
	Timers                   []WorkTimer            `json:"timers"`
	Actions                  []WorkAction           `json:"actions"`
	Watches                  []WorkWatch            `json:"watches"`
	HistoryLimited           bool                   `json:"historyLimited,omitempty"`
	CreatedAt                string                 `json:"createdAt"`
	UpdatedAt                string                 `json:"updatedAt"`
}

// PendingWorkCompletion retains an accepted judgment while external receipts
// settle. Any intervening evidence or decision invalidates this proposal.
type PendingWorkCompletion struct {
	TaskID           string `json:"taskId"`
	Reason           string `json:"reason"`
	EvidenceThrough  int64  `json:"evidenceThrough"`
	DecisionsThrough int64  `json:"decisionsThrough"`
}

type WorkBudget struct {
	MaxEvaluations   int `json:"maxEvaluations"`
	MaxTasks         int `json:"maxTasks"`
	MaxModelRequests int `json:"maxModelRequests"`
	MaxActions       int `json:"maxActions"`
	MaxFollowUps     int `json:"maxFollowUps"`
}

type WorkUsage struct {
	Evaluations           int `json:"evaluations"`
	Tasks                 int `json:"tasks"`
	ModelRequests         int `json:"modelRequests"`
	ReservedModelRequests int `json:"reservedModelRequests"`
	Actions               int `json:"actions"`
	FollowUps             int `json:"followUps"`
}

type WorkEvidence struct {
	EventID   string `json:"eventId"`
	Kind      string `json:"kind"`
	CreatedAt string `json:"createdAt"`
}

type WorkDecision struct {
	PolicyID       string      `json:"policyId,omitempty"`
	ReasonSource   string      `json:"reasonSource,omitempty"`
	Budget         *WorkBudget `json:"budget,omitempty"`
	Deadline       string      `json:"deadline,omitempty"`
	Seq            int64       `json:"seq"`
	ID             string      `json:"id"`
	Kind           string      `json:"kind"`
	Reason         string      `json:"reason"`
	ActorKind      string      `json:"actorKind"`
	ActorID        string      `json:"actorId"`
	EvaluationID   string      `json:"evaluationId,omitempty"`
	PolicyRevision int64       `json:"policyRevision,omitempty"`
	TaskID         string      `json:"taskId,omitempty"`
	EvidenceIDs    []string    `json:"evidenceIds"`
	CreatedAt      string      `json:"createdAt"`
}

type WorkTask struct {
	ID        string `json:"id"`
	Status    string `json:"status"`
	Summary   string `json:"summary"`
	CreatedAt string `json:"createdAt"`
}

type WorkTimer struct {
	ID        string `json:"id"`
	DueAt     string `json:"dueAt"`
	Condition string `json:"condition"`
	Status    string `json:"status"`
	EventID   string `json:"eventId,omitempty"`
}

type WorkWatch struct {
	SourceID  string `json:"sourceId"`
	ThreadKey string `json:"threadKey"`
	Enabled   bool   `json:"enabled"`
}

// WorkAction is a durable intent and receipt projection, not an invitation for
// an Agent to invoke an arbitrary endpoint. Adapters retain dispatch ownership.
type WorkAction struct {
	ID         string `json:"id"`
	Kind       string `json:"kind"`
	Status     string `json:"status"`
	TaskID     string `json:"taskId,omitempty"`
	DeliveryID string `json:"deliveryId,omitempty"`
	Body       string `json:"body"`
	CreatedAt  string `json:"createdAt"`
}

type WorkItemInput struct {
	CompletionMode      string     `json:"completionMode,omitempty"`
	Start               bool       `json:"start,omitempty"`
	Follow              bool       `json:"follow,omitempty"`
	SourceEventID       string     `json:"sourceEventId,omitempty"`
	MessageID           string     `json:"messageId,omitempty"`
	ParticipantAgentIDs []string   `json:"participantAgentIds,omitempty"`
	FirstAgentID        string     `json:"firstAgentId,omitempty"`
	DueAt               string     `json:"dueAt,omitempty"`
	ExpiresAt           string     `json:"expiresAt,omitempty"`
	PolicyRevision      int64      `json:"policyRevision,omitempty"`
	RequestID           string     `json:"requestId"`
	Goal                string     `json:"goal"`
	CompletionCriteria  string     `json:"completionCriteria"`
	PolicyID            string     `json:"policyId,omitempty"`
	EvidenceIDs         []string   `json:"evidenceIds"`
	Deadline            string     `json:"deadline,omitempty"`
	Budget              WorkBudget `json:"budget"`
}

type WorkItemOperation struct {
	CompletionMode      string      `json:"completionMode,omitempty"`
	CompletionCriteria  string      `json:"completionCriteria,omitempty"`
	Comment             string      `json:"comment,omitempty"`
	Content             string      `json:"content,omitempty"`
	TaskID              string      `json:"taskId,omitempty"`
	TaskRevision        int64       `json:"taskRevision,omitempty"`
	QuestionID          string      `json:"questionId,omitempty"`
	ExpiresAt           string      `json:"expiresAt,omitempty"`
	PolicyID            string      `json:"policyId,omitempty"`
	PolicyRevision      int64       `json:"policyRevision,omitempty"`
	Follow              bool        `json:"follow,omitempty"`
	SourceEventID       string      `json:"sourceEventId,omitempty"`
	MessageID           string      `json:"messageId,omitempty"`
	Budget              *WorkBudget `json:"budget,omitempty"`
	Deadline            string      `json:"deadline,omitempty"`
	RequestID           string      `json:"requestId"`
	Revision            int64       `json:"revision"`
	Kind                string      `json:"kind"`
	Reason              string      `json:"reason"`
	EvidenceIDs         []string    `json:"evidenceIds,omitempty"`
	WaitCondition       string      `json:"waitCondition,omitempty"`
	ResumeAt            string      `json:"resumeAt,omitempty"`
	Question            string      `json:"question,omitempty"`
	SourceID            string      `json:"sourceId,omitempty"`
	ThreadKey           string      `json:"threadKey,omitempty"`
	ParticipantAgentIDs []string    `json:"participantAgentIds,omitempty"`
	FirstAgentID        string      `json:"firstAgentId,omitempty"`
}

type WorkItemPage struct {
	Items     []WorkItem `json:"items"`
	NextAfter int64      `json:"nextAfter"`
	HasMore   bool       `json:"hasMore"`
}

type WorkItemSummary struct {
	QuestionKind       WorkQuestionKind `json:"questionKind,omitempty"`
	CompletionMode     string           `json:"completionMode,omitempty"`
	DueAt              string           `json:"dueAt,omitempty"`
	ExpiresAt          string           `json:"expiresAt,omitempty"`
	Frontier           string           `json:"frontier,omitempty"`
	RecentTasks        []WorkTask       `json:"recentTasks,omitempty"`
	RecentDecisions    []WorkDecision   `json:"recentDecisions,omitempty"`
	ContextLimited     bool             `json:"contextLimited,omitempty"`
	Actions            []WorkAction     `json:"actions,omitempty"`
	ActionsLimited     bool             `json:"actionsLimited,omitempty"`
	ID                 string           `json:"id"`
	Goal               string           `json:"goal"`
	CompletionCriteria string           `json:"completionCriteria"`
	Status             string           `json:"status"`
	Revision           int64            `json:"revision"`
	ActiveTaskID       string           `json:"activeTaskId,omitempty"`
	WaitCondition      string           `json:"waitCondition,omitempty"`
	Question           string           `json:"question,omitempty"`
	Budget             WorkBudget       `json:"budget"`
	Usage              WorkUsage        `json:"usage"`
	Deadline           string           `json:"deadline"`
}

// Defaults are an advisory, server-owned finite proposal. Only an explicit
// create/start/delegate command establishes authority and freezes current facts.
type WorkItemDefaults struct {
	Budget              WorkBudget             `json:"budget"`
	ExpiresAt           string                 `json:"expiresAt"`
	ParticipantAgentIDs []string               `json:"participantAgentIds"`
	FirstAgentID        string                 `json:"firstAgentId"`
	Execution           WorkExecutionAllowance `json:"execution"`
}
type WorkExecutionAllowance struct {
	MaxTurns         int `json:"maxTurns"`
	MaxModelRequests int `json:"maxModelRequests"`
}

// Completion confirmation is a typed human decision, distinct from an answer
// that supplies context under an existing execution grant.
type WorkQuestionKind string

const (
	WorkQuestionJudgment   WorkQuestionKind = "judgment"
	WorkQuestionCompletion WorkQuestionKind = "completion"
)
