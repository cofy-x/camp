package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"testing"
)

func testGitHubPayload(title, body string) []byte {
	raw, _ := json.Marshal(map[string]any{"action": "opened", "repository": map[string]any{"id": 123, "full_name": "acme/project"}, "sender": map[string]any{"id": 7, "login": "external-person", "type": "User"}, "issue": map[string]any{"id": 99, "number": 3, "title": title, "body": body}})
	return raw
}
func testGitHubSource(t *testing.T, s *Store, actor context.Context, room Room) GitHubSource {
	t.Helper()
	source, err := s.CreateGitHubSource(actor, room.ID, CreateGitHubSourceInput{RequestID: newID(), Repository: "Acme/Project", Secret: strings.Repeat("private", 8)}, strings.Repeat("a", 48))
	if err != nil {
		t.Fatal(err)
	}
	return source
}
func receiveTestGitHub(t *testing.T, s *Store, source GitHubSource, payload []byte) GitHubEvent {
	t.Helper()
	event, _, err := s.ReceiveGitHubDelivery(context.Background(), source.ID, source.SecretRef, "aaaaaaaa-bbbb-cccc-dddd-"+newID()[:12], "issues", payload)
	if err != nil {
		t.Fatal(err)
	}
	return event
}

func TestGitHubObservationDeduplicationDisableRotationAndIdentity(t *testing.T) {
	s, admin, member, room, _, _ := taskFixture(t)
	source := testGitHubSource(t, s, admin, room)
	payload := testGitHubPayload("Title", "  original\n你好\n ")
	delivery := "aaaaaaaa-bbbb-cccc-dddd-eeeeeeeeeeee"
	first, duplicate, err := s.ReceiveGitHubDelivery(ctx, source.ID, source.SecretRef, delivery, "issues", payload)
	if err != nil || duplicate || first.Status != "pending" {
		t.Fatal(first, duplicate, err)
	}
	for _, id := range []string{delivery, "AAAAAAAA-BBBB-CCCC-DDDD-EEEEEEEEEEEE", "11111111-bbbb-cccc-dddd-eeeeeeeeeeee"} {
		next, duplicate, err := s.ReceiveGitHubDelivery(ctx, source.ID, source.SecretRef, id, "issues", payload)
		if err != nil || !duplicate || next.ID != first.ID {
			t.Fatal(next, duplicate, err)
		}
	}
	if _, _, err = s.ReceiveGitHubDelivery(ctx, source.ID, source.SecretRef, delivery, "issues", testGitHubPayload("Changed", "body")); err == nil {
		t.Fatal("delivery ID rebound")
	}
	for _, guid := range []string{delivery, "88888888-bbbb-cccc-dddd-eeeeeeeeeeee"} {
		if _, _, err = s.ReceiveGitHubDelivery(ctx, source.ID, source.SecretRef, guid, "an_unsigned_header_change", payload); err == nil {
			t.Fatal("changed unsigned header created another observation")
		}
	}
	raw, _ := s.GitHubPayload(ctx, first.ID)
	if string(raw) != string(payload) {
		t.Fatal("original changed")
	}
	if _, err = s.UpdateGitHubSource(member, source.ID, UpdateGitHubSourceInput{RequestID: newID(), Revision: source.Revision, Enabled: new(false)}, ""); err == nil {
		t.Fatal("member changed source")
	}
	off, err := s.UpdateGitHubSource(admin, source.ID, UpdateGitHubSourceInput{RequestID: newID(), Revision: source.Revision, Enabled: new(false)}, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.ReceiveGitHubDelivery(ctx, source.ID, source.SecretRef, "77777777-bbbb-cccc-dddd-eeeeeeeeeeee", "issues", payload); err == nil {
		t.Fatal("disabled source received")
	}
	if _, err = pendingGitHubEvent(ctx, s.queries(), room.ID, first.ID); err == nil {
		t.Fatal("disabled source confirmed")
	}
	updated, err := s.UpdateGitHubSource(admin, source.ID, UpdateGitHubSourceInput{RequestID: newID(), Revision: off.Revision, Enabled: new(true), Secret: strings.Repeat("replacement", 4)}, strings.Repeat("b", 48))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.ReceiveGitHubDelivery(ctx, updated.ID, source.SecretRef, "22222222-bbbb-cccc-dddd-eeeeeeeeeeee", "issues", payload); err == nil {
		t.Fatal("stale credential admitted after rotation")
	}
	received := receiveTestGitHub(t, s, updated, testGitHubPayload("New title", "body"))
	if received.SourceRevision != updated.Revision {
		t.Fatal(received)
	}
	replaced := strings.Replace(string(payload), `"id":123`, `"id":124`, 1)
	if _, _, err = s.ReceiveGitHubDelivery(ctx, updated.ID, updated.SecretRef, "33333333-bbbb-cccc-dddd-eeeeeeeeeeee", "issues", []byte(replaced)); err == nil {
		t.Fatal("repository identity changed")
	}
	var tasks int
	if err = s.db.QueryRow("SELECT count(*) FROM collaboration_tasks").Scan(&tasks); err != nil || tasks != 0 {
		t.Fatal("observation created work", tasks, err)
	}
}

func TestGitHubConfirmationAtomicityConcurrentDecisionAndFrozenContext(t *testing.T) {
	s, admin, member, room, writer, _ := taskFixture(t)
	source := testGitHubSource(t, s, admin, room)
	body := strings.Repeat("  external 你好\n", 3000)
	event := receiveTestGitHub(t, s, source, testGitHubPayload("Context title", body))
	in := CreateTaskInput{InputProtocol: 1, PagedResponse: true, RequestID: newID(), SourceEventID: event.ID, Goal: "Prepare the explicitly requested report.", ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID}
	if _, err := s.db.Exec("CREATE TRIGGER fail_github_admission BEFORE INSERT ON orchestration_records BEGIN SELECT RAISE(ABORT,'forced admission failure'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateTask(admin, room.ID, in, roomBudget); err == nil {
		t.Fatal("injected failure did not abort")
	}
	for _, table := range []string{"collaboration_tasks", "runs", "task_inputs", "task_requests", "github_event_decisions", "orchestration_records"} {
		var count int
		if err := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("partial confirmation", table, count, err)
		}
	}
	if _, err := s.db.Exec("DROP TRIGGER fail_github_admission"); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateTask(admin, room.ID, in, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	if task.SourceEventID != event.ID || task.InputRevision < 2 {
		t.Fatal(task)
	}
	inputs, err := s.TaskInputs(ctx, task.ID, 0, task.InputRevision)
	if err != nil {
		t.Fatal(err)
	}
	var reconstructed strings.Builder
	for i, input := range inputs {
		prefix := fmt.Sprintf("Untrusted GitHub context %s, part %d:\n\n", event.ID, i+1)
		suffix := fmt.Sprintf("\n\n[End of GitHub context part %d]", i+1)
		if !strings.HasPrefix(input.Content, prefix) || !strings.HasSuffix(input.Content, suffix) {
			t.Fatal(input)
		}
		reconstructed.WriteString(strings.TrimSuffix(strings.TrimPrefix(input.Content, prefix), suffix))
	}
	if reconstructed.String() != event.Observation.Context() {
		t.Fatal("source whitespace or bytes lost")
	}
	run, _ := s.GetRun(ctx, task.ActiveRunID)
	if run.TaskInputRevision != task.InputRevision {
		t.Fatal("initial round did not freeze source context")
	}
	page, _ := s.OrchestrationPage(ctx, room.ID, 0, nil, 20)
	if len(page.Items) != 1 || page.Items[0].Trigger.SourceEventID != event.ID || len(page.Items[0].Mandates) != 1 {
		t.Fatal(page)
	}
	off, err := s.UpdateGitHubSource(admin, source.ID, UpdateGitHubSourceInput{RequestID: newID(), Revision: source.Revision, Enabled: new(false)}, "")
	if err != nil {
		t.Fatal(err)
	}
	_ = off
	replay, err := s.CreateTask(admin, room.ID, in, roomBudget)
	if err != nil || replay.ID != task.ID {
		t.Fatal("lost-response recovery changed authorization", replay, err)
	}
	other := in
	other.RequestID = newID()
	if _, err = s.CreateTask(member, room.ID, other, roomBudget); err == nil {
		t.Fatal("same event created another task")
	}

	// Two distinct human requests race for one pending event. Only one can create
	// the task, its original input log, journal and decision.
	source, err = s.UpdateGitHubSource(admin, source.ID, UpdateGitHubSourceInput{RequestID: newID(), Revision: off.Revision, Enabled: new(true)}, "")
	if err != nil {
		t.Fatal(err)
	}
	event = receiveTestGitHub(t, s, source, testGitHubPayload("Concurrent", "context"))
	var wg sync.WaitGroup
	outcomes := make(chan error, 2)
	for _, actor := range []context.Context{admin, member} {
		wg.Go(func() {
			request := in
			request.RequestID = newID()
			request.SourceEventID = event.ID
			_, err := s.CreateTask(actor, room.ID, request, roomBudget)
			outcomes <- err
		})
	}
	wg.Wait()
	close(outcomes)
	success := 0
	for err := range outcomes {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal("concurrent event admitted more than once", success)
	}
}

func TestGitHubEventPagingDismissalReceiptsAndReopen(t *testing.T) {
	s, admin, member, room, _, _ := taskFixture(t)
	source := testGitHubSource(t, s, admin, room)
	for i := range 23 {
		receiveTestGitHub(t, s, source, testGitHubPayload(fmt.Sprint(i)+strings.Repeat("\"", 1000), "original body omitted from list"))
	}
	page, err := s.GitHubEventsPage(ctx, room.ID, "all", 0, nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	through := page.Through
	receiveTestGitHub(t, s, source, testGitHubPayload("late arrival", "context"))
	seen := map[string]bool{}
	for {
		encoded, _ := json.Marshal(page)
		if len(encoded)+1 > 64<<10 {
			t.Fatal("unbounded page")
		}
		for _, event := range page.Items {
			if seen[event.ID] || event.Body != "" || event.Seq > through {
				t.Fatal(event)
			}
			seen[event.ID] = true
		}
		if !page.HasMore {
			break
		}
		page, err = s.GitHubEventsPage(ctx, room.ID, "all", *page.NextAfter, &through, 20)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 23 {
		t.Fatal("paging omitted originals", len(seen))
	}
	var id string
	for value := range seen {
		id = value
		break
	}
	in := DismissGitHubEventInput{RequestID: newID(), Reason: "Not needed for this Room."}
	event, err := s.DismissGitHubEvent(member, id, in)
	if err != nil || event.Status != "dismissed" {
		t.Fatal(event, err)
	}
	replay, err := s.DismissGitHubEvent(member, id, in)
	if err != nil || replay.DecidedAt != event.DecidedAt {
		t.Fatal(replay, err)
	}
	if _, err = s.DismissGitHubEvent(admin, id, DismissGitHubEventInput{RequestID: newID(), Reason: "Another decision"}); err == nil {
		t.Fatal("replaced decision")
	}
	if _, err = s.GitHubRequest(admin, in.RequestID); err == nil {
		t.Fatal("another actor read receipt")
	}
	var seq int
	var name, path string
	if err = s.db.QueryRow("PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	before, _ := s.GitHubPayload(ctx, id)
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	after, _ := reopened.GitHubPayload(ctx, id)
	if string(before) != string(after) {
		t.Fatal("reopen changed original")
	}
	result, duplicate, err := reopened.ReceiveGitHubDelivery(ctx, source.ID, source.SecretRef, "99999999-aaaa-bbbb-cccc-dddddddddddd", "issues", before)
	if err != nil || !duplicate || result.ID != id || result.Status != "dismissed" {
		t.Fatal(result, duplicate, err)
	}
}

func TestGitHubRepositoryReplacementRetainsHistoryAndSerializesActivation(t *testing.T) {
	s, actor, _, room, _, _ := taskFixture(t)
	original := testGitHubSource(t, s, actor, room)
	oldPayload := testGitHubPayload("Original repository", "Retain this original.")
	oldEvent := receiveTestGitHub(t, s, original, oldPayload)
	original, err := s.UpdateGitHubSource(actor, original.ID, UpdateGitHubSourceInput{RequestID: newID(), Revision: original.Revision, Enabled: new(false)}, "")
	if err != nil {
		t.Fatal(err)
	}
	replacement, err := s.CreateGitHubSource(actor, room.ID, CreateGitHubSourceInput{RequestID: newID(), Repository: original.Repository, Secret: strings.Repeat("new-secret", 4)}, strings.Repeat("b", 48))
	if err != nil || replacement.ID == original.ID {
		t.Fatal("disabled source prevented replacement", replacement, err)
	}
	newPayload := strings.Replace(string(testGitHubPayload("Replacement repository", "New identity.")), `"id":123`, `"id":124`, 1)
	newEvent := receiveTestGitHub(t, s, replacement, []byte(newPayload))
	if oldEvent.RepositoryID == newEvent.RepositoryID || oldEvent.SourceID == newEvent.SourceID {
		t.Fatal("replacement reused the original identity")
	}
	if _, err = s.UpdateGitHubSource(actor, original.ID, UpdateGitHubSourceInput{RequestID: newID(), Revision: original.Revision, Enabled: new(true)}, ""); err == nil {
		t.Fatal("two active sources acquired the same address")
	}
	if _, err = s.CreateGitHubSource(actor, room.ID, CreateGitHubSourceInput{RequestID: newID(), Repository: original.Repository, Secret: strings.Repeat("another", 8)}, strings.Repeat("c", 48)); err == nil {
		t.Fatal("created a second active source")
	}
	replacement, err = s.UpdateGitHubSource(actor, replacement.ID, UpdateGitHubSourceInput{RequestID: newID(), Revision: replacement.Revision, Enabled: new(false)}, "")
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	outcomes := make(chan error, 2)
	for _, source := range []GitHubSource{original, replacement} {
		wg.Go(func() {
			_, err := s.UpdateGitHubSource(actor, source.ID, UpdateGitHubSourceInput{RequestID: newID(), Revision: source.Revision, Enabled: new(true)}, "")
			outcomes <- err
		})
	}
	wg.Wait()
	close(outcomes)
	success := 0
	for err := range outcomes {
		if err == nil {
			success++
		}
	}
	if success != 1 {
		t.Fatal("concurrent activations were not exclusive", success)
	}
	sources, err := s.ListGitHubSources(ctx, room.ID)
	if err != nil || len(sources) != 2 {
		t.Fatal(sources, err)
	}
	active := 0
	for _, source := range sources {
		if source.Enabled {
			active++
		}
	}
	if active != 1 {
		t.Fatal("invalid active route count", active)
	}
	retained, _ := s.GitHubPayload(ctx, oldEvent.ID)
	if string(retained) != string(oldPayload) {
		t.Fatal("replacement changed original payload")
	}
	retainedEvent, _ := s.GetGitHubEvent(ctx, oldEvent.ID)
	if retainedEvent != oldEvent {
		t.Fatal("replacement changed event metadata")
	}
}
