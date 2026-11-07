package storage

import (
	"context"
	"encoding/json"
)

// MessageWorkOrigin is derived from the exact task origin and canonical Work
// relation. It changes presentation without changing the original authorship.
type MessageWorkOrigin struct {
	WorkItemID string `json:"workItemId"`
}

// Batch one bounded message page instead of reading Task snapshots per message.
func (s *Store) messageWorkOrigins(ctx context.Context, messages []Message) error {
	if len(messages) == 0 {
		return nil
	}
	ids := make([]string, len(messages))
	for i := range messages {
		ids[i] = messages[i].ID
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return err
	}
	rows, err := s.queries().MessageWorkOrigins(ctx, string(raw))
	if err != nil {
		return err
	}
	origins := make(map[string]string, len(rows))
	for _, row := range rows {
		origins[row.ID] = row.WorkItemID
	}
	for i := range messages {
		if id := origins[messages[i].ID]; id != "" {
			messages[i].WorkOrigin = &MessageWorkOrigin{WorkItemID: id}
		}
	}
	return nil
}
