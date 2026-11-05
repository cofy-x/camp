package storage

import (
	"context"
	"encoding/json"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func builtinWorkSource(source string) bool {
	return source == "task" || source == "timer" || source == "workitem" || source == "action"
}

// Command facts refer to an immutable Message/receipt rather than duplicating
// its body in the event log. Read and execution projections resolve those
// references without rewriting the fact or attributing Camp members externally.
func workEvidenceEvent(ctx context.Context, q *sqlgen.Queries, row sqlgen.AutomationEvent) (orchestration.SourceEvent, error) {
	ev, err := automationEvent(row)
	if err != nil || ev.Content != "" {
		return ev, err
	}
	metadata := struct {
		ID, Source, Kind, ActorKind, ActorID, MessageID, TaskID string
		Request                                                 *orchestration.RequestRef
	}{ev.ID, ev.SourceID, ev.Kind, ev.ActorKind, ev.ActorID, ev.MessageID, ev.TaskID, ev.Request}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return ev, err
	}
	ev.Content = "Immutable source event metadata (quoted evidence):\n" + string(raw)
	if ev.MessageID != "" {
		message, err := q.GetMessage(ctx, ev.MessageID)
		if err != nil {
			return ev, err
		}
		if message.RoomID != ev.RoomID {
			return ev, problem("invalid_work_item", "The event's original message belongs to another Room.")
		}
		ev.Content += "\n\nReferenced original message:\n" + message.Content
	}
	return ev, nil
}
