package storage

import (
	"database/sql"
	"reflect"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func TestWorkTaskOriginFreezesKnownRequesterNameAndReceipt(t *testing.T) {
	s, admin, _, room, writer, _ := taskFixture(t)
	requester, _ := UserFromContext(admin)
	in := orchestration.WorkItemInput{RequestID: newID(), Goal: "Build a notes application.", Start: true, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID}
	w, err := s.CreateWorkItemWithBudget(admin, room.ID, in, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.GetTaskState(admin, w.ActiveTaskID)
	if err != nil {
		t.Fatal(err)
	}
	origin, err := s.GetMessage(admin, task.OriginMessageID)
	if err != nil || origin.AuthorKind != "user" || origin.AuthorUserID != requester.ID || origin.AuthorName != requester.Name || origin.Content != in.Goal || origin.TaskID != task.ID {
		t.Fatal("new Work origin lost its authenticated author snapshot", origin, err)
	}
	// A profile change cannot rewrite an earlier snapshot or its creation
	// receipt. A later origin takes its name from the current trusted user row.
	const renamed = "Renamed owner"
	if _, err = s.db.ExecContext(admin, "UPDATE users SET name=? WHERE id=?", renamed, requester.ID); err != nil {
		t.Fatal(err)
	}
	replayed, err := s.CreateWorkItemWithBudget(admin, room.ID, in, roomBudget)
	if err != nil || replayed.ActiveTaskID != task.ID {
		t.Fatal("profile change reinterpreted a creation receipt", replayed, err)
	}
	in.RequestID = newID()
	second, err := s.CreateWorkItemWithBudget(admin, room.ID, in, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	secondTask, err := s.GetTaskState(admin, second.ActiveTaskID)
	if err != nil {
		t.Fatal(err)
	}
	secondOrigin, err := s.GetMessage(admin, secondTask.OriginMessageID)
	if err != nil || secondOrigin.AuthorName != renamed {
		t.Fatal("new origin did not snapshot the known current requester", secondOrigin, err)
	}
	original, err := s.GetMessage(admin, task.OriginMessageID)
	if err != nil || !reflect.DeepEqual(original, origin) {
		t.Fatal("new origin rewrote earlier attribution", original, err)
	}
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 2)
}

func TestTaskOriginNameDoesNotGuessAnotherActorOrReplaceSnapshot(t *testing.T) {
	s, admin, member, room, writer, _ := taskFixture(t)
	requester, _ := UserFromContext(admin)
	other, _ := UserFromContext(member)
	for _, sample := range []struct {
		name, kind, authorID, authorName, expected string
	}{
		{"known requester", "user", requester.ID, "", requester.Name},
		{"different actor", "user", other.ID, "", ""},
		{"automatic origin", "system", "", "", ""},
		{"recorded attribution", "user", requester.ID, "Recorded name", "Recorded name"},
	} {
		t.Run(sample.name, func(t *testing.T) {
			var task CollaborationTask
			err := s.transaction(admin, func(tx *sql.Tx) error {
				var err error
				task, err = createTaskTx(admin, sqlgen.New(tx), room.ID, CreateTaskInput{Goal: "Prepare a document.", ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID}, roomBudget, taskCreation{RequesterUserID: requester.ID, OriginAuthorKind: sample.kind, OriginAuthorUserID: sample.authorID, OriginAuthorName: sample.authorName, MaxTurns: 6, MaxModelRequests: 24})
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			origin, err := s.GetMessage(admin, task.OriginMessageID)
			if err != nil || origin.AuthorName != sample.expected || origin.AuthorKind != sample.kind || origin.AuthorUserID != sample.authorID {
				t.Fatal("origin attribution was guessed or replaced", origin, err)
			}
		})
	}
}

func TestMessageWorkOriginIsExactReadProjectionAndKeepsUnknownHistory(t *testing.T) {
	s, admin, _, room, writer, _ := taskFixture(t)
	in := orchestration.WorkItemInput{RequestID: newID(), Goal: "Build a notes application.", Start: true, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID}
	w, err := s.CreateWorkItemWithBudget(admin, room.ID, in, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.GetTaskState(admin, w.ActiveTaskID)
	if err != nil {
		t.Fatal(err)
	}
	// Represent a retained pre-fix origin whose original author name is unknown.
	if _, err = s.db.ExecContext(admin, "UPDATE messages SET author_name='' WHERE id=?", task.OriginMessageID); err != nil {
		t.Fatal(err)
	}
	_, err = s.OperateTask(admin, task.ID, "inputs", TaskOperation{RequestID: newID(), InputProtocol: 1, PagedResponse: true, Content: "Use a small, readable layout."})
	if err != nil {
		t.Fatal(err)
	}
	thread, err := s.ListMessages(admin, room.ID, task.ThreadRootID, 0)
	if err != nil || len(thread) != 2 {
		t.Fatal("could not read the origin and user input", thread, err)
	}
	for _, message := range thread {
		if message.ID == task.OriginMessageID {
			if message.WorkOrigin == nil || message.WorkOrigin.WorkItemID != w.ID || message.AuthorName != "" || message.AuthorKind != "user" || message.Content != in.Goal {
				t.Fatal("origin projection changed original attribution or content", message)
			}
		} else if message.TaskID != task.ID || message.WorkOrigin != nil || message.AuthorKind != "user" {
			t.Fatal("user input was classified as a Work origin", message)
		}
	}
	origin, err := s.GetMessage(admin, task.OriginMessageID)
	if err != nil || origin.WorkOrigin == nil || origin.WorkOrigin.WorkItemID != w.ID || origin.AuthorName != "" {
		t.Fatal("single-message read lost exact origin proof", origin, err)
	}
	// Identical text does not establish origin identity or a Work relationship.
	discussion := sendAutomationDiscussion(t, s, admin, room, in.Goal)
	plain, err := s.GetMessage(admin, discussion.Message.ID)
	if err != nil || plain.WorkOrigin != nil {
		t.Fatal("ordinary discussion was inferred from matching text", plain, err)
	}
	stored, err := s.queries().GetMessage(admin, task.OriginMessageID)
	if err != nil || stored.AuthorName != "" || stored.AuthorKind != "user" {
		t.Fatal("reading backfilled historical author data", stored, err)
	}
}
