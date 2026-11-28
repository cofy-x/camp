package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func TestWorkSnapshotSeparatesSavedStateFromOriginalResponse(t *testing.T) {
	// Older snapshots included empty history fields. A retained response can
	// also contain the view as it looked when the command was accepted.
	legacy := `{"goal":"Deliver the brief","deadline":"2028-01-01T00:00:00Z","evidence":null,"actions":[],"execution":{"taskId":"previous"},"currentDecision":null}`
	w, err := workItemRow(sqlgen.WorkItem{ID: "matter", Status: "waiting", Revision: 2, Value: legacy})
	if err != nil || w.Goal != "Deliver the brief" || w.ExpiresAt != "2028-01-01T00:00:00Z" || w.Execution != nil {
		t.Fatal(w, err)
	}
	w.CompletionWait = orchestration.WorkWaitForDelivery
	w.Result = &orchestration.WorkResult{TaskID: "original"}
	w.HistoryLimited = true
	raw, err := workItemSnapshot(w)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]json.RawMessage
	if err = json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"completionWait", "execution", "result", "currentDecision", "githubDelivery", "historyLimited", "evidence", "decisions", "tasks", "timers", "actions", "watches"} {
		if _, exists := fields[key]; exists {
			t.Fatalf("display field %q entered the durable Work state", key)
		}
	}
	receipt, err := readWorkReceipt(legacy)
	if err != nil || receipt.Execution == nil || receipt.Execution.TaskID != "previous" || receipt.CompletionWait != "" {
		t.Fatal("legacy receipt lost its original view", receipt, err)
	}
}

func TestWorkCurrentDecisionSharesQuestionAndCompletionAcrossReads(t *testing.T) {
	s, owner, teammate, room, writer, _ := taskFixture(t)
	work, err := s.CreateWorkItem(owner, room.ID, orchestration.WorkItemInput{
		RequestID: newID(), Goal: "Prepare the reviewed brief.", CompletionMode: "human",
	})
	if err != nil {
		t.Fatal(err)
	}
	work = decideTestWork(t, s, owner, work, orchestration.WorkItemOperation{
		Kind: "execute", ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID,
	})
	task, err := s.GetTaskState(owner, work.ActiveTaskID)
	if err != nil {
		t.Fatal(err)
	}
	enableTaskInputs(t, s, task.Participants[0].RunnerID)
	finishTaskTestRound(t, s, task, "", CoordinationIntent{
		CallID: newID(), Kind: "ask_person", Question: "Which scope?", Options: []string{"Small", "Large"},
	})
	task, err = s.GetTaskState(owner, task.ID)
	if err != nil || task.Question == nil {
		t.Fatal("collaboration lost its current question", err)
	}
	assertDecision := func(want *orchestration.WorkCurrentDecision) orchestration.WorkItem {
		t.Helper()
		current, err := s.GetCurrentWorkItem(owner, work.ID)
		if err != nil || !reflect.DeepEqual(current.CurrentDecision, want) {
			t.Fatalf("current Work decision = %+v, want %+v, err = %v", current.CurrentDecision, want, err)
		}
		full, err := s.GetWorkItem(owner, work.ID)
		if err != nil || full.Status != current.Status || !reflect.DeepEqual(full.CurrentDecision, want) {
			t.Fatal("full Work detail disagreed with current", full.CurrentDecision, err)
		}
		list, err := s.ListWorkItems(owner, WorkListOptions{View: "all", RoomID: room.ID})
		if err != nil || len(list.Items) != 1 || list.Items[0].Status != current.Status || !reflect.DeepEqual(list.Items[0].CurrentDecision, want) {
			t.Fatal("Work list disagreed with current", list, err)
		}
		legacyList, err := s.WorkItems(owner, room.ID, 0, 1)
		if err != nil || len(legacyList.Items) != 1 || !reflect.DeepEqual(legacyList.Items[0].CurrentDecision, want) {
			t.Fatal("Room Work list disagreed with current", legacyList, err)
		}
		attention := attentionFor(t, s, owner, AttentionOptions{RoomID: room.ID, Kind: "judgment"})
		if want != nil && (len(attention.Items) != 1 || attention.Items[0].Summary != want.Text || !reflect.DeepEqual(attention.Items[0].CurrentDecision, want)) {
			t.Fatal("personal attention disagreed with current", attention)
		}
		if want == nil && len(attention.Items) != 0 {
			t.Fatal("settled question remained in personal attention", attention)
		}
		return current
	}
	work = assertDecision(&orchestration.WorkCurrentDecision{
		Kind: "task", ID: task.Question.ID, Text: task.Question.Text, Options: task.Question.Options,
		TaskID: task.ID, TaskRevision: task.Revision,
	})
	if work.Status != "running" {
		t.Fatal("projection changed the durable Work lifecycle", work.Status)
	}
	other := attentionFor(t, s, teammate, AttentionOptions{RoomID: room.ID, Kind: "judgment"})
	if len(other.Items) != 0 {
		t.Fatal("projection reassigned someone else's decision", other.Items)
	}
	raw, err := s.queries().GetWorkItem(owner, work.ID)
	if err != nil || strings.Contains(raw.Value, `"currentDecision"`) {
		t.Fatal("a read projection entered durable Work state", err)
	}
	answer := orchestration.WorkItemOperation{
		Kind: "answer", RequestID: newID(), Revision: work.Revision, TaskID: task.ID, TaskRevision: task.Revision,
		QuestionID: task.Question.ID, Content: "Choose Small.",
	}
	answered, err := s.OperateWorkItem(owner, work.ID, answer, roomBudget)
	if err != nil || answered.CurrentDecision != nil {
		t.Fatal("answer left a stale decision on its receipt", answered.CurrentDecision, err)
	}
	assertDecision(nil)
	task, err = s.GetTaskState(owner, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	finishTaskTestRound(t, s, task, strings.Repeat("b", 40), CoordinationIntent{CallID: newID(), Kind: "finish", Summary: "Saved the brief."})
	if err = s.ReconcileTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	work = assertDecision(&orchestration.WorkCurrentDecision{Kind: "completion", Text: "Is this work complete?", Options: []string{}})
	if work.ActiveTaskID != "" || work.Execution == nil || work.Execution.Status != "completed" {
		t.Fatal("completed execution and human confirmation were conflated", work.Execution)
	}
	receipt, err := s.WorkItemRequest(owner, answer.RequestID)
	if err != nil || receipt.Revision != answered.Revision || receipt.CurrentDecision != nil {
		t.Fatal("answer receipt was rewritten to the later completion decision", receipt.CurrentDecision, err)
	}
	decideTestWork(t, s, owner, work, orchestration.WorkItemOperation{Kind: "complete"})
	completed := assertDecision(nil)
	encoded, err := json.Marshal(completed)
	if err != nil || !strings.Contains(string(encoded), `"currentDecision":null`) {
		t.Fatal("absent pending decisions must be explicit on current reads", err)
	}
}

func TestWorkViewReadsMetadataWithoutFrozenContentAndKeepsExactFacts(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	w := createTestWork(t, s, actor, room)
	w = decideTestWork(t, s, actor, w, orchestration.WorkItemOperation{Kind: "execute", ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID})
	task, err := s.GetTaskState(actor, w.ActiveTaskID)
	if err != nil {
		t.Fatal(err)
	}
	enableTaskInputs(t, s, task.Participants[0].RunnerID)
	commit := strings.Repeat("b", 40)
	run, _ := finishTaskTestRound(t, s, task, commit, CoordinationIntent{CallID: newID(), Kind: "ask_person", Question: "Which scope?", Options: []string{"Small", "Large"}})
	task, err = s.GetTaskState(actor, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	task.Summary, task.Error = "Saved a first draft.", "A current execution detail."
	task.Participants[0].Agent.Instructions = strings.Repeat("PRIVATE_INSTRUCTIONS", 4000)
	task.References = []Reference{{Kind: "message", Content: strings.Repeat("PRIVATE_REFERENCE", 4000)}}
	run.Output = strings.Repeat("PRIVATE_OUTPUT", 18000)
	for index := range 25 {
		run.Files = append(run.Files, RunFile{Path: fmt.Sprintf("src/%02d.txt", index), Status: "added", After: strings.Repeat("PRIVATE_FILE", 1000)})
	}
	if err = s.transaction(actor, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if err := saveRun(actor, q, run); err != nil {
			return err
		}
		return saveTask(actor, q, &task)
	}); err != nil {
		t.Fatal(err)
	}
	taskBefore, _ := s.queries().GetTask(actor, task.ID)
	runBefore, _ := s.queries().GetRun(actor, run.ID)
	executionRaw, err := s.queries().WorkExecutionView(actor, sqlgen.WorkExecutionViewParams{ActiveTaskID: task.ID, WorkItemID: w.ID})
	if err != nil {
		t.Fatal(err)
	}
	resultRaw, err := s.queries().WorkResultView(actor, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(executionRaw)+len(resultRaw) > 4096 || strings.Contains(executionRaw+resultRaw, "PRIVATE_") {
		t.Fatal("metadata read returned frozen content", len(executionRaw), len(resultRaw))
	}
	want := orchestration.WorkExecution{TaskID: task.ID, Status: task.Status, Revision: task.Revision, Summary: task.Summary, Error: task.Error, CurrentAgentName: writer.Name, ActiveRunID: task.ActiveRunID, UpdatedAt: task.UpdatedAt, Question: &orchestration.WorkTaskQuestion{ID: task.Question.ID, Text: task.Question.Text, Options: task.Question.Options, Answer: task.Question.Answer}}
	detail, err := s.GetWorkItem(actor, w.ID)
	if err != nil || !reflect.DeepEqual(detail.Execution, &want) || detail.Result == nil || detail.Result.Commit != commit || detail.Result.Published || len(detail.Result.Files) != 20 || !detail.Result.FilesLimited || detail.Result.Files[19].Path != "src/19.txt" {
		t.Fatal("targeted projection changed current facts", detail.Execution, detail.Result, err)
	}
	list, err := s.ListWorkItems(actor, WorkListOptions{Scope: "team", View: "all"})
	if err != nil || len(list.Items) != 1 || !reflect.DeepEqual(list.Items[0].Execution, detail.Execution) || !reflect.DeepEqual(list.Items[0].Result, detail.Result) {
		t.Fatal("list and detail diverged", list, err)
	}
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), "PRIVATE_") {
		t.Fatal("frozen content escaped the Work list")
	}
	taskAfter, _ := s.queries().GetTask(actor, task.ID)
	runAfter, _ := s.queries().GetRun(actor, run.ID)
	if taskBefore != taskAfter || runBefore != runAfter {
		t.Fatal("display reads modified frozen originals")
	}
}

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

func TestWorkViewKeepsDeclaredArtifactFromEarlierRound(t *testing.T) {
	s, actor, _, room, writer, reviewer := taskFixture(t)
	w := createTestWork(t, s, actor, room)
	w = decideTestWork(t, s, actor, w, orchestration.WorkItemOperation{Kind: "execute", ParticipantAgentIDs: []string{writer.ID, reviewer.ID}, FirstAgentID: writer.ID})
	task, err := s.GetTaskState(actor, w.ActiveTaskID)
	if err != nil {
		t.Fatal(err)
	}
	for _, participant := range task.Participants {
		enableTaskInputs(t, s, participant.RunnerID)
	}
	checkpoint := strings.Repeat("b", 40)
	finishTaskTestRound(t, s, task, checkpoint, CoordinationIntent{CallID: newID(), Kind: "handoff", TargetAgentID: reviewer.ID, Instruction: "Check the saved notebook.", ArtifactPaths: []string{"notes.mjs"}})
	task, err = s.GetTaskState(actor, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	finishTaskTestRound(t, s, task, "", CoordinationIntent{CallID: newID(), Kind: "handoff", TargetAgentID: writer.ID, Instruction: "Publish the checked version."})
	task, err = s.GetTaskState(actor, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	final, _ := finishTaskTestRound(t, s, task, checkpoint, CoordinationIntent{CallID: newID(), Kind: "finish", Summary: "Notebook verified.", ArtifactPaths: []string{"notes.mjs", "notes.mjs"}})
	if len(final.Files) != 0 {
		t.Fatal("final checks unexpectedly changed the checkpoint", final.Files)
	}
	if err := s.ReconcileTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	w, err = s.GetWorkItem(actor, w.ID)
	wantFiles := []orchestration.WorkResultFile{{Path: "notes.mjs", Status: "saved"}}
	if err != nil || w.Result == nil || !w.Result.Published || w.Result.Commit != checkpoint || !reflect.DeepEqual(w.Result.Files, wantFiles) {
		t.Fatal("Work must open the exact verified artifact even if the last round has an empty delta", w.Result, err)
	}
}

func TestWorkViewKeepsDeclaredReadOnlyArtifactAtItsBaseline(t *testing.T) {
	s, actor, _, room, _, reader := taskFixture(t)
	w := createTestWork(t, s, actor, room)
	w = decideTestWork(t, s, actor, w, orchestration.WorkItemOperation{Kind: "execute", ParticipantAgentIDs: []string{reader.ID}, FirstAgentID: reader.ID})
	task, err := s.GetTaskState(actor, w.ActiveTaskID)
	if err != nil {
		t.Fatal(err)
	}
	enableTaskInputs(t, s, task.Participants[0].RunnerID)
	finishTaskTestRound(t, s, task, "", CoordinationIntent{CallID: newID(), Kind: "finish", Summary: "Existing notebook checked.", ArtifactPaths: []string{"notes.mjs"}})
	if err := s.ReconcileTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	w, err = s.GetWorkItem(actor, w.ID)
	if err != nil || w.Result == nil || w.Result.Commit != task.BaseSharedCommit || !reflect.DeepEqual(w.Result.Files, []orchestration.WorkResultFile{{Path: "notes.mjs", Status: "saved"}}) {
		t.Fatal("read-only work lost the path verified against its exact baseline", w.Result, err)
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
