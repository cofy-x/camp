package runnerprotocol

// Collaboration is an immutable, server-authorized execution scope. A model
// can choose a participant; it cannot choose runners or increase its authority.
type Collaboration struct {
	Inputs       *TaskInputScope `json:"inputs,omitempty"`
	ID           string          `json:"id"`
	Goal         string          `json:"goal"`
	AgentID      string          `json:"agentId"`
	Participants []Participant   `json:"participants"`
}
type Participant struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Access string `json:"access"`
}
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

// TaskInputScope binds read-back to the current Attempt's frozen prefix.
type TaskInputScope struct {
	Version int   `json:"version"`
	After   int64 `json:"after"`
	Through int64 `json:"through"`
}
type TaskInput struct {
	Seq          int64  `json:"seq"`
	ID           string `json:"id"`
	AuthorUserID string `json:"authorUserId"`
	AuthorName   string `json:"authorName"`
	Content      string `json:"content"`
	CreatedAt    string `json:"createdAt"`
}
type TaskInputPage struct {
	Items     []TaskInput `json:"items"`
	Through   int64       `json:"through"`
	NextAfter *int64      `json:"nextAfter"`
	HasMore   bool        `json:"hasMore"`
}
type TaskInputGrant struct {
	Token string         `json:"token"`
	URL   string         `json:"url"`
	Scope TaskInputScope `json:"scope"`
}

func (s TaskInputScope) Valid() bool { return s.Version == 1 && s.After >= 0 && s.Through >= s.After }
