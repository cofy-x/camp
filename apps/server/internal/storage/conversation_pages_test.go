package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func largeConversation(t *testing.T) (*Store, Room) {
	t.Helper()
	s, room, agent := roomFixture(t)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		for i := 1; i <= 10000; i++ {
			name := fmt.Sprintf("history-%05d", i)
			kind, runID := "user", ""
			if i > 1 && i%10 == 1 {
				kind = "agent"
				runID = fmt.Sprintf("execution-%05d", i-1)
			}
			err := q.CreateMessage(ctx, sqlgen.CreateMessageParams{ID: name, RoomID: room.ID, Seq: int64(i), AuthorKind: kind, RunID: runID, Content: fmt.Sprintf("Message %d", i), RequestID: name, CreatedAt: now, UpdatedAt: now, ReferencesJson: "[]"})
			if err != nil {
				return err
			}
			if i%10 == 0 {
				run := Run{ID: fmt.Sprintf("execution-%05d", i), RoomID: room.ID, OriginMessageID: name, AgentID: agent.ID, ResourceID: room.ResourceID, Status: "succeeded", CreatedAt: now, UpdatedAt: now, Output: "PRIVATE_ORIGINAL_OUTPUT", AgentSnapshot: Agent{ID: agent.ID, Name: agent.Name, Instructions: strings.Repeat("PRIVATE_INSTRUCTIONS ", 100)}, Files: []RunFile{{Path: "note.md", Status: "added", After: "PRIVATE_FILE_BODY"}}}
				raw, _ := json.Marshal(run)
				if err = q.CreateRun(ctx, sqlgen.CreateRunParams{ID: run.ID, RoomID: room.ID, OriginMessageID: name, AgentID: agent.ID, ResourceID: room.ResourceID, Status: run.Status, Snapshot: string(raw), CreatedAt: now, UpdatedAt: now}); err != nil {
					return err
				}
			}
		}
		return q.SetRoomMessageSequence(ctx, sqlgen.SetRoomMessageSequenceParams{ID: room.ID, LastMessageSeq: 10000})
	})
	if err != nil {
		t.Fatal(err)
	}
	return s, room
}

func TestConversationWindowsSeekLargeHistoryAndKeepOriginals(t *testing.T) {
	s, room := largeConversation(t)
	start := time.Now()
	recent, err := s.RoomMessageWindow(ctx, room.ID, "", MessageWindowPosition{})
	if err != nil {
		t.Fatal(err)
	}
	if len(recent.Messages) != 50 || recent.Messages[0].Seq != 9951 || recent.Messages[49].Seq != 10000 || !recent.HasOlder || recent.HasNewer || recent.Through != 10000 {
		t.Fatal("recent window", recent)
	}
	older, err := s.RoomMessageWindow(ctx, room.ID, "", MessageWindowPosition{Before: 9951})
	if err != nil || len(older.Messages) != 50 || older.Messages[49].Seq != 9950 || !older.HasNewer {
		t.Fatal("older", err)
	}
	newer, err := s.RoomMessageWindow(ctx, room.ID, "", MessageWindowPosition{After: 9950})
	if err != nil || newer.Messages[0].Seq != 9951 || newer.HasNewer {
		t.Fatal("newer", err)
	}
	around, err := s.RoomMessageWindow(ctx, room.ID, "", MessageWindowPosition{Around: "history-00011"})
	if err != nil || around.Messages[10].ID != "history-00011" || !around.HasNewer {
		t.Fatal("direct historical target", err)
	}
	ids := []string{}
	for _, m := range around.Messages {
		ids = append(ids, m.ID)
	}
	activity, err := s.ConversationRuns(ctx, room.ID, ids)
	if err != nil || len(activity.Items) < 3 || len(activity.Items) > 5 {
		t.Fatal("related bounded metadata", len(activity.Items), err)
	}
	found := false
	for _, item := range activity.Items {
		if item.ID == "execution-00010" {
			found = true
		}
	}
	if !found {
		t.Fatal("old reply lost exact Run")
	}
	raw, _ := json.Marshal(activity)
	for _, secret := range []string{"PRIVATE_ORIGINAL_OUTPUT", "PRIVATE_INSTRUCTIONS", "PRIVATE_FILE_BODY"} {
		if strings.Contains(string(raw), secret) {
			t.Fatal("original leaked", secret)
		}
	}
	full, err := s.GetRun(ctx, "execution-00010")
	if err != nil || full.Output != "PRIVATE_ORIGINAL_OUTPUT" || full.Files[0].After != "PRIVATE_FILE_BODY" {
		t.Fatal("original changed", err)
	}
	t.Logf("10,000 messages / 1,000 executions: recent, earlier, later, direct target and activity in %s; activity %d bytes", time.Since(start), len(raw))
	// Global Room metadata can change after this response. The accepted page's
	// certified frontier is immutable, including when opening an older window.
	_, err = s.SendMessage(ctx, room.ID, SendMessageInput{Content: "Later original", RequestID: "later", PostOnly: true}, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	if recent.Through != 10000 {
		t.Fatal("accepted frontier advanced")
	}
	around, err = s.RoomMessageWindow(ctx, room.ID, "", MessageWindowPosition{Around: "history-00011"})
	if err != nil || !around.HasNewer || around.Through != 10001 {
		t.Fatal("old window became latest", err)
	}
	assertScalar(t, s, "SELECT count(*) FROM messages", 10001)
	assertScalar(t, s, "SELECT count(*) FROM runs", 1000)
}

func TestThreadWindowsReadRootSeparatelyAndBoundWholeMessages(t *testing.T) {
	s, room, _ := roomFixture(t)
	root, err := s.SendMessage(ctx, room.ID, SendMessageInput{Content: "Root original", RequestID: "root", PostOnly: true}, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 60; i++ {
		_, err = s.SendMessage(ctx, room.ID, SendMessageInput{Content: strings.Repeat("\\\"", 4000), RequestID: fmt.Sprint(i), PostOnly: true, ThreadRootID: root.Message.ID}, roomBudget)
		if err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.RoomMessageWindow(ctx, room.ID, root.Message.ID, MessageWindowPosition{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(page)
	if len(raw)+1 > MessageWindowBytes || !page.HasOlder || page.HasNewer || page.Messages[0].ID != root.Message.ID || page.Messages[len(page.Messages)-1].Seq != 61 {
		t.Fatal("thread or byte bound", len(raw), len(page.Messages))
	}
	for _, m := range page.Messages[1:] {
		if m.Content != strings.Repeat("\\\"", 4000) {
			t.Fatal("truncated original")
		}
	}
	target := page.Messages[1]
	older, err := s.RoomMessageWindow(ctx, room.ID, root.Message.ID, MessageWindowPosition{Before: target.Seq})
	if err != nil || older.Messages[0].ID != root.Message.ID || !older.HasNewer {
		t.Fatal("root missing on older page", err)
	}
	_, err = s.RoomMessageWindow(ctx, room.ID, "", MessageWindowPosition{Around: target.ID})
	requireCode(t, err, "not_found")
	other, err := s.CreateRoom(ctx, Room{Name: "Other", ResourceID: room.ResourceID})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.RoomMessageWindow(ctx, other.ID, root.Message.ID, MessageWindowPosition{})
	requireCode(t, err, "not_found")
	activity, err := s.ConversationRuns(ctx, other.ID, []string{root.Message.ID})
	if err != nil || len(activity.Items) != 0 {
		t.Fatal("cross Room activity", err)
	}
}

func TestRoomTaskPagesKeepAttentionDiscoverableAndAvoidFrozenInputs(t *testing.T) {
	s, room, _ := roomFixture(t)
	admin, _ := teamUsers(t, s)
	now := time.Now().UTC().Format(time.RFC3339Nano)
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		for i := 0; i < 1000; i++ {
			id := fmt.Sprintf("task-%04d", i)
			status := "completed"
			if i == 0 {
				status = "waiting_for_person"
			}
			task := CollaborationTask{ID: id, RoomID: room.ID, RequestedByUserID: actorID(admin), Status: status, Revision: 1, Goal: "Prepare notes", Question: &TaskQuestion{Text: "Choose a date"}, Participants: []TaskParticipant{}, Inputs: []TaskInput{{Content: strings.Repeat("PRIVATE_INPUT ", 100)}}}
			raw, _ := json.Marshal(task)
			if err := q.CreateTask(ctx, sqlgen.CreateTaskParams{ID: id, RoomID: room.ID, RequestedByUserID: actorID(admin), Status: status, Revision: 1, Snapshot: string(raw), CreatedAt: now, UpdatedAt: now}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	page, err := s.RoomTasksPage(ctx, room.ID, "", false)
	if err != nil || len(page.Tasks) != 20 || page.NextCursor == "" {
		t.Fatal("task page", err)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "PRIVATE_INPUT") {
		t.Fatal("task originals leaked")
	}
	next, err := s.RoomTasksPage(ctx, room.ID, page.NextCursor, false)
	if err != nil || len(next.Tasks) != 20 || string(next.Tasks[0]) == string(page.Tasks[0]) {
		t.Fatal("task seek", err)
	}
	attention, err := s.RoomTasksPage(ctx, room.ID, "", true)
	if err != nil || len(attention.Tasks) != 1 || !strings.Contains(string(attention.Tasks[0]), "task-0000") {
		t.Fatal("old unresolved task hidden", err)
	}
	_, err = s.RoomTasksPage(ctx, room.ID, page.NextCursor, true)
	requireCode(t, err, "invalid_page")
}
