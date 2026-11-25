package orchestration

// WorkCurrentDecision identifies the pending human response on the matter.
// A task question carries the specific task and question revision to answer.
type WorkCurrentDecision struct {
	Kind         string   `json:"kind"`
	Text         string   `json:"text"`
	ID           string   `json:"id,omitempty"`
	Options      []string `json:"options"`
	TaskID       string   `json:"taskId,omitempty"`
	TaskRevision int64    `json:"taskRevision,omitempty"`
}

// WorkExecution is a read-only view of the finite task that currently explains
// a matter's progress. It grants no authority to answer, resume or publish.
type WorkExecution struct {
	TaskID           string            `json:"taskId"`
	Status           string            `json:"status"`
	Revision         int64             `json:"revision"`
	Summary          string            `json:"summary"`
	Error            string            `json:"error"`
	Question         *WorkTaskQuestion `json:"question,omitempty"`
	CurrentAgentName string            `json:"currentAgentName"`
	ActiveRunID      string            `json:"activeRunId,omitempty"`
	UpdatedAt        string            `json:"updatedAt"`
}

type WorkTaskQuestion struct {
	ID      string   `json:"id"`
	Text    string   `json:"text"`
	Options []string `json:"options"`
	Answer  string   `json:"answer"`
}

// WorkResult pins files to the actual saved task checkpoint or shared version.
// Published means Room publication, independently of human approval/delivery.
type WorkResult struct {
	TaskID       string           `json:"taskId"`
	Summary      string           `json:"summary"`
	ResourceID   string           `json:"resourceId"`
	Commit       string           `json:"commit"`
	Published    bool             `json:"published"`
	Files        []WorkResultFile `json:"files"`
	FilesLimited bool             `json:"filesLimited,omitempty"`
}

type WorkResultFile struct {
	Path   string `json:"path"`
	Status string `json:"status"`
}
