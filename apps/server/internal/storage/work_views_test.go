package storage

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func TestWorkViewPinsResultAndAnswersAtTheMatter(t *testing.T) {
	s, admin, _, room, writer, _ := taskFixture(t)
	w := createTestWork(t, s, admin, room)
	w = decideTestWork(t, s, admin, w, orchestration.WorkItemOperation{Kind: "execute", ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID})
	if w.Result != nil {
		t.Fatal("queued execution presented its starting files as a saved result", w.Result)
	}
	task, err := s.GetTaskState(admin, w.ActiveTaskID)
	if err != nil {
		t.Fatal(err)
	}
	enableTaskInputs(t, s, task.Participants[0].RunnerID)
	checkpoint := strings.Repeat("b", 40)
	run, _ := finishTaskTestRound(t, s, task, checkpoint, CoordinationIntent{CallID: newID(), Kind: "ask_person", Question: "Include the preview feature?", Options: []string{"Include", "Leave out"}})
	if err := s.transaction(admin, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		for index := range 23 {
			run.Files = append(run.Files, RunFile{Path: fmt.Sprintf("release-%02d.md", index), Status: "added"})
		}
		return saveRun(admin, q, run)
	}); err != nil {
		t.Fatal(err)
	}
	w, err = s.GetWorkItem(admin, w.ID)
	if err != nil || w.Execution == nil || w.Execution.TaskID != task.ID || w.Execution.Status != "waiting_for_person" || w.Execution.Question == nil || w.Execution.Question.Text != "Include the preview feature?" || w.Execution.CurrentAgentName != writer.Name {
		t.Fatal("matter lost its execution question", w.Execution, err)
	}
	if w.Result == nil || w.Result.Commit != checkpoint || w.Result.Published || w.Result.ResourceID != room.ResourceID || len(w.Result.Files) != 20 || !w.Result.FilesLimited || w.Result.Files[0].Path != "release-00.md" {
		t.Fatal("result did not retain bounded exact draft files", w.Result)
	}
	inbox, err := s.ListInbox(admin)
	if err != nil || len(inbox) != 1 || inbox[0].WorkItemID != w.ID || inbox[0].Title != w.Goal || inbox[0].Question != "Include the preview feature?" {
		t.Fatal("notification did not identify the matter and actual question", inbox, err)
	}
	// The projection is a view, never part of the matter's saved state.
	raw, err := s.queries().GetWorkItem(admin, w.ID)
	if err != nil || strings.Contains(raw.Value, `"execution"`) || strings.Contains(raw.Value, `"result"`) {
		t.Fatal("execution projection was persisted into the matter", raw.Value, err)
	}
	task, err = s.GetTaskState(admin, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.OperateTask(admin, task.ID, "answers", TaskOperation{RequestID: newID(), Revision: task.Revision, QuestionID: task.Question.ID, Content: "Leave it out.", InputProtocol: 1, PagedResponse: true}); err != nil {
		t.Fatal(err)
	}
	inbox, err = s.ListInbox(admin)
	if err != nil || inbox[0].Question != "" {
		t.Fatal("answered question still advertised as actionable", inbox, err)
	}
	w, err = s.GetWorkItem(admin, w.ID)
	if err != nil || w.Result == nil || w.Result.Commit != checkpoint || w.Result.Published || w.Execution.Status != "queued" {
		t.Fatal("continuation replaced the saved draft projection", w.Result, w.Execution, err)
	}
}

func TestWorkListFiltersBeforePagingAndKeepsCurrentTaskQuestions(t *testing.T) {
	s, admin, _, room, writer, _ := taskFixture(t)
	questionWork := createTestWork(t, s, admin, room)
	questionWork = decideTestWork(t, s, admin, questionWork, orchestration.WorkItemOperation{Kind: "execute", ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID})
	task, err := s.GetTaskState(admin, questionWork.ActiveTaskID)
	if err != nil {
		t.Fatal(err)
	}
	enableTaskInputs(t, s, task.Participants[0].RunnerID)
	finishTaskTestRound(t, s, task, "", CoordinationIntent{CallID: newID(), Kind: "ask_person", Question: "Which audience?"})
	for range 22 {
		w := createTestWork(t, s, admin, room)
		decideTestWork(t, s, admin, w, orchestration.WorkItemOperation{Kind: "cancel"})
	}
	attention, err := s.WorkItemsFiltered(admin, room.ID, 0, 1, "needs_attention")
	if err != nil || len(attention.Items) != 1 || attention.HasMore || attention.Items[0].ID != questionWork.ID || attention.Items[0].Execution.Question.Text != "Which audience?" {
		t.Fatal("filter was applied after the page boundary", attention, err)
	}
	first, err := s.WorkItemsFiltered(admin, room.ID, 0, 20, "done")
	if err != nil || len(first.Items) != 20 || !first.HasMore {
		t.Fatal(first, err)
	}
	second, err := s.WorkItemsFiltered(admin, room.ID, first.NextAfter, 20, "done")
	if err != nil || len(second.Items) != 2 || second.HasMore || second.Items[0].Seq >= first.NextAfter {
		t.Fatal(second, err)
	}
	for _, invalid := range []string{"unknown", "completed"} {
		_, err := s.WorkItemsFiltered(admin, room.ID, 0, 20, invalid)
		requireCode(t, err, "invalid_page")
	}
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 1)
}
