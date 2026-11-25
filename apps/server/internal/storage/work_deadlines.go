package storage

import (
	"context"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

// Deadlines stop unattended matters even when no follow-up timer was installed,
// including after a Server outage. An observed expiry fences duplicate scans.
func expireWorkItems(ctx context.Context, q *sqlgen.Queries) error {
	rows, err := q.ExpiredWorkItems(ctx, now())
	if err != nil {
		return err
	}
	for _, row := range rows {
		w, err := workItemRow(row)
		if err != nil {
			return err
		}
		if !workExpired(w) {
			continue
		}
		ev := orchestration.SourceEvent{ID: "deadline:" + w.ID + ":" + remoteHash([]byte(w.Deadline)), RoomID: w.RoomID, SourceID: "timer", ThreadKey: "workitem:" + w.ID, WorkItemID: w.ID, CausationID: w.ID, Kind: "workitem.deadline", ActorKind: "system", ActorID: "timer", Content: "The matter's deadline has passed. Further execution requires explicit authorization with a future deadline.", OccurredAt: now(), Status: "context"}
		if err = insertWorkFact(ctx, q, &w, ev); err != nil {
			return err
		}
		w.ObservedAuthorizationExpiry = w.Deadline
		if err = q.CancelWorkTimers(ctx, w.ID); err != nil {
			return err
		}
		// An older intervention or a live collaboration still owns its recovery.
		// The expiry fact wakes readers while retaining that state for renewal.
		if w.Status != "needs_attention" && w.ActiveTaskID == "" {
			markWorkRecovery(&w, workRecoveryForReason("authorization_expired"), "")
			if err = appendWorkDecision(ctx, q, w, orchestration.WorkDecision{Kind: "request_judgment", Reason: ev.Content, ActorKind: "system", ActorID: "timer", EvidenceIDs: []string{ev.ID}}); err != nil {
				return err
			}
			if err = workNotice(ctx, q, &w, "work_item_attention", w.WorkRecovery.Message); err != nil {
				return err
			}
		}
		if err = saveWorkItem(ctx, q, &w); err != nil {
			return err
		}
	}
	return nil
}
