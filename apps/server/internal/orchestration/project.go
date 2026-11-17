package orchestration

// ProjectSettings are personal Room defaults. Saving them never starts work.
// Rule delivery is a separate explicit grant, tied to one policy revision.
type ProjectSettings struct {
	RoomID              string              `json:"roomId"`
	Revision            int64               `json:"revision"`
	Enabled             bool                `json:"enabled"`
	ParticipantAgentIDs []string            `json:"participantAgentIds"`
	FirstAgentID        string              `json:"firstAgentId"`
	Budget              WorkBudget          `json:"budget"`
	CompletionMode      string              `json:"completionMode"`
	GitHubDelivery      *GitHubDeliverySpec `json:"githubDelivery,omitempty"`
	RuleID              string              `json:"ruleId,omitempty"`
	RuleRevision        int64               `json:"ruleRevision,omitempty"`
}

type ProjectSettingsInput struct {
	RequestID string `json:"requestId"`
	ProjectSettings
}

type ProjectSettingsReceipt struct {
	Kind     string           `json:"kind"`
	Settings *ProjectSettings `json:"settings,omitempty"`
}
