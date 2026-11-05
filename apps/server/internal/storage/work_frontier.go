package storage

import (
	"context"
	"encoding/json"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

// The UI revision includes accounting updates. Assessment validity depends on
// semantic state, explicit decisions and immutable evidence instead. Current
// usage is always reread and checked by the committing operation, never copied
// back from a model's frozen snapshot.
func workAssessmentFrontier(ctx context.Context, q *sqlgen.Queries, w orchestration.WorkItem) (string, error) {
	evidence, err := q.WorkHistoryEvidenceFrontier(ctx, w.ID)
	if err != nil {
		return "", err
	}
	decisions, err := q.WorkHistoryDecisionsFrontier(ctx, w.ID)
	if err != nil {
		return "", err
	}
	semantic := struct {
		ID, RoomID, OwnerUserID, PolicyID, Goal, CompletionCriteria, Status, ActiveTaskID, Question, WaitCondition, Deadline string
		Budget                                                                                                               orchestration.WorkBudget
		Evidence, Decisions                                                                                                  int64
	}{w.ID, w.RoomID, w.OwnerUserID, w.PolicyID, w.Goal, w.CompletionCriteria, w.Status, w.ActiveTaskID, w.Question, w.WaitCondition, w.Deadline, w.Budget, evidence, decisions}
	raw, err := json.Marshal(semantic)
	if err != nil {
		return "", err
	}
	return remoteHash(raw), nil
}
