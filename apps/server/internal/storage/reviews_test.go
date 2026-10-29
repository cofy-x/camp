package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

type reviewFixture struct {
	store           *Store
	room            Room
	owner, reviewer context.Context
	input           CreateReviewInput
}

func newReviewFixture(t *testing.T) reviewFixture {
	t.Helper()
	s, room, _ := roomFixture(t)
	owner, reviewer := teamUsers(t, s)
	person, _ := UserFromContext(reviewer)
	resource, err := s.GetResource(ctx, room.ResourceID)
	if err != nil {
		t.Fatal(err)
	}
	return reviewFixture{s, room, owner, reviewer, CreateReviewInput{RequestID: "request", Title: "Launch proposal", ResourceID: resource.ID, Path: "proposal.md", Commit: resource.CurrentCommit, ReviewerUserID: person.ID}}
}
func requestReview(t *testing.T, f reviewFixture) ReviewResult {
	t.Helper()
	r, err := f.store.CreateReview(f.owner, f.room.ID, f.input)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func reviewAction(t *testing.T, f reviewFixture, actor context.Context, r ReviewResult, action, comment, commit string) ReviewResult {
	t.Helper()
	out, err := f.store.ActOnReview(actor, r.Review.ID, ReviewActionInput{RequestID: fmt.Sprintf("%s-%d", action, r.Review.Revision), Revision: r.Review.Revision, Action: action, Comment: comment, Commit: commit})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func publishReviewVersion(t *testing.T, f reviewFixture, commit string) {
	t.Helper()
	current, err := f.store.GetResource(f.owner, f.room.ResourceID)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.store.Publish(f.owner, PublishInput{ResourceID: current.ID, ExpectedCommit: current.CurrentCommit, Commit: commit, Kind: "edit", Path: "proposal.md", RequestID: "save-" + commit, RequestHash: strings.Repeat("a", 64)})
	if err != nil {
		t.Fatal(err)
	}
}
func TestReviewRevisionDecisionDeliveryAndThread(t *testing.T) {
	f := newReviewFixture(t)
	first := requestReview(t, f)
	r := first.Review
	if r.Status != "in_review" || r.Revision != 1 || r.Outdated || r.ThreadRootID == "" || len(first.Events) != 1 {
		t.Fatalf("invalid admission: %+v", first)
	}
	_, err := f.store.ActOnReview(f.owner, r.ID, ReviewActionInput{RequestID: "wrong-reviewer", Revision: 1, Action: "approve"})
	requireCode(t, err, "forbidden")
	_, err = f.store.ActOnReview(f.reviewer, r.ID, ReviewActionInput{RequestID: "blank-reason", Revision: 1, Action: "request_changes"})
	requireCode(t, err, "invalid_review")
	changes := reviewAction(t, f, f.reviewer, first, "request_changes", "Please clarify the audience.", "")
	if changes.Review.Status != "changes_requested" {
		t.Fatal(changes)
	}
	updated := strings.Repeat("b", 40)
	publishReviewVersion(t, f, updated)
	stale, err := f.store.GetReview(f.owner, r.ID)
	if err != nil || !stale.Review.Outdated {
		t.Fatalf("new shared files did not invalidate decision: %+v %v", stale, err)
	}
	resubmitted := reviewAction(t, f, f.owner, changes, "resubmit", "The audience is now explicit.", updated)
	approved := reviewAction(t, f, f.reviewer, resubmitted, "approve", "Ready for the team.", "")
	newer := strings.Repeat("c", 40)
	publishReviewVersion(t, f, newer)
	_, err = f.store.ActOnReview(f.owner, r.ID, ReviewActionInput{RequestID: "deliver-stale", Revision: approved.Review.Revision, Action: "deliver"})
	requireCode(t, err, "revision_conflict")
	resubmitted = reviewAction(t, f, f.owner, approved, "resubmit", "Updated the schedule.", newer)
	approved = reviewAction(t, f, f.reviewer, resubmitted, "approve", "Reviewed the new schedule.", "")
	delivered := reviewAction(t, f, f.owner, approved, "deliver", "For the launch team.", "")
	publishReviewVersion(t, f, strings.Repeat("d", 40))
	retained, err := f.store.GetReview(f.owner, r.ID)
	if err != nil || retained.Review.Status != "delivered" || retained.Review.Outdated || retained.Review.DeliveredCommit != newer {
		t.Fatalf("delivery changed after publication: %+v %v", retained, err)
	}
	_, err = f.store.ActOnReview(f.owner, r.ID, ReviewActionInput{RequestID: "cancel-delivery", Revision: delivered.Review.Revision, Action: "cancel"})
	requireCode(t, err, "review_finished")
	messages, err := f.store.ListMessages(f.owner, f.room.ID, r.ThreadRootID, 0)
	if err != nil || len(messages) != len(retained.Events) {
		t.Fatalf("thread records: %d %d %v", len(messages), len(retained.Events), err)
	}
	for i, m := range messages {
		if m.ReviewID != r.ID || m.AuthorKind != "system" || m.AuthorUserID == "" || len(m.References) != 1 || m.References[0].Commit != retained.Events[i].Commit {
			t.Fatalf("unattributed or unpinned review record: %+v", m)
		}
		if i > 0 && m.ThreadRootID != r.ThreadRootID {
			t.Fatal("transition created a separate thread")
		}
	}
	if messages[0].References[0].Commit != f.input.Commit {
		t.Fatal("root reference was rewritten")
	}
	runs, err := f.store.ListRoomRuns(f.owner, f.room.ID)
	if err != nil || len(runs) != 0 {
		t.Fatal("review transition started an Agent", runs, err)
	}
	notifications, err := f.store.ListInbox(f.reviewer)
	if err != nil || len(notifications) != 4 {
		t.Fatalf("reviewer notifications: %+v %v", notifications, err)
	}
	for _, n := range notifications {
		if n.ReviewID != r.ID || n.RoomID != r.RoomID || n.ReviewTitle != r.Title || n.RoomName != f.room.Name {
			t.Fatalf("notification is not navigable: %+v", n)
		}
	}
}
func TestReviewRetriesAreActorScopedAndCASIsAtomic(t *testing.T) {
	f := newReviewFixture(t)
	first := requestReview(t, f)
	conflicting := f.input
	conflicting.Title = "Different title"
	_, err := f.store.CreateReview(f.owner, f.room.ID, conflicting)
	requireCode(t, err, "request_conflict")
	other := f.input
	owner, _ := UserFromContext(f.owner)
	other.ReviewerUserID = owner.ID
	second, err := f.store.CreateReview(f.reviewer, f.room.ID, other)
	if err != nil || second.Review.ID == first.Review.ID {
		t.Fatalf("request identity not scoped to actor: %+v %v", second, err)
	}
	var wg sync.WaitGroup
	results := make(chan ReviewResult, 2)
	failures := make(chan error, 2)
	for range 2 {
		wg.Go(func() {
			result, e := f.store.ActOnReview(f.reviewer, first.Review.ID, ReviewActionInput{RequestID: "same-approval", Revision: 1, Action: "approve"})
			results <- result
			failures <- e
		})
	}
	wg.Wait()
	close(results)
	close(failures)
	for e := range failures {
		if e != nil {
			t.Fatal(e)
		}
	}
	replayed := 0
	for result := range results {
		if result.Replayed {
			replayed++
		}
		if result.Review.Revision != 2 || len(result.Events) != 2 {
			t.Fatal(result)
		}
	}
	if replayed != 1 {
		t.Fatal("same action executed twice")
	}
	_, err = f.store.ActOnReview(f.reviewer, first.Review.ID, ReviewActionInput{RequestID: "different-approval", Revision: 1, Action: "request_changes", Comment: "New decision"})
	requireCode(t, err, "revision_conflict")
	publishReviewVersion(t, f, strings.Repeat("b", 40))
	replay, err := f.store.CreateReview(f.owner, f.room.ID, f.input)
	if err != nil || !replay.Replayed || replay.Review.Revision != 1 || len(replay.Events) != 1 {
		t.Fatalf("creation receipt not retained: %+v %v", replay, err)
	}
	assertScalar(t, f.store, "SELECT count(*) FROM reviews", 2)
	assertScalar(t, f.store, "SELECT count(*) FROM review_events", 3)
	assertScalar(t, f.store, "SELECT count(*) FROM inbox", 3)
}
func TestReviewTransitionFailureRollsBackAllEffects(t *testing.T) {
	f := newReviewFixture(t)
	r := requestReview(t, f)
	if _, err := f.store.db.Exec(`CREATE TRIGGER fail_review_receipt BEFORE INSERT ON review_requests BEGIN SELECT RAISE(FAIL,'receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	input := ReviewActionInput{RequestID: "approval", Revision: 1, Action: "approve"}
	if _, err := f.store.ActOnReview(f.reviewer, r.Review.ID, input); err == nil {
		t.Fatal("failed receipt reported success")
	}
	current, err := f.store.GetReview(f.owner, r.Review.ID)
	if err != nil || current.Review.Status != "in_review" || current.Review.Revision != 1 || len(current.Events) != 1 {
		t.Fatalf("failed transaction retained partial transition: %+v %v", current, err)
	}
	assertScalar(t, f.store, "SELECT count(*) FROM messages", 1)
	assertScalar(t, f.store, "SELECT count(*) FROM inbox", 1)
	assertScalar(t, f.store, "SELECT count(*) FROM workspace_events WHERE type='review.updated'", 1)
	if _, err = f.store.db.Exec(`DROP TRIGGER fail_review_receipt`); err != nil {
		t.Fatal(err)
	}
	result, err := f.store.ActOnReview(f.reviewer, r.Review.ID, input)
	if err != nil || result.Replayed || result.Review.Status != "approved" {
		t.Fatal(result, err)
	}
}
func TestReviewDisabledActorReassignmentAndCancellation(t *testing.T) {
	f := newReviewFixture(t)
	r := requestReview(t, f)
	person, _ := UserFromContext(f.reviewer)
	disabled := false
	if _, err := f.store.UpdateUserAccess(f.owner, person.ID, nil, &disabled); err != nil {
		t.Fatal(err)
	}
	_, err := f.store.ActOnReview(f.reviewer, r.Review.ID, ReviewActionInput{RequestID: "late-approve", Revision: 1, Action: "approve"})
	requireCode(t, err, "forbidden")
	current, err := f.store.GetReview(f.owner, r.Review.ID)
	if err != nil || current.Review.ReviewerEnabled {
		t.Fatal("disabled reviewer hidden", current, err)
	}
	owner, _ := UserFromContext(f.owner)
	reassigned, err := f.store.ActOnReview(f.owner, r.Review.ID, ReviewActionInput{RequestID: "reassign", Revision: 1, Action: "reassign", ReviewerUserID: owner.ID})
	if err != nil || !reassigned.Review.ReviewerEnabled || reassigned.Review.ReviewerUserID != owner.ID {
		t.Fatal(reassigned, err)
	}
	approved := reviewAction(t, f, f.owner, reassigned, "approve", "Self-reviewed by the named requester.", "")
	cancelled := reviewAction(t, f, f.owner, approved, "cancel", "Superseded by a new proposal.", "")
	if cancelled.Review.Status != "cancelled" {
		t.Fatal(cancelled)
	}
	_, err = f.store.ActOnReview(f.owner, r.Review.ID, ReviewActionInput{RequestID: "reopen", Revision: cancelled.Review.Revision, Action: "resubmit", Commit: f.input.Commit})
	requireCode(t, err, "review_finished")
}
func TestReviewPinnedResourceAndUnpublishedInputs(t *testing.T) {
	f := newReviewFixture(t)
	input := f.input
	input.Commit = strings.Repeat("f", 40)
	_, err := f.store.CreateReview(f.owner, f.room.ID, input)
	requireCode(t, err, "invalid_reference")
	input = f.input
	input.Path = "../secret"
	_, err = f.store.CreateReview(f.owner, f.room.ID, input)
	requireCode(t, err, "invalid_reference")
	source, err := f.store.RegisterRoomUpload(f.owner, f.room.ID, "upload", strings.Repeat("f", 32), strings.Repeat("e", 40), "proposal.md", 3, strings.Repeat("d", 64))
	if err != nil {
		t.Fatal(err)
	}
	input = f.input
	input.ResourceID = source.Reference.ResourceID
	input.Commit = source.Reference.Commit
	_, err = f.store.CreateReview(f.owner, f.room.ID, input)
	requireCode(t, err, "invalid_review")
	r := requestReview(t, f)
	resource, err := f.store.CreateResource(f.owner, Resource{ID: "replacement", Name: "Replacement", CurrentCommit: strings.Repeat("f", 40)})
	if err != nil {
		t.Fatal(err)
	}
	room := f.room
	room.ResourceID = resource.ID
	if _, err = f.store.UpdateRoom(f.owner, room); err != nil {
		t.Fatal(err)
	}
	approved := reviewAction(t, f, f.reviewer, r, "approve", "Reviewed the original resource.", "")
	delivered := reviewAction(t, f, f.owner, approved, "deliver", "Pinned to the reviewed resource.", "")
	if delivered.Review.ResourceID != f.input.ResourceID {
		t.Fatal("room switch redirected delivery")
	}
}
func TestReviewSurvivesRestartAndDraftsRemainPrivate(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "reviews.sqlite")
	s, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	owner, reviewer := teamUsers(t, s)
	person, _ := UserFromContext(reviewer)
	room, err := s.SeedRoom(owner, Resource{ID: "files", Name: "Files", CurrentCommit: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	input := CreateReviewInput{RequestID: "request", Title: "Document", ResourceID: room.ResourceID, Path: "doc.md", Commit: strings.Repeat("a", 40), ReviewerUserID: person.ID}
	created, err := s.CreateReview(owner, room.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SaveDraft(owner, "review", "decision:"+created.Review.ID, json.RawMessage(`{"comment":"private"}`), 0)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	replay, err := s.CreateReview(owner, room.ID, input)
	if err != nil || !replay.Replayed || replay.Review.ID != created.Review.ID {
		t.Fatal(replay, err)
	}
	draft, err := s.GetDraft(reviewer, "review", "decision:"+created.Review.ID)
	if err != nil || string(draft.Value) != "null" {
		t.Fatal("review draft leaked", draft, err)
	}
	_, err = s.SaveDraft(owner, "review", "decision:"+created.Review.ID, json.RawMessage(`{"comment":"stale"}`), 0)
	requireCode(t, err, "revision_conflict")
	assertScalar(t, s, "SELECT count(*) FROM reviews", 1)
	assertScalar(t, s, "SELECT count(*) FROM inbox", 1)
}
func TestReviewMigrationPreservesPersonalStateAndNotifications(t *testing.T) {
	s := unmigratedStore(t)
	sources := migrationSourcesThrough(t, 6)
	if err := s.applyMigrations(ctx, sources); err != nil {
		t.Fatal(err)
	}
	// Seed historical rows directly: current services use the latest schema.
	if _, err := s.db.Exec(`INSERT INTO users(id,username,name,role,enabled,password_hash,created_at,updated_at) VALUES('owner','owner','Owner','admin',1,'hash','then','then'),('reviewer','reviewer','Reviewer','member',1,'hash','then','then')`); err != nil {
		t.Fatal(err)
	}
	owner := WithUser(ctx, User{ID: "owner", Role: "admin", Enabled: true})
	person := User{ID: "reviewer", Role: "member", Enabled: true}
	reviewer := WithUser(ctx, person)
	if _, err := s.db.Exec(`INSERT INTO resources(id,name,source_path,current_commit,environment,created_at,updated_at) VALUES('files','Files','','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','{}','now','now'); INSERT INTO rooms(id,name,description,resource_id,created_at,updated_at) VALUES('room','Room','','files','now','now'); INSERT INTO messages(id,room_id,seq,author_kind,content,created_at,updated_at) VALUES('message','room',1,'system','Keep this','now','now');`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO inbox(id,user_id,kind,room_id,message_id,created_at,read_at) VALUES('notification',?,'mention','room','message','then','read')`, person.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`INSERT INTO user_drafts(user_id,kind,draft_key,value,revision,updated_at) VALUES('owner','conversation','draft','{"content":"kept"}',1,'then')`); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	notifications, err := s.ListInbox(reviewer)
	if err != nil || len(notifications) != 1 || notifications[0].ReadAt != "read" || notifications[0].RoomName != "Room" || notifications[0].ReviewTitle != "" {
		t.Fatal("migration changed inbox", notifications, err)
	}
	draft, err := s.GetDraft(owner, "conversation", "draft")
	if err != nil || !strings.Contains(string(draft.Value), "kept") || draft.Revision != 1 {
		t.Fatal("migration changed draft", draft, err)
	}
	if _, err := s.SaveDraft(owner, "review", "decision", json.RawMessage(`{"comment":"new"}`), 0); err != nil {
		t.Fatal(err)
	}
	var violations int
	rows, err := s.db.Query(`PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		violations++
	}
	rows.Close()
	if violations > 0 {
		t.Fatal("migration broke foreign keys")
	}
}
func TestReviewDifferentConcurrentDecisionsHaveOneWinner(t *testing.T) {
	f := newReviewFixture(t)
	r := requestReview(t, f)
	results := make(chan error, 2)
	var wg sync.WaitGroup
	for _, action := range []string{"approve", "request_changes"} {
		wg.Go(func() {
			_, err := f.store.ActOnReview(f.reviewer, r.Review.ID, ReviewActionInput{RequestID: action, Revision: 1, Action: action, Comment: "Explicit decision"})
			results <- err
		})
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else {
			requireCode(t, err, "revision_conflict")
		}
	}
	if success != 1 {
		t.Fatal("two reviewers' decisions overwrote each other")
	}
	assertScalar(t, f.store, "SELECT count(*) FROM review_events", 2)
}
func TestReviewEventVersionsRemainKnownAfterPointerChanges(t *testing.T) {
	f := newReviewFixture(t)
	r := requestReview(t, f)
	// A restored resource database still retains review evidence independently of
	// publication history. Git objects are immutable and retained separately.
	_, err := f.store.queries().PublishResource(f.owner, sqlgen.PublishResourceParams{ID: r.Review.ResourceID, CurrentCommit: strings.Repeat("b", 40), CurrentCommit_2: r.Review.SubmittedCommit, UpdatedAt: now()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.ValidateResourceRevision(f.owner, r.Review.ResourceID, r.Review.SubmittedCommit); err != nil {
		t.Fatal("review evidence became inaccessible", err)
	}
}
