package storage

import (
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func createTestWork(t *testing.T, s *Store, actor context.Context, room Room, evidence ...string) orchestration.WorkItem {
	t.Helper()
	w, e := s.CreateWorkItem(actor, room.ID, orchestration.WorkItemInput{RequestID: newID(), Goal: "Prepare the release and follow its review.", CompletionCriteria: "The reviewed release document is ready.", EvidenceIDs: evidence})
	if e != nil {
		t.Fatal(e)
	}
	return w
}
func decideTestWork(t *testing.T, s *Store, actor context.Context, w orchestration.WorkItem, op orchestration.WorkItemOperation) orchestration.WorkItem {
	t.Helper()
	op.RequestID = newID()
	op.Revision = w.Revision
	if op.Reason == "" {
		op.Reason = "An explicit decision based on the recorded evidence."
	}
	next, e := s.OperateWorkItem(actor, w.ID, op, roomBudget)
	if e != nil {
		t.Fatal(e)
	}
	return next
}

func TestWorkItemDecisionReceiptsAuthorityAndImmutableEvidence(t *testing.T) {
	s, admin, member, room, _, _ := taskFixture(t)
	message := sendAutomationDiscussion(t, s, admin, room, "Please prepare the release.")
	eventID := "room:" + message.Message.ID
	in := orchestration.WorkItemInput{RequestID: newID(), Goal: "Prepare release", CompletionCriteria: "A reviewed release is ready.", EvidenceIDs: []string{eventID}}
	w, e := s.CreateWorkItem(admin, room.ID, in)
	if e != nil {
		t.Fatal(e)
	}
	replay, e := s.CreateWorkItem(admin, room.ID, in)
	if e != nil || replay.ID != w.ID {
		t.Fatal(replay, e)
	}
	changed := in
	changed.Goal = "changed"
	_, e = s.CreateWorkItem(admin, room.ID, changed)
	requireCode(t, e, "request_conflict")
	_, e = s.OperateWorkItem(member, w.ID, orchestration.WorkItemOperation{RequestID: newID(), Revision: w.Revision, Kind: "request_judgment", Reason: "Choose", Question: "Approve?"}, roomBudget)
	requireCode(t, e, "forbidden")
	op := orchestration.WorkItemOperation{RequestID: newID(), Revision: w.Revision, Kind: "request_judgment", Reason: "A choice is needed", Question: "Which release target?"}
	w, e = s.OperateWorkItem(admin, w.ID, op, roomBudget)
	if e != nil || w.Status != "waiting_for_person" || len(w.Actions) != 1 {
		t.Fatal(w, e)
	}
	_, e = s.OperateWorkItem(admin, w.ID, orchestration.WorkItemOperation{RequestID: newID(), Revision: op.Revision, Kind: "cancel", Reason: "Stale"}, roomBudget)
	requireCode(t, e, "revision_conflict")
	replayed, e := s.OperateWorkItem(admin, w.ID, op, roomBudget)
	if e != nil || replayed.Revision != w.Revision {
		t.Fatal(replayed, e)
	}
	items, e := s.ListInbox(admin)
	if e != nil || len(items) != 1 || items[0].WorkItemID != w.ID {
		t.Fatal(items, e)
	}
	if _, e = s.db.Exec("UPDATE work_item_evidence SET kind='changed' WHERE work_item_id=?", w.ID); e == nil {
		t.Fatal("evidence was mutable")
	}
	if _, e = s.db.Exec("DELETE FROM work_item_decisions WHERE work_item_id=?", w.ID); e == nil {
		t.Fatal("decision audit was mutable")
	}
	w = decideTestWork(t, s, admin, w, orchestration.WorkItemOperation{Kind: "answer", Reason: "Use the stable target."})
	if w.Status != "open" || len(w.Evidence) != 2 {
		t.Fatal(w)
	}
	w = decideTestWork(t, s, admin, w, orchestration.WorkItemOperation{Kind: "complete", Reason: "The linked evidence meets the release criteria."})
	if w.Status != "completed" {
		t.Fatal(w)
	}
	_, e = s.OperateWorkItem(admin, w.ID, orchestration.WorkItemOperation{RequestID: newID(), Revision: w.Revision, Kind: "wait", Reason: "No grant", WaitCondition: "Later"}, roomBudget)
	requireCode(t, e, "work_item_finished")
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
}

func TestWorkItemTaskResultIsEvidenceAndNewExecutionHasNewFiniteGrant(t *testing.T) {
	s, admin, _, room, writer, _ := taskFixture(t)
	testRoomAutomationPolicy(t, s, admin, room, writer, "auto")
	sendAutomationDiscussion(t, s, admin, room, "Prepare the release document.")
	c := claimAutomation(t, s)
	if e := s.CompleteAutomationEvaluation(ctx, c.ID, orchestration.Assessment{Outcome: "execute", Goal: "Prepare release document", CompletionCriteria: "Document reviewed and ready.", Reason: "A new request."}); e != nil {
		t.Fatal(e)
	}
	page, e := s.WorkItems(admin, room.ID, 0, 20)
	if e != nil || len(page.Items) != 1 {
		t.Fatal(page, e)
	}
	w, e := s.GetWorkItem(admin, page.Items[0].ID)
	if e != nil || w.Status != "running" || w.Usage.Tasks != 1 || w.Usage.ReservedModelRequests != 24 {
		t.Fatal(w, e)
	}
	task, e := s.GetTaskState(ctx, w.ActiveTaskID)
	if e != nil || task.WorkItemID != w.ID || task.WorkItemCompletionCriteria != w.CompletionCriteria {
		t.Fatal(task, e)
	}
	enableTaskInputs(t, s, task.Participants[0].RunnerID)
	r, e := s.GetRun(ctx, task.ActiveRunID)
	if e != nil || r.TaskInputRevision != task.InputRevision {
		t.Fatal("first round did not freeze attached evidence", r, e)
	}
	finishTaskTestRound(t, s, task, strings.Repeat("b", 40), CoordinationIntent{CallID: newID(), Kind: "finish", Summary: "The first draft is ready."})
	if e = s.ReconcileTask(ctx, task.ID); e != nil {
		t.Fatal(e)
	}
	w, e = s.GetWorkItem(admin, w.ID)
	if e != nil || w.Status != "open" || w.ActiveTaskID != "" || len(w.Evidence) != 2 {
		t.Fatal("task result closed the matter", w, e)
	}
	c = claimAutomation(t, s)
	if c.Events[0].WorkItemID != w.ID || c.Events[0].Kind != "task.result" {
		t.Fatal(c)
	}
	if e = s.CompleteAutomationEvaluation(ctx, c.ID, orchestration.Assessment{Outcome: "wait", WorkItemID: w.ID, WaitCondition: "A person reviews the draft", Reason: "Drafting finished; review remains."}); e != nil {
		t.Fatal(e)
	}
	w, _ = s.GetWorkItem(admin, w.ID)
	if w.Status != "waiting" || w.Usage.Tasks != 1 {
		t.Fatal(w)
	}
	sendAutomationDiscussion(t, s, admin, room, "Review found a typo. Please correct it.")
	c = claimAutomation(t, s)
	if e = s.CompleteAutomationEvaluation(ctx, c.ID, orchestration.Assessment{Outcome: "execute", WorkItemID: w.ID, Reason: "A correction is required."}); e != nil {
		t.Fatal(e)
	}
	w, _ = s.GetWorkItem(admin, w.ID)
	if w.Usage.Tasks != 2 || w.ActiveTaskID == task.ID || w.Usage.ReservedModelRequests != 48 {
		t.Fatal("the ended task resumed or allowance reset", w)
	}
	original, _ := s.GetTaskState(ctx, task.ID)
	if original.Status != "completed" {
		t.Fatal(original)
	}
}

func TestWorkItemTimerFiresOnceAndCancellationFencesOldWait(t *testing.T) {
	s, admin, _, room, _, _ := taskFixture(t)
	w := createTestWork(t, s, admin, room)
	w = decideTestWork(t, s, admin, w, orchestration.WorkItemOperation{Kind: "wait", WaitCondition: "Review arrives", ResumeAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)})
	if _, e := s.db.Exec("UPDATE work_item_timers SET due_at=? WHERE work_item_id=?", time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano), w.ID); e != nil {
		t.Fatal(e)
	}
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if e := s.FireWorkItemTimers(ctx); e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	w, e := s.GetWorkItem(admin, w.ID)
	if e != nil || w.Usage.FollowUps != 1 || w.Status != "needs_attention" || len(w.Evidence) != 1 || w.Timers[0].Status != "fired" {
		t.Fatal(w, e)
	}
	assertScalar(t, s, "SELECT count(*) FROM automation_events WHERE source_id='timer'", 1)
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
	w = decideTestWork(t, s, admin, w, orchestration.WorkItemOperation{Kind: "answer", Reason: "Continue observing."})
	w = decideTestWork(t, s, admin, w, orchestration.WorkItemOperation{Kind: "wait", WaitCondition: "Another review", ResumeAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)})
	w = decideTestWork(t, s, admin, w, orchestration.WorkItemOperation{Kind: "cancel", Reason: "The release was cancelled."})
	if _, e = s.db.Exec("UPDATE work_item_timers SET due_at=?", time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)); e != nil {
		t.Fatal(e)
	}
	if e = s.FireWorkItemTimers(ctx); e != nil {
		t.Fatal(e)
	}
	assertScalar(t, s, "SELECT count(*) FROM automation_events WHERE source_id='timer'", 1)
}

func TestWorkItemConcurrentExecutionAndCumulativeModelCeiling(t *testing.T) {
	s, admin, _, room, writer, _ := taskFixture(t)
	w := createTestWork(t, s, admin, room)
	op := orchestration.WorkItemOperation{RequestID: newID(), Revision: w.Revision, Kind: "execute", Reason: "Prepare the release.", ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID}
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			if _, e := s.OperateWorkItem(admin, w.ID, op, roomBudget); e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	w, e := s.GetWorkItem(admin, w.ID)
	if e != nil || w.Usage.Tasks != 1 {
		t.Fatal(w, e)
	}
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 1)
	task, e := s.GetTaskState(ctx, w.ActiveTaskID)
	if e != nil {
		t.Fatal(e)
	}
	enableTaskInputs(t, s, task.Participants[0].RunnerID)
	if e = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		matter, e := getWorkItem(ctx, q, w.ID)
		if e != nil {
			return e
		}
		matter.Budget.MaxModelRequests = matter.Usage.ReservedModelRequests
		return saveWorkItem(ctx, q, &matter)
	}); e != nil {
		t.Fatal(e)
	}
	finishTaskTestRound(t, s, task, "", CoordinationIntent{CallID: newID(), Kind: "ask_person", Question: "Which target?"})
	task, _ = s.GetTaskState(ctx, task.ID)
	_, e = s.OperateTask(admin, task.ID, "continue", TaskOperation{RequestID: newID(), Revision: task.Revision, AddModelRequests: 1})
	requireCode(t, e, "work_budget_exhausted")
	w, _ = s.GetWorkItem(admin, w.ID)
	if w.Usage.ReservedModelRequests != 24 {
		t.Fatal(w)
	}
}

func TestWorkItemFailedDecisionRollsBackTentativeTaskAndRetainsAssessment(t *testing.T) {
	s, admin, _, room, writer, _ := taskFixture(t)
	testRoomAutomationPolicy(t, s, admin, room, writer, "auto")
	sendAutomationDiscussion(t, s, admin, room, "Prepare a document.")
	c := claimAutomation(t, s)
	// The advisory goal exceeds task admission once combined with the facts only
	// if its input is invalid; unavailable must commit without partial work.
	if e := s.CompleteAutomationEvaluation(ctx, c.ID, orchestration.Assessment{Outcome: "wait", Goal: "Watch document review", WaitCondition: "Review arrives", ResumeAt: "invalid", Reason: "A follow-up is useful."}); e != nil {
		t.Fatal(e)
	}
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
	assertScalar(t, s, "SELECT count(*) FROM work_item_timers", 0)
	r, e := s.queries().GetAutomationEvaluation(ctx, c.ID)
	if e != nil || r.Status != "needs_attention" {
		t.Fatal(r, e)
	}
	page, e := s.WorkItems(admin, room.ID, 0, 20)
	if e != nil || len(page.Items) != 1 || page.Items[0].Status != "needs_attention" {
		t.Fatal("a partial proposed matter escaped its savepoint", page, e)
	}
}

func TestWorkItemAuthorizationPreservesConsumptionAndRequiresCurrentOwner(t *testing.T) {
	s, admin, member, room, _, _ := taskFixture(t)
	w := createTestWork(t, s, admin, room)
	if e := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		current, e := getWorkItem(ctx, q, w.ID)
		if e != nil {
			return e
		}
		current.Usage = orchestration.WorkUsage{Evaluations: 12, Tasks: 4, ModelRequests: 60, ReservedModelRequests: 96, Actions: 16, FollowUps: 8}
		current.Status = "needs_attention"
		return saveWorkItem(ctx, q, &current)
	}); e != nil {
		t.Fatal(e)
	}
	w, _ = s.GetWorkItem(admin, w.ID)
	more := orchestration.WorkBudget{MaxEvaluations: 24, MaxTasks: 8, MaxModelRequests: 192, MaxActions: 32, MaxFollowUps: 16}
	op := orchestration.WorkItemOperation{RequestID: newID(), Revision: w.Revision, Kind: "authorize", Reason: "Authorize another finite stage after reviewing progress.", Budget: &more}
	_, e := s.OperateWorkItem(member, w.ID, op, roomBudget)
	requireCode(t, e, "forbidden")
	updated, e := s.OperateWorkItem(admin, w.ID, op, roomBudget)
	if e != nil || updated.Usage != w.Usage || updated.Budget != more || updated.Status != "open" {
		t.Fatal(updated, e)
	}
	if updated.Decisions[0].Budget == nil || *updated.Decisions[0].Budget != more {
		t.Fatal("authorization ceilings absent from immutable decision", updated.Decisions)
	}
	replay, e := s.OperateWorkItem(admin, w.ID, op, roomBudget)
	if e != nil || replay.Revision != updated.Revision || replay.Usage != w.Usage {
		t.Fatal(replay, e)
	}
	less := more
	less.MaxModelRequests = 95
	_, e = s.OperateWorkItem(admin, w.ID, orchestration.WorkItemOperation{RequestID: newID(), Revision: updated.Revision, Kind: "authorize", Reason: "Cannot discard reservations", Budget: &less}, roomBudget)
	requireCode(t, e, "invalid_work_item")
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
}

func TestWorkItemUpgradePreservesOriginalsAndExistingInbox(t *testing.T) {
	s := unmigratedStore(t)
	if e := s.applyMigrations(ctx, migrationSourcesThrough(t, 26)); e != nil {
		t.Fatal(e)
	}
	admin, _ := teamUsers(t, s)
	runner, _ := enrollTestRunner(t, s, admin, "prior")
	room, _ := remoteTestRoom(t, s, admin, runner)
	u, _ := UserFromContext(admin)
	m := Message{ID: newID(), RoomID: room.ID, Seq: 1, AuthorKind: "user", AuthorUserID: u.ID, AuthorName: u.Name, Content: "Keep the immutable original.", CreatedAt: now(), UpdatedAt: now()}
	var original string
	if e := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := insertMessage(ctx, q, m, ""); e != nil {
			return e
		}
		original = `{"id":"preserved","content":"immutable original"}`
		if _, e := q.InsertAutomationEvent(ctx, sqlgen.InsertAutomationEventParams{ID: "preserved", RoomID: room.ID, SourceID: "room", ThreadKey: room.ID + ":room", Kind: "room.message", ActorID: u.ID, ActorKind: "user", MessageID: m.ID, Status: "context", Value: original}); e != nil {
			return e
		}
		return q.CreateInboxItem(ctx, sqlgen.CreateInboxItemParams{ID: "old-inbox", UserID: u.ID, Kind: "mention", RoomID: room.ID, MessageID: m.ID, CreatedAt: now()})
	}); e != nil {
		t.Fatal(e)
	}
	if e := s.migrate(ctx); e != nil {
		t.Fatal(e)
	}
	r, e := s.queries().GetAutomationEvent(ctx, "preserved")
	if e != nil || r.Value != original {
		t.Fatal(r, e)
	}
	inbox, e := s.ListInbox(admin)
	if e != nil || len(inbox) != 1 || inbox[0].ID != "old-inbox" || inbox[0].WorkItemID != "" {
		t.Fatal(inbox, e)
	}
	if _, e = s.db.Exec("UPDATE automation_events SET value='{}' WHERE id='preserved'"); e == nil {
		t.Fatal("event facts can be overwritten")
	}
	if _, e = s.db.Exec("DELETE FROM automation_events WHERE id='preserved'"); e == nil {
		t.Fatal("event facts can be deleted")
	}
	if _, e = s.db.Exec("UPDATE automation_events SET status='ignored' WHERE id='preserved'"); e != nil {
		t.Fatal("processing state cannot advance independently", e)
	}
	assertScalar(t, s, "SELECT count(*) FROM pragma_foreign_key_check", 0)
}

func TestWorkItemDeadlineEscalatesOnceWithoutATimerAndNeedsNewAuthorization(t *testing.T) {
	s, admin, _, room, _, _ := taskFixture(t)
	w := createTestWork(t, s, admin, room)
	if e := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		current, e := getWorkItem(ctx, q, w.ID)
		if e != nil {
			return e
		}
		current.Deadline = time.Now().Add(-time.Hour).UTC().Format(time.RFC3339Nano)
		return saveWorkItem(ctx, q, &current)
	}); e != nil {
		t.Fatal(e)
	}
	for range 3 {
		if e := s.FireWorkItemTimers(ctx); e != nil {
			t.Fatal(e)
		}
	}
	w, e := s.GetWorkItem(admin, w.ID)
	if e != nil || w.Status != "needs_attention" || len(w.Evidence) != 1 || len(w.Actions) != 1 || w.Usage.Evaluations != 0 || w.Usage.FollowUps != 0 {
		t.Fatal(w, e)
	}
	_, e = s.OperateWorkItem(admin, w.ID, orchestration.WorkItemOperation{RequestID: newID(), Revision: w.Revision, Kind: "answer", Reason: "Try again"}, roomBudget)
	requireCode(t, e, "work_budget_exhausted")
	_, e = s.OperateWorkItem(admin, w.ID, orchestration.WorkItemOperation{RequestID: newID(), Revision: w.Revision, Kind: "authorize", Reason: "Add allowance", Budget: &w.Budget}, roomBudget)
	requireCode(t, e, "invalid_work_item")
	w = decideTestWork(t, s, admin, w, orchestration.WorkItemOperation{Kind: "authorize", Reason: "Authorize a new finite stage with a future deadline.", Budget: &w.Budget, Deadline: time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339Nano)})
	if w.Status != "open" {
		t.Fatal(w)
	}
	if e = s.FireWorkItemTimers(ctx); e != nil {
		t.Fatal(e)
	}
	assertScalar(t, s, "SELECT count(*) FROM automation_events WHERE kind='workitem.deadline'", 1)
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
}

func TestWorkItemManyWatchedMattersCannotBlockLaterSourceIntake(t *testing.T) {
	s, admin, _, room, writer, _ := taskFixture(t)
	seed := sendAutomationDiscussion(t, s, admin, room, "A shared conversation can contain independent matters.")
	for range 21 {
		w := createTestWork(t, s, admin, room, "room:"+seed.Message.ID)
		decideTestWork(t, s, admin, w, orchestration.WorkItemOperation{Kind: "follow", SourceID: "room", ThreadKey: room.ID + ":room"})
	}
	testRoomAutomationPolicy(t, s, admin, room, writer, "auto")
	overloaded := sendAutomationDiscussion(t, s, admin, room, "An ambiguous event touching many matters.")
	separate, e := s.SendMessage(admin, room.ID, SendMessageInput{RequestID: newID(), ThreadRootID: seed.Message.ID, Content: "An independent request in another conversation."}, roomBudget)
	if e != nil {
		t.Fatal(e)
	}
	if c, e := s.ClaimAutomationEvaluation(ctx, "frozen-model", roomBudget); e != nil || c != nil {
		t.Fatal("oversized relationship set attempted model work or blocked with an error", c, e)
	}
	row, e := s.queries().GetAutomationEvent(ctx, "room:"+overloaded.Message.ID)
	if e != nil || row.Status != "needs_attention" || row.Reason != "assessment_related_work_limit" {
		t.Fatal(row, e)
	}
	assertScalar(t, s, "SELECT coalesce(sum(evaluations),0) FROM automation_usage", 0)
	c, e := s.ClaimAutomationEvaluation(ctx, "frozen-model", roomBudget)
	if e != nil || c == nil || len(c.Events) != 1 || c.Events[0].MessageID != separate.Message.ID {
		t.Fatal("older oversized event starved independent intake", c, e)
	}
}

func TestWorkItemEscapedPolicyMetadataCannotPoisonEventQueue(t *testing.T) {
	s, admin, _, room, writer, _ := taskFixture(t)
	u, _ := UserFromContext(admin)
	ids := []string{u.ID}
	for range 127 {
		ids = append(ids, strings.Repeat("<", 128))
	}
	p, e := s.SaveAutomationPolicy(admin, room.ID, "room", orchestration.PolicyInput{RequestID: newID(), Enabled: true, Mode: "auto", Rules: strings.Repeat("<", 8192), AllowedActorIDs: ids, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID, DailyEvaluations: 5, DailyTasks: 2})
	if e != nil {
		t.Fatal(e)
	}
	m := sendAutomationDiscussion(t, s, admin, room, "A real request under an oversized escaped configuration.")
	if c, e := s.ClaimAutomationEvaluation(ctx, "frozen-model", roomBudget); e != nil || c != nil {
		t.Fatal("oversized reference metadata blocked the global worker", c, e)
	}
	r, e := s.queries().GetAutomationEvent(ctx, "room:"+m.Message.ID)
	if e != nil || r.Status != "needs_attention" || r.Reason != "assessment_reference_limit" {
		t.Fatal(r, e)
	}
	assertScalar(t, s, "SELECT coalesce(sum(evaluations),0) FROM automation_usage", 0)
	_, e = s.SaveAutomationPolicy(admin, room.ID, "room", orchestration.PolicyInput{RequestID: newID(), Revision: p.Revision, Enabled: true, Mode: "auto", Rules: "Prepare documents", AllowedActorIDs: []string{u.ID}, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID, DailyEvaluations: 5, DailyTasks: 2})
	if e != nil {
		t.Fatal(e)
	}
	later := sendAutomationDiscussion(t, s, admin, room, "A new independent request after repair.")
	c, e := s.ClaimAutomationEvaluation(ctx, "frozen-model", roomBudget)
	if e != nil || c == nil || len(c.Events) != 1 || c.Events[0].MessageID != later.Message.ID {
		t.Fatal("a repaired source replayed the old request or could not progress", c, e)
	}
}

func TestWorkItemWatchManagementUsesAllEvidenceAndCanReenableAtCapacity(t *testing.T) {
	s, admin, _, room, _, _ := taskFixture(t)
	w := createTestWork(t, s, admin, room)
	lastThread := ""
	for range 32 {
		root := sendAutomationDiscussion(t, s, admin, room, "A separate source conversation.")
		reply, e := s.SendMessage(admin, room.ID, SendMessageInput{RequestID: newID(), ThreadRootID: root.Message.ID, Content: "Evidence for this conversation."}, roomBudget)
		if e != nil {
			t.Fatal(e)
		}
		w = decideTestWork(t, s, admin, w, orchestration.WorkItemOperation{Kind: "evidence", EvidenceIDs: []string{"room:" + reply.Message.ID}})
		lastThread = room.ID + ":" + root.Message.ID
		w = decideTestWork(t, s, admin, w, orchestration.WorkItemOperation{Kind: "follow", SourceID: "room", ThreadKey: lastThread})
	}
	if len(w.Watches) != 32 {
		t.Fatal(w.Watches)
	}
	w = decideTestWork(t, s, admin, w, orchestration.WorkItemOperation{Kind: "unfollow", SourceID: "room", ThreadKey: lastThread})
	w = decideTestWork(t, s, admin, w, orchestration.WorkItemOperation{Kind: "follow", SourceID: "room", ThreadKey: lastThread})
	if len(w.Watches) != 32 || w.Usage.Evaluations != 0 || w.Usage.Tasks != 0 {
		t.Fatal(w)
	}
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
}

func TestWorkItemAssessmentOutcomesRemainVisibleInActivity(t *testing.T) {
	s, admin, _, room, writer, _ := taskFixture(t)
	testRoomAutomationPolicy(t, s, admin, room, writer, "observe")
	for _, a := range []orchestration.Assessment{
		{Outcome: "wait", Goal: "Watch the release", WaitCondition: "Review arrives", Reason: "The event requires waiting."},
		{Outcome: "request_judgment", Goal: "Review the release target", Question: "Which target?", Reason: "A person must choose."},
		{Outcome: "complete", WorkItemID: "advisory-existing-matter", Reason: "The quoted evidence indicates completion."},
	} {
		m := sendAutomationDiscussion(t, s, admin, room, "Consider this new event.")
		c := claimAutomation(t, s)
		if e := s.CompleteAutomationEvaluation(ctx, c.ID, a); e != nil {
			t.Fatal(e)
		}
		page, e := s.AutomationActivity(admin, room.ID, AutomationEventQuery{Direction: "desc"})
		if e != nil || len(page.Items) == 0 || page.Items[0].ID != "room:"+m.Message.ID || page.Items[0].AssessmentOutcome != a.Outcome || page.Items[0].Status != "observed" {
			t.Fatal(page, e)
		}
	}
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
}

func TestWorkItemWatchesReachManualTasksAndUnfollowStopsActiveContext(t *testing.T) {
	s, admin, _, room, writer, _ := taskFixture(t)
	seed := sendAutomationDiscussion(t, s, admin, room, "Initial source evidence.")
	w := createTestWork(t, s, admin, room, "room:"+seed.Message.ID)
	w = decideTestWork(t, s, admin, w, orchestration.WorkItemOperation{Kind: "follow", SourceID: "room", ThreadKey: room.ID + ":room"})
	w = decideTestWork(t, s, admin, w, orchestration.WorkItemOperation{Kind: "execute", ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID})
	task, e := s.GetTaskState(ctx, w.ActiveTaskID)
	if e != nil || task.InputRevision != 1 {
		t.Fatal(task, e)
	}
	sendAutomationDiscussion(t, s, admin, room, "The followed conversation supplied another fact.")
	task, e = s.GetTaskState(ctx, task.ID)
	if e != nil || task.InputRevision != 2 {
		t.Fatal("manual finite task did not inherit the matter's explicit routes", task, e)
	}
	w, _ = s.GetWorkItem(admin, w.ID)
	w = decideTestWork(t, s, admin, w, orchestration.WorkItemOperation{Kind: "unfollow", SourceID: "room", ThreadKey: room.ID + ":room"})
	sendAutomationDiscussion(t, s, admin, room, "This context is no longer followed.")
	task, e = s.GetTaskState(ctx, task.ID)
	if e != nil || task.InputRevision != 2 {
		t.Fatal("unfollow kept delivering to the active task", task, e)
	}
	w = decideTestWork(t, s, admin, w, orchestration.WorkItemOperation{Kind: "follow", SourceID: "room", ThreadKey: room.ID + ":room"})
	sendAutomationDiscussion(t, s, admin, room, "New context after explicit re-enable.")
	task, e = s.GetTaskState(ctx, task.ID)
	if e != nil || task.InputRevision != 3 {
		t.Fatal("re-enabled route did not deliver new context", task, e)
	}
	if task.TurnsUsed != 0 || task.ModelRequestsUsed != 0 {
		t.Fatal("context delivery executed work", task)
	}
}

func TestWorkItemCanUseCanonicalTaskCommandsWithoutChangingAuthorship(t *testing.T) {
	s, admin, _, room, writer, _ := taskFixture(t)
	original := startTask(t, s, admin, room, writer)
	records := admissionRecords(t, s, room.ID)
	id := "task:" + records[len(records)-1].Trigger.EventID
	before, e := s.queries().GetAutomationEvent(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	projection, e := s.GetAutomationEvent(admin, id)
	if e != nil || !strings.Contains(projection.Content, "task.start") || !strings.Contains(projection.Content, original.Goal) {
		t.Fatal("canonical command references were not readable", projection, e)
	}
	w := createTestWork(t, s, admin, room, id)
	w = decideTestWork(t, s, admin, w, orchestration.WorkItemOperation{Kind: "execute", ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID})
	inputs, e := s.TaskInputsPage(admin, w.ActiveTaskID, 0, nil, 20)
	u, _ := UserFromContext(admin)
	if e != nil || len(inputs.Items) != 1 || inputs.Items[0].AuthorKind != "user" || inputs.Items[0].AuthorUserID != u.ID || inputs.Items[0].ExternalActorID != "" || !strings.Contains(inputs.Items[0].Content, original.Goal) {
		t.Fatal("Camp identity became external or original context was lost", inputs, e)
	}
	after, e := s.queries().GetAutomationEvent(ctx, id)
	if e != nil || after.Value != before.Value {
		t.Fatal("reading/executing rewrote the immutable command fact", after, e)
	}
}
