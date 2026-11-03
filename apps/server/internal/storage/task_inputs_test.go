package storage

import (
	"database/sql"
	"encoding/json"

	"fmt"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
	"strings"
	"sync"
	"testing"
)

func TestTaskInputFinalPageCountsNullCursor(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	task := startTask(t, s, actor, room, writer)
	u, _ := UserFromContext(actor)
	inputs := []TaskInput{
		{ID: newID(), Seq: 1, AuthorUserID: u.ID, AuthorName: u.Name, CreatedAt: "2026-11-02T00:00:00Z", Content: strings.Repeat("<", 8192)},
		{ID: newID(), Seq: 2, AuthorUserID: u.ID, AuthorName: u.Name, CreatedAt: "2026-11-02T00:00:00Z"},
	}
	full := TaskInputPage{Items: inputs, Through: 2}
	encoded, err := json.Marshal(full)
	if err != nil {
		t.Fatal(err)
	}
	padding := TaskInputPageBytes - len(encoded)
	// HTML escaping expands '<' to six JSON bytes while originals remain within
	// the per-input limit. The complete final response is exactly one byte over.
	inputs[1].Content = strings.Repeat("<", padding/6) + strings.Repeat("x", padding%6)
	if len(inputs[1].Content) > MaxRunPromptSize {
		t.Fatal("fixture exceeded the original-input limit")
	}
	if err := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		for _, input := range inputs {
			seq, err := q.NextMessageSequence(ctx, room.ID)
			if err != nil {
				return err
			}
			m := Message{ID: input.ID, TaskID: task.ID, RoomID: room.ID, ThreadRootID: task.ThreadRootID, Seq: seq, AuthorKind: "user", AuthorUserID: u.ID, AuthorName: u.Name, Content: input.Content, CreatedAt: input.CreatedAt, UpdatedAt: input.CreatedAt}
			if err := insertMessage(ctx, q, m, ""); err != nil {
				return err
			}
			data, err := json.Marshal(input)
			if err != nil {
				return err
			}
			if err := q.InsertTaskInput(ctx, sqlgen.InsertTaskInputParams{ID: input.ID, TaskID: task.ID, Seq: input.Seq, ContentBytes: int64(len(input.Content)), Value: string(data)}); err != nil {
				return err
			}
			task.InputRevision = input.Seq
			task.InputBytes += int64(len(input.Content))
		}
		return saveTask(ctx, q, &task)
	}); err != nil {
		t.Fatal(err)
	}
	page, err := s.TaskInputsPage(ctx, task.ID, 0, nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = json.Marshal(page)
	if err != nil || len(encoded)+1 > TaskInputPageBytes || !page.HasMore || page.NextAfter == nil {
		t.Fatal("final input page exceeded its wire budget", len(encoded)+1, page.HasMore, err)
	}
	next, err := s.TaskInputsPage(ctx, task.ID, *page.NextAfter, &page.Through, 20)
	if err != nil || next.HasMore || next.NextAfter != nil || len(page.Items) != 1 || len(next.Items) != 1 || page.Items[0] != inputs[0] || next.Items[0] != inputs[1] {
		t.Fatal("byte-bound input continuation changed an original", page, next, err)
	}
}

func TestTaskInputLogConcurrentAppendAndReceiptFrontier(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	task := startTask(t, s, actor, room, writer)
	first := TaskOperation{RequestID: "lost-response", Content: "Keep the original constraint."}
	receipt, err := s.OperateTask(actor, task.ID, "inputs", first)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := range 24 {
		wg.Go(func() {
			_, err := s.OperateTask(actor, task.ID, "inputs", TaskOperation{RequestID: fmt.Sprintf("append-%d", i), Content: fmt.Sprintf("Input %d", i), PagedResponse: true})
			if err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	current, err := s.GetTaskState(ctx, task.ID)
	if err != nil || current.InputRevision != 25 || len(current.Inputs) != 0 {
		t.Fatal(current, err)
	}
	var snapshot string
	if err := s.db.QueryRow("SELECT snapshot FROM collaboration_tasks WHERE id=?", task.ID).Scan(&snapshot); err != nil || strings.Contains(snapshot, first.Content) {
		t.Fatal("originals remained embedded in task metadata", err)
	}
	inputs, err := s.TaskInputs(ctx, task.ID, 0, current.InputRevision)
	if err != nil || len(inputs) != 25 {
		t.Fatal(inputs, err)
	}
	for i, input := range inputs {
		message, err := s.GetMessage(ctx, input.MessageID)
		if err != nil || input.Seq != int64(i+1) || message.TaskID != task.ID || message.Content != input.Content || message.AuthorUserID != input.AuthorUserID || message.CreatedAt != input.CreatedAt {
			t.Fatal("log/message identity diverged", input, message, err)
		}
	}
	replayed, err := s.OperateTask(actor, task.ID, "inputs", first)
	if err != nil || replayed.InputRevision != 1 || replayed.Revision != receipt.Revision || len(replayed.Inputs) != 1 || replayed.Inputs[0] != receipt.Inputs[0] {
		t.Fatal("receipt was reconstructed from today's frontier", replayed, err)
	}
	first.PagedResponse = true // Presentation is not part of the request hash.
	summary, err := s.OperateTask(actor, task.ID, "inputs", first)
	if err != nil || summary.InputRevision != 1 || summary.Inputs != nil {
		t.Fatal(summary, err)
	}
	first.Content = "A different constraint."
	if _, err := s.OperateTask(actor, task.ID, "inputs", first); err == nil {
		t.Fatal("changed request body reused an accepted identity")
	}
}

func TestTaskInputPagesFreezeBoundsAndCountEncodedBytes(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	task := startTask(t, s, actor, room, writer)
	// JSON escaping makes each stored input much larger on the wire.
	for i := range 8 {
		if _, err := s.OperateTask(actor, task.ID, "inputs", TaskOperation{RequestID: fmt.Sprint(i), Content: strings.Repeat("<", 8192), PagedResponse: true}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.TaskInputsPage(ctx, task.ID, 0, nil, 20)
	if err != nil || len(page.Items) != 1 || page.Through != 8 || !page.HasMore || page.NextAfter == nil {
		t.Fatal(page, err)
	}
	seen := []TaskInput{}
	for {
		data, err := json.Marshal(page)
		if err != nil || len(data)+1 > TaskInputPageBytes {
			t.Fatal("page exceeds its encoded wire bound", len(data), err)
		}
		seen = append(seen, page.Items...)
		if !page.HasMore {
			break
		}
		page, err = s.TaskInputsPage(ctx, task.ID, *page.NextAfter, &page.Through, 20)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 8 || page.NextAfter != nil {
		t.Fatal("input was omitted or duplicated", seen, page)
	}
	end := int64(4)
	frozen, err := s.TaskInputsPage(ctx, task.ID, 3, &end, 1)
	if err != nil || len(frozen.Items) != 1 || frozen.Items[0].Seq != 4 || frozen.HasMore {
		t.Fatal(frozen, err)
	}
	empty, err := s.TaskInputsPage(ctx, task.ID, 8, nil, 20)
	if err != nil || len(empty.Items) != 0 || empty.HasMore || empty.NextAfter != nil {
		t.Fatal(empty, err)
	}
	for _, after := range []int64{-1, 9} {
		if _, err := s.TaskInputsPage(ctx, task.ID, after, nil, 20); err == nil {
			t.Fatal("accepted an invalid page frontier", after)
		}
	}
	if _, err := s.TaskInputsPage(ctx, task.ID, 0, nil, 21); err == nil {
		t.Fatal("accepted an oversized page")
	}
	if _, err := s.OperateTask(actor, task.ID, "inputs", TaskOperation{RequestID: "over-budget", Content: "more", PagedResponse: true}); err == nil {
		t.Fatal("the additive protocol lifted lifetime admission limits")
	}
	assertScalar(t, s, "SELECT count(*) FROM task_inputs", 8)
}

func TestTaskRunPagesUseScopedKeysetAndBoundedSummaries(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	task := startTask(t, s, actor, room, writer)
	other := startTask(t, s, actor, room, writer)
	// Distinct rounds with identical timestamps exercise the ID tie breaker.
	var snapshot string
	if err := s.db.QueryRow("SELECT snapshot FROM runs WHERE id=?", task.ActiveRunID).Scan(&snapshot); err != nil {
		t.Fatal(err)
	}
	for i := range 24 {
		var run Run
		if err := json.Unmarshal([]byte(snapshot), &run); err != nil {
			t.Fatal(err)
		}
		run.ID = fmt.Sprintf("history-%02d", i)
		run.Output = strings.Repeat("large output", 1000)
		raw, _ := json.Marshal(run)
		message, err := s.SendMessage(actor, room.ID, SendMessageInput{RequestID: "history-message-" + run.ID, Content: "History fixture", PostOnly: true}, roomBudget)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.queries().CreateRun(ctx, sqlgen.CreateRunParams{ID: run.ID, RoomID: run.RoomID, ThreadRootID: nullableParent(run.ThreadRootID), OriginMessageID: message.Message.ID, AgentID: run.AgentID, ResourceID: run.ResourceID, Status: run.Status, Snapshot: string(raw), CreatedAt: run.CreatedAt, UpdatedAt: run.UpdatedAt, RequestedByUserID: run.RequestedByUserID}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.TaskRunsPage(ctx, task.ID, "", 20)
	if err != nil || len(first.Items) != 20 || first.NextCursor == "" || first.Items[0].AgentName != writer.Name {
		t.Fatal(first, err)
	}
	second, err := s.TaskRunsPage(ctx, task.ID, first.NextCursor, 20)
	if err != nil || len(second.Items) != 5 || second.NextCursor != "" {
		t.Fatal(second, err)
	}
	seen := map[string]bool{}
	for _, item := range append(first.Items, second.Items...) {
		if seen[item.ID] {
			t.Fatal("repeated a history row", item)
		}
		seen[item.ID] = true
	}
	if _, err := s.TaskRunsPage(ctx, other.ID, first.NextCursor, 20); err == nil {
		t.Fatal("accepted another task's cursor")
	}
	if _, err := s.TaskRunsPage(ctx, task.ID, strings.Repeat("x", 1000), 20); err == nil {
		t.Fatal("accepted an oversized cursor")
	}
}

func TestTaskFailedRoundDoesNotCoverInputs(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	task := startTask(t, s, actor, room, writer)
	if _, err := s.OperateTask(actor, task.ID, "inputs", TaskOperation{RequestID: "constraint", Content: "Must survive a failed round."}); err != nil {
		t.Fatal(err)
	}
	run, _ := s.GetRun(ctx, task.ActiveRunID)
	run, attempt := prepareTaskTestAttempt(t, s, run)
	run.Status = "failed"
	run.Error = "The fixture did not complete."
	if _, err := s.FinishRemoteRun(ctx, attempt.ID, run.RunnerID, "failed-receipt", strings.Repeat("e", 64), run, "", false, true); err != nil {
		t.Fatal(err)
	}
	stopped, err := s.GetTaskState(ctx, task.ID)
	if err != nil || stopped.CoveredInputRevision != 0 || stopped.InputRevision != 1 || stopped.ProcessedInputRevision != 1 {
		t.Fatal(stopped, err)
	}
	continued, err := s.OperateTask(actor, task.ID, "continue", TaskOperation{RequestID: "continue", Revision: stopped.Revision})
	if err != nil {
		t.Fatal(err)
	}
	finishTaskTestRound(t, s, continued, continued.CheckpointCommit, CoordinationIntent{CallID: "accepted", Kind: "ask_person", Question: "Is this checkpoint acceptable?"})
	covered, err := s.GetTaskState(ctx, task.ID)
	if err != nil || covered.CoveredInputRevision != 1 {
		t.Fatal(covered, err)
	}
}

func TestLegacyTaskMutationPreflightFollowsReceiptResolution(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	task := startTask(t, s, actor, room, writer)
	in := TaskOperation{RequestID: "accepted", Content: "Original."}
	accepted, err := s.OperateTask(actor, task.ID, "inputs", in)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a task admitted by a later large-history protocol. Old clients
	// must fail before mutation, while accepted old receipts stay recoverable.
	if _, err := s.db.Exec("UPDATE collaboration_tasks SET snapshot=json_set(snapshot,'$.inputRevision',129) WHERE id=?", task.ID); err != nil {
		t.Fatal(err)
	}
	replayed, err := s.OperateTask(actor, task.ID, "inputs", in)
	if err != nil || replayed.Revision != accepted.Revision || len(replayed.Inputs) != 1 {
		t.Fatal(replayed, err)
	}
	_, err = s.OperateTask(actor, task.ID, "cancel", TaskOperation{RequestID: "new-action", Revision: accepted.Revision})
	requireCode(t, err, "task_pagination_required")
	current, _ := s.GetTaskState(ctx, task.ID)
	if current.Status != task.Status || current.Revision != accepted.Revision {
		t.Fatal("legacy preflight changed task", current)
	}
	assertScalar(t, s, "SELECT count(*) FROM task_requests WHERE request_id='new-action'", 0)
}
