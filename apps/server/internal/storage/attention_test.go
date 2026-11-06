package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func attentionFor(t *testing.T, s *Store, actor context.Context, options ...AttentionOptions) AttentionPage {
	t.Helper()
	var option AttentionOptions
	if len(options) > 0 {
		option = options[0]
	}
	page, err := s.Attention(actor, option)
	if err != nil {
		t.Fatal(err)
	}
	return page
}

func TestAttentionMigrationPreservesExistingMattersAndUnreadNotices(t *testing.T) {
	s := unmigratedStore(t)
	if err := s.applyMigrations(ctx, migrationSourcesThrough(t, 30)); err != nil {
		t.Fatal(err)
	}
	owner, _ := teamUsers(t, s)
	room, err := s.CreateRoomWithResource(owner, Room{Name: "Existing attention Room"}, Resource{Name: "Existing files", CurrentCommit: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	w := createTestWork(t, s, owner, room)
	w = decideTestWork(t, s, owner, w, orchestration.WorkItemOperation{Kind: "request_judgment", Question: "Keep this original question."})
	var original string
	if err = s.db.QueryRow("SELECT value FROM work_items WHERE id=?", w.ID).Scan(&original); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err = s.migrate(ctx); err != nil {
			t.Fatal(err)
		}
		page := attentionFor(t, s, owner)
		if page.Total != 1 || page.Items[0].ID != "work:"+w.ID {
			t.Fatal(page)
		}
		var retained string
		if err = s.db.QueryRow("SELECT value FROM work_items WHERE id=?", w.ID).Scan(&retained); err != nil || retained != original {
			t.Fatal("projection migration rewrote the matter", retained, err)
		}
		notices, err := s.ListInbox(owner)
		if err != nil || len(notices) != 1 || notices[0].ReadAt != "" {
			t.Fatal("migration lost the original unread notification", notices, err)
		}
	}
}

func TestAttentionWorkReadIsNotResolutionAndReadsHaveNoEffects(t *testing.T) {
	s, owner, teammate, room, _, _ := taskFixture(t)
	w := createTestWork(t, s, owner, room)
	w = decideTestWork(t, s, owner, w, orchestration.WorkItemOperation{Kind: "request_judgment", Question: "Which launch date?"})
	first := attentionFor(t, s, owner)
	if first.Total != 1 || first.Counts.Judgment != 1 || len(first.Items) != 1 || first.Items[0].ID != "work:"+w.ID || first.Items[0].Target.WorkItemID != w.ID || first.Items[0].Target.App != "work" {
		t.Fatal(first)
	}
	if other := attentionFor(t, s, teammate); other.Total != 0 {
		t.Fatal("team visibility assigned someone else's judgment", other)
	}
	notices, err := s.ListInbox(owner)
	if err != nil || len(notices) != 1 {
		t.Fatal(notices, err)
	}
	if err = s.ReadInboxItem(owner, notices[0].ID); err != nil {
		t.Fatal(err)
	}
	var events int
	if err = s.db.QueryRow("SELECT count(*) FROM workspace_events").Scan(&events); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if page := attentionFor(t, s, owner); page.Total != 1 || page.Items[0].ID != first.Items[0].ID {
			t.Fatal("reading resolved the matter", page)
		}
	}
	current, err := s.GetWorkItem(owner, w.ID)
	if err != nil || current.Revision != w.Revision || current.Usage != w.Usage {
		t.Fatal("attention read mutated authorization", current, err)
	}
	assertScalar(t, s, "SELECT count(*) FROM workspace_events", events)
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
	assertScalar(t, s, "SELECT count(*) FROM automation_evaluations", 0)
	w = decideTestWork(t, s, owner, w, orchestration.WorkItemOperation{Kind: "answer", Content: "Use Tuesday."})
	if page := attentionFor(t, s, owner); page.Total != 0 {
		t.Fatal("answered judgment remained actionable", page)
	}
	w = decideTestWork(t, s, owner, w, orchestration.WorkItemOperation{Kind: "wait", WaitCondition: "Wait for customer confirmation."})
	if page := attentionFor(t, s, owner); page.Total != 0 {
		t.Fatal("ordinary waiting demanded human attention", page)
	}
	w = decideTestWork(t, s, owner, w, orchestration.WorkItemOperation{Kind: "request_judgment", Question: "Confirm the audience."})
	if page := attentionFor(t, s, owner); page.Total != 1 || page.Items[0].ID != first.Items[0].ID {
		t.Fatal("new question lost its stable matter identity", page)
	}
	decideTestWork(t, s, owner, w, orchestration.WorkItemOperation{Kind: "stop"})
	if page := attentionFor(t, s, owner); page.Total != 0 {
		t.Fatal("stopped matter stayed pending", page)
	}
}

func TestAttentionReviewFollowsAssignedPersonAndActualReviewLifecycle(t *testing.T) {
	f := newReviewFixture(t)
	r := requestReview(t, f)
	if page := attentionFor(t, f.store, f.owner); page.Total != 0 {
		t.Fatal("administrator was assigned a teammate's review", page)
	}
	page := attentionFor(t, f.store, f.reviewer)
	if page.Total != 1 || page.Counts.Review != 1 || page.Items[0].Target.ReviewID != r.Review.ID || page.Items[0].Reason != "review_requested" {
		t.Fatal(page)
	}
	r = reviewAction(t, f, f.reviewer, r, "request_changes", "Clarify the dates.", "")
	if page = attentionFor(t, f.store, f.reviewer); page.Total != 0 {
		t.Fatal("completed reviewer decision stayed pending", page)
	}
	page = attentionFor(t, f.store, f.owner)
	if page.Counts.Judgment != 1 || page.Items[0].Reason != "review_changes_requested" {
		t.Fatal(page)
	}
	// The unread notification is activity, even after its business state changed.
	notices, err := f.store.ListInbox(f.reviewer)
	if err != nil || len(notices) == 0 || notices[0].ReadAt != "" {
		t.Fatal(notices, err)
	}
	publishReviewVersion(t, f, strings.Repeat("b", 40))
	page = attentionFor(t, f.store, f.owner)
	if page.Counts.Exception != 1 || page.Items[0].Reason != "review_outdated" {
		t.Fatal(page)
	}
	r = reviewAction(t, f, f.owner, r, "resubmit", "Dates corrected.", strings.Repeat("b", 40))
	if page = attentionFor(t, f.store, f.reviewer); page.Counts.Review != 1 {
		t.Fatal(page)
	}
	r = reviewAction(t, f, f.reviewer, r, "approve", "Ready.", "")
	page = attentionFor(t, f.store, f.owner)
	if page.Counts.Judgment != 1 || page.Items[0].Reason != "review_ready_delivery" {
		t.Fatal(page)
	}
	reviewer, _ := UserFromContext(f.reviewer)
	disabled := false
	if _, err = f.store.UpdateUserAccess(f.owner, reviewer.ID, nil, &disabled); err != nil {
		t.Fatal(err)
	}
	// Current approved bytes retain the existing explicit delivery action; an
	// account disable does not invent a second review or revoke recorded approval.
	page = attentionFor(t, f.store, f.owner)
	if page.Counts.Judgment != 1 || page.Items[0].Reason != "review_ready_delivery" {
		t.Fatal(page)
	}
	reviewAction(t, f, f.owner, r, "deliver", "Deliver the approved bytes.", "")
	if page = attentionFor(t, f.store, f.owner); page.Total != 0 {
		t.Fatal("delivered review stayed pending", page)
	}
}

func TestAttentionUnavailableReviewerRequiresRequesterRecoveryAndCancelResolves(t *testing.T) {
	f := newReviewFixture(t)
	r := requestReview(t, f)
	reviewer, _ := UserFromContext(f.reviewer)
	disabled := false
	if _, err := f.store.UpdateUserAccess(f.owner, reviewer.ID, nil, &disabled); err != nil {
		t.Fatal(err)
	}
	page := attentionFor(t, f.store, f.owner)
	if page.Total != 1 || page.Counts.Exception != 1 || page.Items[0].Reason != "reviewer_unavailable" {
		t.Fatal(page)
	}
	_, err := f.store.Attention(f.reviewer, AttentionOptions{})
	requireCode(t, err, "unauthenticated")
	reviewAction(t, f, f.owner, r, "cancel", "Replace the review.", "")
	if page = attentionFor(t, f.store, f.owner); page.Total != 0 {
		t.Fatal(page)
	}
}

func TestAttentionTaskQuestionsDeduplicateOntoWorkAndLeaveOnAnswer(t *testing.T) {
	s, owner, teammate, room, writer, _ := taskFixture(t)
	legacy := startTask(t, s, owner, room, writer)
	if page := attentionFor(t, s, owner); page.Total != 0 {
		t.Fatal("queued work was treated as a human interruption", page)
	}
	finishTaskTestRound(t, s, legacy, strings.Repeat("b", 40), CoordinationIntent{CallID: "legacy-question", Kind: "ask_person", Question: "Choose the document title."})
	w := createIntentWork(t, s, owner, room, writer, true)
	linked, err := s.GetTaskState(owner, w.ActiveTaskID)
	if err != nil {
		t.Fatal(err)
	}
	enableTaskInputs(t, s, linked.Participants[0].RunnerID)
	finishTaskTestRound(t, s, linked, strings.Repeat("c", 40), CoordinationIntent{CallID: "work-question", Kind: "ask_person", Question: "Which audience should the matter address?"})
	linked, _ = s.GetTaskState(owner, linked.ID)
	page := attentionFor(t, s, owner)
	if page.Total != 2 || page.Counts.Judgment != 2 {
		t.Fatal(page)
	}
	seen := map[string]AttentionItem{}
	for _, item := range page.Items {
		seen[item.ID] = item
	}
	if seen["task:"+legacy.ID].Target.TaskID != legacy.ID || seen["task:"+legacy.ID].Target.App != "workspace" || seen["work:"+w.ID].Summary != linked.Question.Text || seen["work:"+w.ID].Target.WorkItemID != w.ID {
		t.Fatal(seen)
	}
	if _, duplicate := seen["task:"+linked.ID]; duplicate {
		t.Fatal("linked collaboration question appeared twice")
	}
	if other := attentionFor(t, s, teammate); other.Total != 0 {
		t.Fatal("answer permission assigned another person's task", other)
	}
	w, _ = s.GetWorkItem(owner, w.ID)
	decideTestWork(t, s, owner, w, orchestration.WorkItemOperation{Kind: "answer", TaskID: linked.ID, TaskRevision: linked.Revision, QuestionID: linked.Question.ID, Content: "Use the customer audience."})
	if page = attentionFor(t, s, owner); page.Total != 1 || page.Items[0].ID != "task:"+legacy.ID {
		t.Fatal("answered linked question remained pending", page)
	}
	legacy, _ = s.GetTaskState(owner, legacy.ID)
	if _, err = s.OperateTask(owner, legacy.ID, "cancel", TaskOperation{RequestID: newID(), Revision: legacy.Revision}); err != nil {
		t.Fatal(err)
	}
	if page = attentionFor(t, s, owner); page.Total != 0 {
		t.Fatal(page)
	}
}

func TestAttentionDeliveryUsesRecoveryOwnerAndConfirmedReceiptResolvesWithdrawal(t *testing.T) {
	s, _, w, task, deliveries := workActionFixture(t)
	requester := WithUser(ctx, User{ID: task.RequestedByUserID})
	if page := attentionFor(t, s, requester); page.Total != 0 {
		t.Fatal("ordinary pending dispatch required human recovery", page)
	}
	for _, status := range []string{"blocked", "failed"} {
		if err := s.transaction(ctx, func(tx *sql.Tx) error {
			q := sqlgen.New(tx)
			d := deliveries[0]
			d.Status, d.Reason = status, "Check the comment connection."
			d.Body, d.CredentialRef = "private-comment-original", "private-credential-reference"
			return saveAutomationDelivery(ctx, q, d)
		}); err != nil {
			t.Fatal(err)
		}
		page := attentionFor(t, s, requester)
		if page.Total != 1 || page.Counts.Exception != 1 || page.Items[0].Reason != "delivery_"+status {
			t.Fatal(page)
		}
		raw, _ := json.Marshal(page)
		if strings.Contains(string(raw), "private-comment-original") || strings.Contains(string(raw), "private-credential-reference") || strings.Contains(string(raw), "credentialRef") {
			t.Fatal("recovery projection exposed private dispatch context", string(raw))
		}
		if admin := attentionFor(t, s, WithUser(ctx, User{ID: w.OwnerUserID})); w.OwnerUserID != task.RequestedByUserID && admin.Total != 0 {
			t.Fatal("administrator capability became delivery assignment", admin)
		}
	}
	if err := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		d := deliveries[0]
		d.Status, d.Withdrawn = "withdrawn", true
		d.Attempts = []orchestration.DeliveryAttempt{{Revision: d.Revision, ActorID: 9, Outcome: "unknown"}}
		d.Reason = "The earlier comment outcome is unknown."
		return saveAutomationDelivery(ctx, q, d)
	}); err != nil {
		t.Fatal(err)
	}
	page := attentionFor(t, s, requester)
	if page.Total != 1 || page.Counts.Exception != 1 || page.Items[0].ID != "delivery:"+deliveries[0].ID || page.Items[0].Reason != "delivery_unknown" || page.Items[0].Target.WorkItemID != w.ID || page.Items[0].Target.TaskID != task.ID || page.Items[0].Target.App != "work" {
		t.Fatal(page)
	}
	row, err := s.queries().GetAutomationDelivery(ctx, deliveries[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	delivery, err := s.OperateAutomationDelivery(requester, row.ID, "reconcile", AutomationDeliveryInput{RequestID: newID(), Revision: row.Revision})
	if err != nil || delivery.Status != "reconcile" {
		t.Fatal(delivery, err)
	}
	if page = attentionFor(t, s, requester); page.Total != 0 {
		t.Fatal("read-only reconciliation in progress demanded another action", page)
	}
	claim, err := s.ClaimAutomationDelivery(ctx)
	if err != nil || claim == nil || !claim.Reconcile {
		t.Fatal(claim, err)
	}
	if err = s.CompleteAutomationDelivery(ctx, claim.ID, orchestration.DeliveryResult{ClaimRevision: claim.Revision, Status: "sent", CommentID: 81, URL: "https://github.com/team/repo/issues/3#issuecomment-81"}); err != nil {
		t.Fatal(err)
	}
	if page = attentionFor(t, s, requester); page.Total != 0 {
		t.Fatal("confirmed historical receipt remained an unresolved exception", page)
	}
}

func TestAttentionTaskRecoveryLeavesHistoricalRunFailureInActivity(t *testing.T) {
	s, owner, _, room, writer, _ := taskFixture(t)
	task := startTask(t, s, owner, room, writer)
	finishTaskTestRound(t, s, task, strings.Repeat("b", 40), CoordinationIntent{CallID: "ask", Kind: "ask_person", Question: "An earlier question"})
	task, _ = s.GetTaskState(owner, task.ID)
	task, err := s.OperateTask(owner, task.ID, "answers", TaskOperation{RequestID: newID(), Revision: task.Revision, QuestionID: task.Question.ID, Content: "The earlier answer"})
	if err != nil {
		t.Fatal(err)
	}
	run, _ := s.GetRun(owner, task.ActiveRunID)
	_, attempt := prepareTaskTestAttempt(t, s, run)
	if _, err = s.InterruptAttempt(ctx, attempt.ID, "The connection ended before completion."); err != nil {
		t.Fatal(err)
	}
	w := createTestWork(t, s, owner, room)
	decideTestWork(t, s, owner, w, orchestration.WorkItemOperation{Kind: "request_judgment", Question: "Choose the audience."})
	resource, _ := s.GetResource(owner, room.ResourceID)
	if _, err = s.CreateReview(owner, room.ID, CreateReviewInput{RequestID: newID(), Title: "Review the proposal", ResourceID: resource.ID, Path: "proposal.md", Commit: resource.CurrentCommit, ReviewerUserID: actorID(owner)}); err != nil {
		t.Fatal(err)
	}
	page := attentionFor(t, s, owner)
	if page.Total != 3 || page.Counts != (AttentionCounts{Exception: 1, Judgment: 1, Review: 1}) || page.Items[0].ID != "task:"+task.ID || page.Items[0].Summary == "An earlier question" || page.Items[1].Kind != "judgment" || page.Items[2].Kind != "review" {
		t.Fatal(page)
	}
	task, _ = s.GetTaskState(owner, task.ID)
	if _, err = s.OperateTask(owner, task.ID, "continue", TaskOperation{RequestID: newID(), Revision: task.Revision, Content: "Continue the original goal.", AddTurns: 1, AddModelRequests: 2}); err != nil {
		t.Fatal(err)
	}
	page = attentionFor(t, s, owner)
	if page.Total != 2 || page.Counts.Exception != 0 {
		t.Fatal("historical interrupted Run became an unresolved incident", page)
	}
}

func TestAttentionParentStopRetiresKnownLateFailureButKeepsUnknownDelivery(t *testing.T) {
	s, owner, w, task, deliveries := workActionFixture(t)
	requester := WithUser(ctx, User{ID: task.RequestedByUserID})
	claim, err := s.ClaimAutomationDelivery(ctx)
	if err != nil || claim == nil {
		t.Fatal(claim, err)
	}
	var unknownID string
	for _, delivery := range deliveries {
		if delivery.ID == claim.ID {
			continue
		}
		unknownID = delivery.ID
		delivery.Status = "unknown"
		delivery.Attempts = []orchestration.DeliveryAttempt{{Revision: delivery.Revision, ActorID: 9, Outcome: "unknown"}}
		if err = s.transaction(ctx, func(tx *sql.Tx) error { return saveAutomationDelivery(ctx, sqlgen.New(tx), delivery) }); err != nil {
			t.Fatal(err)
		}
	}
	w, _ = s.GetWorkItem(owner, w.ID)
	decideTestWork(t, s, owner, w, orchestration.WorkItemOperation{Kind: "stop"})
	// The send was already claimed when Stop committed. A late known failure
	// remains audit history, while the separate unknown outcome still needs proof.
	if err = s.CompleteAutomationDelivery(ctx, claim.ID, orchestration.DeliveryResult{ClaimRevision: claim.Revision, Status: "failed", Reason: "The send failed without posting a comment."}); err != nil {
		t.Fatal(err)
	}
	page := attentionFor(t, s, requester)
	if page.Total != 1 || page.Items[0].ID != "delivery:"+unknownID || page.Items[0].Reason != "delivery_unknown" {
		t.Fatal("Stop left a known failure actionable or hid the uncertain effect", page)
	}
}

func TestAttentionAnsweredOrCancelledQuestionRetiresKnownUnsentComments(t *testing.T) {
	for _, action := range []string{"answers", "cancel"} {
		t.Run(action, func(t *testing.T) {
			s, task, _ := automationDeliveryFixture(t)
			requester := WithUser(ctx, User{ID: task.RequestedByUserID})
			enableTaskInputs(t, s, task.Participants[0].RunnerID)
			finishTaskTestRound(t, s, task, strings.Repeat("b", 40), CoordinationIntent{CallID: "ask", Kind: "ask_person", Question: "Choose the title."})
			task, _ = s.GetTaskState(requester, task.ID)
			deliveries, err := s.TaskAutomationDeliveries(requester, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			var unknownID string
			for _, delivery := range deliveries {
				if delivery.QuestionID != task.Question.ID {
					continue
				}
				delivery.Status = "blocked"
				if unknownID == "" {
					unknownID, delivery.Status = delivery.ID, "unknown"
					delivery.Attempts = []orchestration.DeliveryAttempt{{Revision: delivery.Revision, ActorID: 9, Outcome: "unknown"}}
				}
				if err = s.transaction(ctx, func(tx *sql.Tx) error { return saveAutomationDelivery(ctx, sqlgen.New(tx), delivery) }); err != nil {
					t.Fatal(err)
				}
			}
			if unknownID == "" {
				t.Fatal("missing queued question delivery fixture")
			}
			if _, err = s.OperateTask(requester, task.ID, action, TaskOperation{RequestID: newID(), Revision: task.Revision, QuestionID: task.Question.ID, Content: "Use the reviewed title."}); err != nil {
				t.Fatal(err)
			}
			page := attentionFor(t, s, requester)
			if page.Total != 1 || page.Items[0].ID != "delivery:"+unknownID {
				t.Fatal("resolved question kept a known-unsent failure or hid uncertainty", page)
			}
		})
	}
}

func TestAttentionPaginationCountsFilteringAndCurrentAuthority(t *testing.T) {
	s, owner, teammate, room, _, _ := taskFixture(t)
	other, err := s.CreateRoomWithResource(owner, Room{Name: "Other attention Room"}, Resource{Name: "Other files", CurrentCommit: strings.Repeat("d", 40)})
	if err != nil {
		t.Fatal(err)
	}
	for range 23 {
		w := createTestWork(t, s, owner, room)
		decideTestWork(t, s, owner, w, orchestration.WorkItemOperation{Kind: "request_judgment", Question: strings.Repeat("先", 2000)})
	}
	w := createTestWork(t, s, owner, other)
	w = decideTestWork(t, s, owner, w, orchestration.WorkItemOperation{Kind: "request_judgment", Question: "Other Room question"})
	first := attentionFor(t, s, owner, AttentionOptions{RoomID: room.ID, Limit: 20})
	if first.Total != 23 || first.Counts.Judgment != 23 || len(first.Items) != 20 || !first.HasMore || !first.Items[0].TextLimited {
		t.Fatal(first)
	}
	seen := map[string]bool{}
	for _, item := range first.Items {
		seen[item.ID] = true
		if item.RoomID != room.ID || len(item.Summary) > 2048 {
			t.Fatal(item)
		}
	}
	last := attentionFor(t, s, owner, AttentionOptions{RoomID: room.ID, Cursor: first.NextCursor, Limit: 20})
	if len(last.Items) != 3 || last.Total != 23 || last.HasMore || last.NextCursor != "" {
		t.Fatal(last)
	}
	for _, item := range last.Items {
		if seen[item.ID] {
			t.Fatal("pagination repeated a stable identity", item.ID)
		}
	}
	exceptions := attentionFor(t, s, owner, AttentionOptions{RoomID: room.ID, Kind: "exception"})
	if len(exceptions.Items) != 0 || exceptions.Total != 23 || exceptions.Counts.Judgment != 23 {
		t.Fatal("category filtering changed total responsibility", exceptions)
	}
	for _, option := range []AttentionOptions{
		{Cursor: "invalid"}, {RoomID: other.ID, Cursor: first.NextCursor}, {RoomID: room.ID, Kind: "judgment", Cursor: first.NextCursor}, {Limit: 21}, {Limit: -1}, {Kind: "unknown"},
	} {
		_, err = s.Attention(owner, option)
		requireCode(t, err, "invalid_page")
	}
	_, err = s.Attention(teammate, AttentionOptions{RoomID: room.ID, Cursor: first.NextCursor})
	requireCode(t, err, "invalid_page")
	raw, _ := base64.RawURLEncoding.DecodeString(first.NextCursor)
	var cursor map[string]any
	if err = json.Unmarshal(raw, &cursor); err != nil {
		t.Fatal(err)
	}
	cursor["extra"] = true
	raw, _ = json.Marshal(cursor)
	_, err = s.Attention(owner, AttentionOptions{RoomID: room.ID, Cursor: base64.RawURLEncoding.EncodeToString(raw)})
	requireCode(t, err, "invalid_page")
	_, err = s.Attention(ctx, AttentionOptions{})
	requireCode(t, err, "unauthenticated")
	_, err = s.Attention(owner, AttentionOptions{RoomID: "missing"})
	requireCode(t, err, "not_found")
	if _, err = s.UpdateUserAccess(owner, actorID(teammate), nil, new(false)); err != nil {
		t.Fatal(err)
	}
	_, err = s.Attention(teammate, AttentionOptions{})
	requireCode(t, err, "unauthenticated")
	if global := attentionFor(t, s, owner); global.Total != 24 {
		t.Fatal(global)
	}
}
