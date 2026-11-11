package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"runtime"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func requireMessageNotifications(t *testing.T, s *Store, room, thread string, after int64) []WorkspaceEvent {
	t.Helper()
	messages, err := s.ListMessages(ctx, room, thread, 0)
	if err != nil || len(messages) == 0 {
		t.Fatal(messages, err)
	}
	events, err := s.ListWorkspaceEvents(ctx, after)
	if err != nil {
		t.Fatal(err)
	}
	counts := map[string]int{}
	for _, event := range events {
		if event.Type == "message.created" && event.RoomID == room {
			counts[event.EntityID]++
		}
	}
	for _, message := range messages {
		if counts[message.ID] != 1 {
			t.Fatalf("message %s (%s) has %d notifications", message.ID, message.AuthorKind, counts[message.ID])
		}
	}
	return events
}

func TestRunCompletionMessageNotifiesOnceAndReplayAppendsNothing(t *testing.T) {
	s, room, _ := roomFixture(t)
	run := sendRoom(t, s, room, "run").Runs[0]
	boot, err := s.Bootstrap(ctx, "browser")
	if err != nil {
		t.Fatal(err)
	}
	run.Status = "succeeded"
	if _, err = s.FinishRun(ctx, run, "The completed result.", false); err != nil {
		t.Fatal(err)
	}
	reply, err := s.GetRunReply(ctx, run.ID)
	if err != nil || reply == nil {
		t.Fatal(reply, err)
	}
	events, err := s.ListWorkspaceEvents(ctx, boot.WorkspaceEventCursor)
	if err != nil || len(events) != 2 || events[0].Type != "message.created" || events[0].EntityID != reply.ID || events[1].Type != "run.finished" {
		t.Fatal(events, err)
	}
	if _, err = s.FinishRun(ctx, run, "A replay cannot replace the original.", false); err != nil {
		t.Fatal(err)
	}
	if tail, err := s.ListWorkspaceEvents(ctx, events[1].ID); err != nil || len(tail) != 0 {
		t.Fatal(tail, err)
	}
}

func TestTaskOriginInputsCompletionAndNoticesHaveCanonicalMessageNotifications(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	boot, err := s.Bootstrap(actor, "browser")
	if err != nil {
		t.Fatal(err)
	}
	task := startTask(t, s, actor, room, writer)
	task, err = s.OperateTask(actor, task.ID, "inputs", TaskOperation{RequestID: "input", Content: "Keep this input."})
	if err != nil {
		t.Fatal(err)
	}
	finishTaskTestRound(t, s, task, strings.Repeat("b", 40), CoordinationIntent{CallID: "question", Kind: "ask_person", Question: "Which title?"})
	requireMessageNotifications(t, s, room.ID, task.ThreadRootID, boot.WorkspaceEventCursor)
}

func TestReviewMessageNotificationsAreNotDuplicatedByTransitions(t *testing.T) {
	f := newReviewFixture(t)
	boot, err := f.store.Bootstrap(f.owner, "browser")
	if err != nil {
		t.Fatal(err)
	}
	created := requestReview(t, f)
	reviewAction(t, f, f.reviewer, created, "approve", "Reviewed the exact version.", "")
	requireMessageNotifications(t, f.store, f.room.ID, created.Review.ThreadRootID, boot.WorkspaceEventCursor)
}

func TestAutomationEventNotificationsCoverIntakeAndCompletedAssessment(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	assertNotification := func(after int64, id, status string) int64 {
		t.Helper()
		events, err := s.ListWorkspaceEvents(actor, after)
		if err != nil || !slices.ContainsFunc(events, func(e WorkspaceEvent) bool {
			return e.Type == "automation.event.updated" && e.RoomID == room.ID && e.EntityID == id
		}) {
			t.Fatal("missing canonical source event notification", events, err)
		}
		original, err := s.GetAutomationEvent(actor, id)
		if err != nil || original.Status != status {
			t.Fatal(original, err)
		}
		return events[len(events)-1].ID
	}
	boot, err := s.Bootstrap(actor, "browser")
	if err != nil {
		t.Fatal(err)
	}
	in := SendMessageInput{RequestID: "post-only", Content: "Original source remains private to its detail read.", PostOnly: true}
	posted, err := s.SendMessage(actor, room.ID, in, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	last := assertNotification(boot.WorkspaceEventCursor, "room:"+posted.Message.ID, "ignored")
	if _, err = s.SendMessage(actor, room.ID, in, roomBudget); err != nil {
		t.Fatal(err)
	}
	if events, err := s.ListWorkspaceEvents(actor, last); err != nil || len(events) != 0 {
		t.Fatal("message replay emitted another source notification", events, err)
	}
	discussion, err := s.SendMessage(actor, room.ID, SendMessageInput{RequestID: "discussion", Content: "No enabled automation policy."}, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	last = assertNotification(last, "room:"+discussion.Message.ID, "context")
	testRoomAutomationPolicy(t, s, actor, room, writer, "observe")
	sendAutomationDiscussion(t, s, actor, room, "A new observation for assessment.")
	candidate := claimAutomation(t, s)
	if len(candidate.Events) != 1 {
		t.Fatal(candidate)
	}
	last = assertNotification(last, candidate.Events[0].ID, "evaluating")
	if err = s.CompleteAutomationEvaluation(ctx, candidate.ID, orchestration.Assessment{Outcome: "ignore", Reason: "No action needed."}); err != nil {
		t.Fatal(err)
	}
	assertNotification(last, candidate.Events[0].ID, "observed")
}

func TestAutomationGitHubAcceptanceNotifiesItsSourceDecision(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	source := testGitHubSource(t, s, actor, room)
	_, err := s.SaveAutomationPolicy(actor, room.ID, source.ID, orchestration.PolicyInput{RequestID: newID(), Enabled: true, Mode: "auto", Rules: "Prepare the requested document.", AllowedActorIDs: []string{"7"}, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID, DailyEvaluations: 5, DailyTasks: 2})
	if err != nil {
		t.Fatal(err)
	}
	original := receiveTestGitHub(t, s, source, testGitHubPayload("Prepare a document", "Retain this original."))
	candidate := claimAutomation(t, s)
	boot, err := s.Bootstrap(actor, "browser")
	if err != nil {
		t.Fatal(err)
	}
	assessment := orchestration.Assessment{Outcome: "accept", Goal: "Prepare the requested document.", Reason: "An explicit request from an allowed sender."}
	if err = s.CompleteAutomationEvaluation(ctx, candidate.ID, assessment); err != nil {
		t.Fatal(err)
	}
	events, err := s.ListWorkspaceEvents(actor, boot.WorkspaceEventCursor)
	if err != nil || !slices.ContainsFunc(events, func(e WorkspaceEvent) bool {
		return e.Type == "github.event.decided" && e.RoomID == room.ID && e.EntityID == original.ID
	}) {
		t.Fatal("accepted GitHub projection had no source notification", events, err)
	}
	accepted, err := s.GetGitHubEvent(actor, original.ID)
	if err != nil || accepted.Status != "accepted" || accepted.TaskID == "" {
		t.Fatal(accepted, err)
	}
	last := events[len(events)-1].ID
	if err = s.CompleteAutomationEvaluation(ctx, candidate.ID, assessment); err != nil {
		t.Fatal(err)
	}
	if tail, err := s.ListWorkspaceEvents(actor, last); err != nil || len(tail) != 0 {
		t.Fatal("completed evaluation replay emitted another decision", tail, err)
	}
}

func TestPersonalReadNotificationsOnlyFollowActualOwnReadChanges(t *testing.T) {
	s, room, _ := roomFixture(t)
	owner, member := teamUsers(t, s)
	person, _ := UserFromContext(member)
	sent, err := s.SendMessage(owner, room.ID, SendMessageInput{RequestID: "mention", Content: "@writer Please review."}, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	items, err := s.ListInbox(member)
	if err != nil || len(items) != 1 {
		t.Fatal(items, err)
	}
	boot, err := s.Bootstrap(member, "browser")
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveRoomRead(member, room.ID, sent.Message.Seq); err != nil {
		t.Fatal(err)
	}
	events, err := s.ListWorkspaceEvents(ctx, boot.WorkspaceEventCursor)
	if err != nil || len(events) != 1 || events[0].Type != "personal_read.updated" || events[0].EntityID != person.ID || events[0].RoomID != "" {
		t.Fatal(events, err)
	}
	for _, seq := range []int64{sent.Message.Seq, 0} {
		if err = s.SaveRoomRead(member, room.ID, seq); err != nil {
			t.Fatal(err)
		}
	}
	requireCode(t, s.ReadInboxItem(owner, items[0].ID), "not_found")
	tail, err := s.ListWorkspaceEvents(ctx, events[0].ID)
	if err != nil || len(tail) != 0 {
		t.Fatal("duplicate reads or a rejected private read emitted a notification", tail, err)
	}
	if err = s.ReadInboxItem(member, items[0].ID); err != nil {
		t.Fatal(err)
	}
	tail, err = s.ListWorkspaceEvents(ctx, events[0].ID)
	if err != nil || len(tail) != 1 || tail[0].Type != "personal_read.updated" || tail[0].EntityID != person.ID {
		t.Fatal(tail, err)
	}
	read, err := s.ListInbox(member)
	if err != nil || read[0].ReadAt == "" {
		t.Fatal(read, err)
	}
	if err = s.ReadInboxItem(member, items[0].ID); err != nil {
		t.Fatal(err)
	}
	repeated, err := s.ListInbox(member)
	if err != nil || repeated[0].ReadAt != read[0].ReadAt {
		t.Fatal("repeat changed the accepted read timestamp", repeated, err)
	}
	tail, err = s.ListWorkspaceEvents(ctx, tail[0].ID)
	if err != nil || len(tail) != 0 {
		t.Fatal("repeated Inbox read emitted another notification", tail, err)
	}
}

func TestMessagePageFrontierAndRowsShareOneReadSnapshot(t *testing.T) {
	reached, release := make(chan struct{}), make(chan struct{})
	var armed atomic.Bool
	s := observedReadStore(t, func(query string) {
		if strings.Contains(query, "-- name: GetRoom :one") && armed.CompareAndSwap(true, false) {
			close(reached)
			<-release
		}
	})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	room, err := s.SeedRoom(ctx, Resource{ID: "files", Name: "Files", CurrentCommit: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.SendMessage(ctx, room.ID, SendMessageInput{RequestID: "first", Content: "Before read", PostOnly: true}, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	readCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	type result struct {
		page MessagePage
		err  error
	}
	read := make(chan result, 1)
	armed.Store(true)
	go func() {
		page, err := s.RoomMessagePage(readCtx, room.ID, "", 0)
		read <- result{page, err}
	}()
	select {
	case <-reached:
	case <-readCtx.Done():
		t.Fatal("read did not capture its Room frontier")
	}
	waits := s.db.Stats().WaitCount
	write := make(chan error, 1)
	go func() {
		_, err := s.SendMessage(readCtx, room.ID, SendMessageInput{RequestID: "later", Content: "After read began", PostOnly: true}, roomBudget)
		write <- err
	}()
	for s.db.Stats().WaitCount == waits && readCtx.Err() == nil {
		runtime.Gosched()
	}
	if readCtx.Err() != nil {
		t.Fatal("writer did not contend with the snapshot")
	}
	close(release)
	got := <-read
	if got.err != nil || got.page.Through != first.Message.Seq || len(got.page.Messages) != 1 || got.page.Messages[0].ID != first.Message.ID {
		t.Fatal(got.page, got.err)
	}
	if err := <-write; err != nil {
		t.Fatal(err)
	}
	later, err := s.RoomMessagePage(ctx, room.ID, "", got.page.Through)
	if err != nil || len(later.Messages) != 1 || later.Through != later.Messages[0].Seq || later.Messages[0].Content != "After read began" {
		t.Fatal(later, err)
	}
}

func TestRunnerEnrollmentChangesNotifyWithoutDisclosingCredentials(t *testing.T) {
	s := testStore(t)
	owner, _ := teamUsers(t, s)
	boot, err := s.Bootstrap(owner, "browser")
	if err != nil {
		t.Fatal(err)
	}
	enrollment, token, err := s.CreateRunnerEnrollment(owner, "Laptop", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	events, err := s.ListWorkspaceEvents(ctx, boot.WorkspaceEventCursor)
	if err != nil || len(events) != 1 || events[0].Type != "runner_enrollment.updated" || events[0].EntityID != enrollment.ID {
		t.Fatal(events, err)
	}
	encoded, _ := json.Marshal(events)
	if strings.Contains(string(encoded), token) || strings.Contains(string(encoded), remoteHash([]byte(token))) {
		t.Fatal("enrollment event disclosed its credential")
	}
	join := JoinRunnerInput{Token: token, RegistrationID: "register", Credential: strings.Repeat("r", 40), StorageID: "store"}
	if _, err = s.JoinRunner(ctx, join); err != nil {
		t.Fatal(err)
	}
	consumed, err := s.ListWorkspaceEvents(ctx, events[0].ID)
	if err != nil || len(consumed) != 2 || consumed[0].Type != "runner_enrollment.updated" || consumed[1].Type != "runner.updated" {
		t.Fatal(consumed, err)
	}
	if _, err = s.JoinRunner(ctx, join); err != nil {
		t.Fatal(err)
	}
	if err = s.RevokeRunnerEnrollment(owner, enrollment.ID); err != nil {
		t.Fatal(err)
	}
	revoked, err := s.ListWorkspaceEvents(ctx, consumed[1].ID)
	if err != nil || len(revoked) != 1 || revoked[0].Type != "runner_enrollment.updated" || revoked[0].EntityID != enrollment.ID {
		t.Fatal(revoked, err)
	}
}

func TestWorkspaceWakeupFollowsCommitAndIgnoresRollbackAndReads(t *testing.T) {
	s := testStore(t)
	wakeup := s.WorkspaceEventsChanged()
	if err := s.transaction(ctx, func(tx *sql.Tx) error {
		if err := event(ctx, sqlgen.New(tx), "team.changed", "", "example"); err != nil {
			return err
		}
		select {
		case <-wakeup:
			t.Fatal("notified before commit")
		default:
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-wakeup:
	default:
		t.Fatal("successful commit did not wake its subscriber")
	}
	events, err := s.ListWorkspaceEvents(ctx, 0)
	if err != nil || len(events) != 1 || events[0].EntityID != "example" {
		t.Fatal(events, err)
	}
	wakeup = s.WorkspaceEventsChanged()
	rejected := errors.New("rejected command")
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		if err := event(ctx, sqlgen.New(tx), "team.changed", "", "rejected"); err != nil {
			return err
		}
		return rejected
	})
	if !errors.Is(err, rejected) {
		t.Fatal(err)
	}
	if _, err = s.Bootstrap(ctx, "browser"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-wakeup:
		t.Fatal("rollback or read notified subscribers")
	default:
	}
	events, err = s.ListWorkspaceEvents(ctx, events[0].ID)
	if err != nil || len(events) != 0 {
		t.Fatal(events, err)
	}
}

func TestBootstrapCapturesNotificationFrontierBeforePersonalSnapshot(t *testing.T) {
	var queries []string
	armed := false
	s := observedReadStore(t, func(query string) {
		if armed {
			queries = append(queries, query)
		}
	})
	owner, _ := teamUsers(t, s)
	room, err := s.SeedRoom(owner, Resource{ID: "files", Name: "Files", CurrentCommit: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SavePreferences(owner, Preferences{Wallpaper: "solid", IconSnap: true}); err != nil {
		t.Fatal(err)
	}
	events, err := s.ListWorkspaceEvents(ctx, 0)
	if err != nil || len(events) == 0 {
		t.Fatal(events, err)
	}
	armed = true
	boot, err := s.Bootstrap(owner, "browser")
	armed = false
	if err != nil || boot.Preferences.Wallpaper != "solid" || boot.WorkspaceEventCursor != events[len(events)-1].ID {
		t.Fatal(boot, err)
	}
	if len(queries) < 2 || !strings.Contains(queries[0], "-- name: WorkspaceEventCursor :one") {
		t.Fatalf("snapshot read before notification frontier: %v", queries)
	}
	if _, err = s.SendMessage(owner, room.ID, SendMessageInput{RequestID: "after-bootstrap", Content: "A later fact", PostOnly: true}, roomBudget); err != nil {
		t.Fatal(err)
	}
	later, err := s.ListWorkspaceEvents(ctx, boot.WorkspaceEventCursor)
	if err != nil || !slices.ContainsFunc(later, func(e WorkspaceEvent) bool { return e.Type == "message.created" }) {
		t.Fatal(later, err)
	}
}

func TestAgentRoomReadReturnsOnlySelectedMemberships(t *testing.T) {
	s, first, selected := roomFixture(t)
	second, err := s.CreateRoom(ctx, Room{Name: "Second", ResourceID: first.ResourceID})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.JoinRoom(ctx, second.ID, selected.ID); err != nil {
		t.Fatal(err)
	}
	unjoined, err := s.CreateRoom(ctx, Room{Name: "Unjoined", ResourceID: first.ResourceID})
	if err != nil {
		t.Fatal(err)
	}
	rooms, err := s.ListAgentRooms(ctx, selected.ID)
	if err != nil || len(rooms) != 2 || !slices.Contains(rooms, first.ID) || !slices.Contains(rooms, second.ID) || slices.Contains(rooms, unjoined.ID) {
		t.Fatal(rooms, err)
	}
	if err = s.LeaveRoom(ctx, second.ID, selected.ID); err != nil {
		t.Fatal(err)
	}
	rooms, err = s.ListAgentRooms(ctx, selected.ID)
	if err != nil || len(rooms) != 1 || rooms[0] != first.ID {
		t.Fatal(rooms, err)
	}
	_, err = s.ListAgentRooms(ctx, "missing")
	requireCode(t, err, "not_found")
}

func TestAttemptLogsNotifyActivityWithoutChangingRoomActivity(t *testing.T) {
	s := testStore(t)
	owner, _ := teamUsers(t, s)
	runner, _ := enrollTestRunner(t, s, owner, "alpha")
	room, agent := remoteTestRoom(t, s, owner, runner)
	run, attempt := prepareTestAttempt(t, s, sendRemoteTestRun(t, s, owner, room, agent, "run"))
	before, err := s.Bootstrap(owner, "browser")
	if err != nil {
		t.Fatal(err)
	}
	room, _ = s.GetRoom(ctx, room.ID)
	if replayed, err := s.RecordAttemptEvent(ctx, runner.ID, attempt.ID, 1, "assistant", "Original reply.", strings.Repeat("1", 64)); err != nil || replayed {
		t.Fatal(replayed, err)
	}
	events, err := s.ListWorkspaceEvents(ctx, before.WorkspaceEventCursor)
	if err != nil || len(events) != 1 || events[0].Type != "run.activity" || events[0].EntityID != run.ID {
		t.Fatal(events, err)
	}
	updatedRoom, _ := s.GetRoom(ctx, room.ID)
	if updatedRoom.ActivitySeq != room.ActivitySeq || updatedRoom.UpdatedAt != room.UpdatedAt {
		t.Fatal("log append changed Room ordering", room, updatedRoom)
	}
	saved, err := s.GetRun(ctx, run.ID)
	if err != nil || saved.Output != "Original reply." || saved.Status != run.Status {
		t.Fatal(saved, err)
	}
	if replayed, err := s.RecordAttemptEvent(ctx, runner.ID, attempt.ID, 1, "assistant", "Original reply.", strings.Repeat("1", 64)); err != nil || !replayed {
		t.Fatal(replayed, err)
	}
	if _, err = s.RecordAttemptEvent(ctx, runner.ID, attempt.ID, 2, "status", "checking", strings.Repeat("2", 64)); err != nil {
		t.Fatal(err)
	}
	tail, err := s.ListWorkspaceEvents(ctx, events[0].ID)
	if err != nil || len(tail) != 1 || tail[0].Type != "run.updated" {
		t.Fatal(tail, err)
	}
	updatedRoom, _ = s.GetRoom(ctx, room.ID)
	if updatedRoom.ActivitySeq != tail[0].ID {
		t.Fatal("status change did not update Room activity", updatedRoom, tail)
	}
	activity, err := s.ListRunEvents(ctx, run.ID, 0)
	if err != nil || !slices.ContainsFunc(activity, func(e RunEvent) bool { return e.Type == "assistant" && e.Text == "Original reply." }) {
		t.Fatal(activity, err)
	}
}
