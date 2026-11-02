package storage

import "github.com/cofy-x/camp/apps/server/internal/githubhooks"

type GitHubSource struct {
	ID           string `json:"id"`
	RoomID       string `json:"roomId"`
	Repository   string `json:"repository"`
	RepositoryID int64  `json:"repositoryId"`
	Enabled      bool   `json:"enabled"`
	Revision     int64  `json:"revision"`
	Mode         string `json:"mode"`
	SecretRef    string `json:"-"`
	CreatedBy    string `json:"createdBy"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
}

type CreateGitHubSourceInput struct {
	RequestID  string `json:"requestId"`
	Repository string `json:"repository"`
	Secret     string `json:"secret"`
}

type UpdateGitHubSourceInput struct {
	RequestID string `json:"requestId"`
	Revision  int64  `json:"revision"`
	Enabled   *bool  `json:"enabled,omitempty"`
	Secret    string `json:"secret,omitempty"`
}

type GitHubEvent struct {
	githubhooks.Observation
	Seq            int64  `json:"seq"`
	ID             string `json:"id"`
	SourceID       string `json:"sourceId"`
	RoomID         string `json:"roomId"`
	SourceRevision int64  `json:"sourceRevision"`
	Fingerprint    string `json:"fingerprint"`
	Status         string `json:"status"`
	ReceivedAt     string `json:"receivedAt"`
	DecidedBy      string `json:"decidedBy,omitempty"`
	DecidedAt      string `json:"decidedAt,omitempty"`
	DecisionReason string `json:"decisionReason,omitempty"`
	TaskID         string `json:"taskId,omitempty"`
}

type GitHubEventPage struct {
	Items     []GitHubEvent `json:"items"`
	Through   int64         `json:"through"`
	NextAfter *int64        `json:"nextAfter"`
	HasMore   bool          `json:"hasMore"`
}

type DismissGitHubEventInput struct {
	RequestID string `json:"requestId"`
	Reason    string `json:"reason"`
}

// A private write receipt returns safe output only, including after credential
// rotation. Request hashes bind the original secret without storing its text.
type GitHubReceipt struct {
	Kind   string        `json:"kind"`
	Source *GitHubSource `json:"source,omitempty"`
	Event  *GitHubEvent  `json:"event,omitempty"`
}
