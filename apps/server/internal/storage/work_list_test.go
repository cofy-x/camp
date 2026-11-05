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

func TestWorkDesktopListFiltersBeforePriorityPaginationAcrossRooms(t *testing.T) {
	s, admin, member, room, _, _ := taskFixture(t)
	other, err := s.CreateRoomWithResource(admin, Room{Name: "Customer planning"}, Resource{Name: "Customer files", CurrentCommit: strings.Repeat("c", 40)})
	if err != nil {
		t.Fatal(err)
	}
	firstQuestion := createTestWork(t, s, admin, room)
	firstQuestion = decideTestWork(t, s, admin, firstQuestion, orchestration.WorkItemOperation{Kind: "request_judgment", Question: "Choose the release date."})
	secondQuestion := createTestWork(t, s, admin, other)
	secondQuestion = decideTestWork(t, s, admin, secondQuestion, orchestration.WorkItemOperation{Kind: "request_judgment", Question: "Choose the audience."})
	otherOwner := createTestWork(t, s, member, other)
	decideTestWork(t, s, member, otherOwner, orchestration.WorkItemOperation{Kind: "request_judgment", Question: "A teammate’s decision."})
	for range 22 {
		finished := createTestWork(t, s, admin, room)
		decideTestWork(t, s, admin, finished, orchestration.WorkItemOperation{Kind: "complete"})
	}
	normal := createTestWork(t, s, admin, other)
	before, err := s.GetWorkItem(admin, normal.ID)
	if err != nil {
		t.Fatal(err)
	}
	var eventCount int
	if err = s.db.QueryRow("SELECT count(*) FROM workspace_events").Scan(&eventCount); err != nil {
		t.Fatal(err)
	}
	var inboxCount int
	if err = s.db.QueryRow("SELECT count(*) FROM inbox").Scan(&inboxCount); err != nil {
		t.Fatal(err)
	}
	first, err := s.ListWorkItems(admin, WorkListOptions{Limit: 1})
	if err != nil || len(first.Items) != 1 || first.Items[0].ID != secondQuestion.ID || !first.Items[0].NeedsYou || first.Items[0].RoomName != other.Name || !first.HasMore || first.NextCursor == "" {
		t.Fatal(first, err)
	}
	// A new insertion cannot enter the retained sequence frontier mid-traversal.
	newer := createTestWork(t, s, admin, other)
	second, err := s.ListWorkItems(admin, WorkListOptions{Limit: 2, Cursor: first.NextCursor})
	if err != nil || len(second.Items) != 2 || second.Items[0].ID != firstQuestion.ID || second.Items[1].ID != normal.ID || second.HasMore || second.NextCursor != "" || second.Items[1].NeedsYou {
		t.Fatal(second, err, newer.ID)
	}
	needs, err := s.ListWorkItems(admin, WorkListOptions{Scope: "team", View: "needs_you", Limit: 20})
	if err != nil || len(needs.Items) != 2 || needs.Items[0].ID != secondQuestion.ID || needs.Items[1].ID != firstQuestion.ID {
		t.Fatal(needs, err)
	}
	teamPage, err := s.ListWorkItems(admin, WorkListOptions{Scope: "team", View: "active", RoomID: other.ID})
	if err != nil || len(teamPage.Items) != 4 {
		t.Fatal(teamPage, err)
	}
	foundOther := false
	for _, item := range teamPage.Items {
		if item.ID == otherOwner.ID {
			foundOther = true
			if item.NeedsYou {
				t.Fatal("administrator was assigned another owner’s judgment", item)
			}
		}
		if item.RoomID != other.ID {
			t.Fatal("room filter leaked", item)
		}
	}
	if !foundOther {
		t.Fatal("enabled teammate visibility was lost")
	}
	done, err := s.ListWorkItems(admin, WorkListOptions{View: "done", Limit: 20})
	if err != nil || len(done.Items) != 20 || !done.HasMore {
		t.Fatal(done, err)
	}
	last, err := s.ListWorkItems(admin, WorkListOptions{View: "done", Limit: 20, Cursor: done.NextCursor})
	if err != nil || len(last.Items) != 2 || last.HasMore || last.Items[0].Seq >= done.Items[19].Seq {
		t.Fatal(last, err)
	}
	after, err := s.GetWorkItem(admin, normal.ID)
	if err != nil || after.Revision != before.Revision || after.Usage != before.Usage {
		t.Fatal("list mutated the matter", after, err)
	}
	// Only the one explicit insertion above may have written events.
	var afterCount int
	if err = s.db.QueryRow("SELECT count(*) FROM workspace_events").Scan(&afterCount); err != nil {
		t.Fatal(err)
	}
	if afterCount-eventCount != 1 {
		t.Fatal("list wrote events", eventCount, afterCount)
	}
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
	assertScalar(t, s, "SELECT count(*) FROM automation_evaluations", 0)
	assertScalar(t, s, "SELECT count(*) FROM runs", 0)
	assertScalar(t, s, "SELECT count(*) FROM inbox", inboxCount)
	raw, _ := json.Marshal(first.Items[0])
	var shape map[string]json.RawMessage
	if err = json.Unmarshal(raw, &shape); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"budget", "usage", "evidence", "decisions", "tasks", "timers", "actions", "watches"} {
		if _, exists := shape[key]; exists {
			t.Fatal("list bloated with grants/history", key)
		}
	}
}

func TestWorkDesktopNeedsYouUsesCurrentOwnerAndActionableTaskState(t *testing.T) {
	s, admin, member, room, writer, _ := taskFixture(t)
	w := createTestWork(t, s, admin, room)
	w = decideTestWork(t, s, admin, w, orchestration.WorkItemOperation{Kind: "execute", ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID})
	task, err := s.GetTaskState(admin, w.ActiveTaskID)
	if err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []struct {
		name, status, run string
		question          *TaskQuestion
		want              bool
	}{
		{name: "queued work is not a human action", status: "queued"},
		{name: "ordinary running work", status: "running", run: task.ActiveRunID},
		{name: "blocked work without a live run", status: "needs_attention", want: true},
		{name: "blocked work still has a live run", status: "needs_attention", run: "live-run"},
		{name: "retained interruption needs recovery", status: "interrupted", want: true},
		{name: "unanswered active question", status: "waiting_for_person", question: &TaskQuestion{ID: "q", Text: "Which release?"}, want: true},
		{name: "answered question", status: "waiting_for_person", question: &TaskQuestion{ID: "q", Text: "Which release?", Answer: "Stable"}},
		{name: "no actionable question", status: "waiting_for_person"},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			// Retained task snapshots model the read boundary without dispatching a Runner.
			task.Status, task.ActiveRunID, task.Question = scenario.status, scenario.run, scenario.question
			raw, e := json.Marshal(task)
			if e != nil {
				t.Fatal(e)
			}
			if e = s.queries().SaveTask(admin, sqlgen.SaveTaskParams{ID: task.ID, Status: task.Status, Revision: task.Revision, Snapshot: string(raw), UpdatedAt: task.UpdatedAt}); e != nil {
				t.Fatal(e)
			}
			page, e := s.ListWorkItems(admin, WorkListOptions{View: "needs_you"})
			if e != nil || (len(page.Items) == 1) != scenario.want {
				t.Fatal(page, e)
			}
			if scenario.want && (page.Items[0].Execution == nil || !page.Items[0].NeedsYou || page.Items[0].Execution.Status != scenario.status) {
				t.Fatal("filter and projection disagree", page)
			}
			other, e := s.ListWorkItems(member, WorkListOptions{Scope: "team", View: "needs_you"})
			if e != nil || len(other.Items) != 0 {
				t.Fatal("another owner was asked to act", other, e)
			}
		})
	}
	if err = s.transaction(admin, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		item, e := getWorkItem(admin, q, w.ID)
		if e != nil {
			return e
		}
		item.StopRequested = true
		item.Status = "stopping"
		return saveWorkItem(admin, q, &item)
	}); err != nil {
		t.Fatal(err)
	}
	page, err := s.ListWorkItems(admin, WorkListOptions{View: "needs_you"})
	if err != nil || len(page.Items) != 0 {
		t.Fatal("stopping work invited a new action", page, err)
	}
	active, err := s.ListWorkItems(admin, WorkListOptions{})
	if err != nil || len(active.Items) != 1 || active.Items[0].Status != "stopping" || active.Items[0].NeedsYou {
		t.Fatal(active, err)
	}
}

func TestWorkDesktopCursorAuthorityAndRetainedDisabledOwnerVisibility(t *testing.T) {
	s, admin, member, room, _, _ := taskFixture(t)
	for range 3 {
		createTestWork(t, s, member, room)
	}
	first, err := s.ListWorkItems(member, WorkListOptions{Limit: 1})
	if err != nil || !first.HasMore {
		t.Fatal(first, err)
	}
	for _, change := range []WorkListOptions{{Scope: "team"}, {View: "all"}, {RoomID: room.ID}} {
		change.Cursor = first.NextCursor
		_, err = s.ListWorkItems(member, change)
		requireCode(t, err, "invalid_page")
	}
	_, err = s.ListWorkItems(admin, WorkListOptions{Cursor: first.NextCursor})
	requireCode(t, err, "invalid_page")
	for _, raw := range []string{"bad", strings.Repeat("x", 2049), base64.RawURLEncoding.EncodeToString([]byte(`{"v":1,"v":1}`))} {
		_, err = s.ListWorkItems(member, WorkListOptions{Cursor: raw})
		requireCode(t, err, "invalid_page")
	}
	validRaw, _ := base64.RawURLEncoding.DecodeString(first.NextCursor)
	duplicate := strings.Replace(string(validRaw), `"v":1`, `"v":1,"v":1`, 1)
	_, err = s.ListWorkItems(member, WorkListOptions{Cursor: base64.RawURLEncoding.EncodeToString([]byte(duplicate))})
	requireCode(t, err, "invalid_page")
	decoded, _ := decodeWorkListCursor(first.NextCursor, workListCursor{Actor: actorID(member), Scope: "mine", View: "active"})
	decoded.Priority = 2
	_, err = s.ListWorkItems(member, WorkListOptions{Cursor: encodeWorkListCursor(decoded)})
	requireCode(t, err, "invalid_page")
	decoded.Priority, decoded.Through = 0, 1<<50
	_, err = s.ListWorkItems(member, WorkListOptions{Cursor: encodeWorkListCursor(decoded)})
	requireCode(t, err, "invalid_page")
	_, err = s.ListWorkItems(context.Background(), WorkListOptions{Scope: "team"})
	requireCode(t, err, "unauthenticated")
	for _, options := range []WorkListOptions{{View: "unknown"}, {Scope: "all"}, {Limit: 21}, {Limit: -1}} {
		_, err = s.ListWorkItems(member, options)
		requireCode(t, err, "invalid_page")
	}
	_, err = s.ListWorkItems(member, WorkListOptions{RoomID: "missing"})
	requireCode(t, err, "not_found")
	user, _ := UserFromContext(member)
	disabled := false
	if _, err = s.UpdateUserAccess(admin, user.ID, nil, &disabled); err != nil {
		t.Fatal(err)
	}
	_, err = s.ListWorkItems(member, WorkListOptions{Cursor: first.NextCursor})
	requireCode(t, err, "unauthenticated")
	retained, err := s.ListWorkItems(admin, WorkListOptions{Scope: "team"})
	if err != nil || len(retained.Items) != 3 {
		t.Fatal("disabling an owner hid retained team matters", retained, err)
	}
}

func TestWorkDesktopRetainsExactExecutionQuestionAndSavedResult(t *testing.T) {
	s, admin, _, room, writer, _ := taskFixture(t)
	w := createTestWork(t, s, admin, room)
	w = decideTestWork(t, s, admin, w, orchestration.WorkItemOperation{Kind: "execute", ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID})
	task, err := s.GetTaskState(admin, w.ActiveTaskID)
	if err != nil {
		t.Fatal(err)
	}
	checkpoint := strings.Repeat("b", 40)
	enableTaskInputs(t, s, task.Participants[0].RunnerID)
	finishTaskTestRound(t, s, task, checkpoint, CoordinationIntent{CallID: newID(), Kind: "ask_person", Question: "Which launch date?"})
	page, err := s.ListWorkItems(admin, WorkListOptions{View: "needs_you"})
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	item := page.Items[0]
	if item.Execution == nil || item.Execution.TaskID != task.ID || item.Execution.Question == nil || item.Execution.Question.Text != "Which launch date?" || item.Result == nil || item.Result.TaskID != task.ID || item.Result.Commit != checkpoint || item.Result.ResourceID != room.ResourceID || item.Result.Published || !item.NeedsYou {
		t.Fatal("cross-Room summary lost its exact current question or saved result", item)
	}
}
