package storage

import (
	"context"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

// A matter's explicit routing intent applies to its current finite task as well
// as subsequent tasks. The existing subscription owner still controls delivery
// and frozen task input coverage; routing never creates an execution grant.
func setWorkTaskWatch(ctx context.Context, q *sqlgen.Queries, taskID, source, thread string, enabled bool) error {
	if taskID == "" {
		return nil
	}
	subs, err := q.ListAutomationTaskSubscriptions(ctx, taskID)
	if err != nil {
		return err
	}
	found := false
	for _, sub := range subs {
		if sub.SourceID == source && sub.ThreadKey == thread {
			found = true
			break
		}
	}
	if enabled && !found && len(subs) >= 32 {
		return problem("invalid_work_item", "The active collaboration already retains 32 conversation routes. Review its subscriptions before following another.")
	}
	n := int64(0)
	if enabled {
		n = 1
	}
	return q.SetWorkTaskWatch(ctx, sqlgen.SetWorkTaskWatchParams{ID: newID(), TaskID: taskID, SourceID: source, ThreadKey: thread, Enabled: n})
}

func inheritWorkWatches(ctx context.Context, q *sqlgen.Queries, w orchestration.WorkItem, t CollaborationTask) error {
	watches, err := q.WorkWatches(ctx, w.ID)
	if err != nil {
		return err
	}
	for _, watch := range watches {
		if watch.Enabled != 0 {
			if err = setWorkTaskWatch(ctx, q, t.ID, watch.SourceID, watch.ThreadKey, true); err != nil {
				return err
			}
		}
	}
	return nil
}
