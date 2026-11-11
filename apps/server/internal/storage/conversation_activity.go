package storage

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

type ConversationTask struct {
	ID              string `json:"id"`
	RoomID          string `json:"roomId"`
	OriginMessageID string `json:"originMessageId"`
	WorkItemID      string `json:"workItemId"`
	Status          string `json:"status"`
}

type ConversationActivity struct {
	Items []RunTimeline      `json:"items"`
	Tasks []ConversationTask `json:"tasks"`
}

// Related activity is selected by visible message identities, never by draining
// the Room's execution history. All identities are resolved in this Room.
func (s *Store) ConversationRuns(ctx context.Context, room string, messageIDs []string) (out ConversationActivity, err error) {
	out.Items = []RunTimeline{}
	out.Tasks = []ConversationTask{}
	if len(messageIDs) > MessageWindowLimit+1 {
		return out, problem("invalid_page", "Read activity for at most 51 visible messages.")
	}
	if _, err = s.GetRoom(ctx, room); err != nil {
		return out, err
	}
	raw, err := json.Marshal(messageIDs)
	if err != nil {
		return out, err
	}
	ids, err := s.queries().ConversationRunIDs(ctx, sqlgen.ConversationRunIDsParams{RoomID: room, MessageIds: string(raw)})
	if err != nil {
		return out, err
	}
	taskIDs, err := s.queries().ConversationMessageTaskIDs(ctx, sqlgen.ConversationMessageTaskIDsParams{RoomID: room, MessageIds: string(raw)})
	if err != nil {
		return out, err
	}
	linked := map[string]bool{}
	for _, id := range taskIDs {
		linked[id] = true
	}
	for _, id := range ids {
		if id == "" {
			continue
		}
		item, err := s.GetRunSummary(ctx, id)
		if err != nil {
			return out, err
		}
		out.Items = append(out.Items, item)
		if item.TaskID != "" {
			linked[item.TaskID] = true
		}
	}
	sort.Slice(out.Items, func(i, j int) bool {
		a, b := out.Items[i], out.Items[j]
		if a.CreatedAt == b.CreatedAt {
			return a.ID < b.ID
		}
		return a.CreatedAt < b.CreatedAt
	})
	for id := range linked {
		raw, err := s.queries().ConversationTaskLink(ctx, id)
		if err != nil {
			return out, err
		}
		var task ConversationTask
		if err = json.Unmarshal([]byte(raw), &task); err != nil {
			return out, err
		}
		if task.RoomID == room {
			out.Tasks = append(out.Tasks, task)
		}
	}
	sort.Slice(out.Tasks, func(i, j int) bool { return out.Tasks[i].ID < out.Tasks[j].ID })
	err = out.Bound()
	return out, err
}

func (out *ConversationActivity) Bound() error {
	// Previews can be shortened; execution identities and state never disappear.
	// Originals and the complete changed-file list remain in explicit details.
	body, err := json.Marshal(out)
	if err != nil {
		return err
	}
	if len(body)+1 <= 2<<20 {
		return nil
	}
	for i := range out.Items {
		if len(out.Items[i].Files) > 0 {
			out.Items[i].Files = []RunTimelineFile{}
			out.Items[i].FilesLimited = true
		}
	}
	body, err = json.Marshal(out)
	if err != nil {
		return err
	}
	if len(body)+1 > 2<<20 {
		return problem("invalid_page", "This activity cannot fit a bounded view. Open execution details directly.")
	}
	return nil
}
