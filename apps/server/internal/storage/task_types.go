package storage

import (
	"encoding/json"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

type CoordinationIntent = p.CoordinationIntent

type TaskParticipant struct {
	Agent      Agent  `json:"agent"`
	RunnerID   string `json:"runnerId"`
	RunnerName string `json:"runnerName"`
}
type TaskInput struct {
	ID           string `json:"id"`
	AuthorUserID string `json:"authorUserId"`
	AuthorName   string `json:"authorName"`
	Content      string `json:"content"`
	CreatedAt    string `json:"createdAt"`
}
type TaskQuestion struct {
	ID               string   `json:"id"`
	Text             string   `json:"text"`
	Options          []string `json:"options"`
	Answer           string   `json:"answer"`
	AnsweredByUserID string   `json:"answeredByUserId"`
}

// CollaborationTask owns its draft, execution fence, explicit inputs and finite
// authorization. Shared-resource publication is a separate durable transition.
type CollaborationTask struct {
	ID                     string            `json:"id"`
	RoomID                 string            `json:"roomId"`
	ThreadRootID           string            `json:"threadRootId"`
	OriginMessageID        string            `json:"originMessageId"`
	ResourceID             string            `json:"resourceId"`
	RequestedByUserID      string            `json:"requestedByUserId"`
	Goal                   string            `json:"goal"`
	Status                 string            `json:"status"`
	Revision               int64             `json:"revision"`
	Participants           []TaskParticipant `json:"participants"`
	CurrentAgentID         string            `json:"currentAgentId"`
	ActiveRunID            string            `json:"activeRunId"`
	ActiveAttemptID        string            `json:"activeAttemptId"`
	DraftFence             int64             `json:"draftFence"`
	BaseSharedCommit       string            `json:"baseSharedCommit"`
	CheckpointCommit       string            `json:"checkpointCommit"`
	CheckpointRevision     int64             `json:"checkpointRevision"`
	PreviousDraftCommit    string            `json:"previousDraftCommit,omitempty"`
	PublishedCommit        string            `json:"publishedCommit"`
	PublicationRunID       string            `json:"publicationRunId,omitempty"`
	Error                  string            `json:"error"`
	Summary                string            `json:"summary"`
	TurnsUsed              int               `json:"turnsUsed"`
	MaxTurns               int               `json:"maxTurns"`
	ModelRequestsUsed      int               `json:"modelRequestsUsed"`
	MaxModelRequests       int               `json:"maxModelRequests"`
	Inputs                 []TaskInput       `json:"inputs"`
	ProcessedInputRevision int64             `json:"processedInputRevision"`
	Question               *TaskQuestion     `json:"question,omitempty"`
	Handoff                string            `json:"handoff"`
	Environment            json.RawMessage   `json:"environment"`
	Budget                 Budget            `json:"budget"`
	References             []Reference       `json:"references"`
	CreatedAt              string            `json:"createdAt"`
	UpdatedAt              string            `json:"updatedAt"`
}
type CreateTaskInput struct {
	RequestID           string           `json:"requestId"`
	Goal                string           `json:"goal"`
	ParticipantAgentIDs []string         `json:"participantAgentIds"`
	FirstAgentID        string           `json:"firstAgentId"`
	ThreadRootID        string           `json:"threadRootId"`
	References          []ReferenceInput `json:"references"`
}
type TaskOperation struct {
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
