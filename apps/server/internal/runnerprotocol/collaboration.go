package runnerprotocol

// Collaboration is an immutable, server-authorized execution scope. A model
// can choose a participant; it cannot choose runners or increase its authority.
type Collaboration struct {
	ID           string        `json:"id"`
	Goal         string        `json:"goal"`
	AgentID      string        `json:"agentId"`
	Participants []Participant `json:"participants"`
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
