package storage

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
)

func TestAutomaticWorkRequiresAnExplicitModeAndCompatibleFiniteGrant(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	input := orchestration.WorkItemInput{RequestID: "automatic-once", Goal: "Save the verified release notes", CompletionMode: "automatic", Start: true, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID}
	_, err := s.CreateWorkItemWithBudget(actor, room.ID, input, roomBudget)
	requireCode(t, err, "runner_unavailable")
	assertScalar(t, s, "SELECT count(*) FROM work_items", 0)
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
	if _, err = s.db.Exec("UPDATE runners SET capabilities=json_set(capabilities,'$.workItemCompletionVersion',1,'$.taskInputsVersion',1)"); err != nil {
		t.Fatal(err)
	}
	work, err := s.CreateWorkItemWithBudget(actor, room.ID, input, roomBudget)
	if err != nil || work.CompletionMode != "automatic" || work.CompletionCriteria != input.Goal {
		t.Fatal(work, err)
	}
	task, err := s.GetTaskState(ctx, work.ActiveTaskID)
	if err != nil || !task.WorkItemCompletionAuthorized || task.WorkItemCompletionCriteria != work.CompletionCriteria {
		t.Fatal(task, err)
	}
	replay, err := s.CreateWorkItemWithBudget(actor, room.ID, input, Budget{})
	if err != nil || replay.ID != work.ID || replay.ActiveTaskID != task.ID {
		t.Fatal("creation did not replay its exact grant", replay, err)
	}
	changed := input
	changed.CompletionMode = "human"
	_, err = s.CreateWorkItemWithBudget(actor, room.ID, changed, roomBudget)
	requireCode(t, err, "request_conflict")
	invalid := input
	invalid.RequestID = newID()
	invalid.CompletionMode = "anything"
	_, err = s.CreateWorkItemWithBudget(actor, room.ID, invalid, roomBudget)
	requireCode(t, err, "invalid_work_item")
}

func TestAutomaticWorkJudgmentAppliesOnlyAfterPublication(t *testing.T) {
	for _, met := range []bool{true, false} {
		t.Run(map[bool]string{true: "met", false: "not-met"}[met], func(t *testing.T) {
			s, actor, _, room, writer, _ := taskFixture(t)
			if _, err := s.db.Exec("UPDATE runners SET capabilities=json_set(capabilities,'$.workItemCompletionVersion',1,'$.taskInputsVersion',1)"); err != nil {
				t.Fatal(err)
			}
			work, err := s.CreateWorkItemWithBudget(actor, room.ID, orchestration.WorkItemInput{RequestID: newID(), Goal: "Prepare the checked document", CompletionMode: "automatic", Start: true, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID}, roomBudget)
			if err != nil {
				t.Fatal(err)
			}
			task, _ := s.GetTaskState(ctx, work.ActiveTaskID)
			reason := "The saved document was read back and all configured checks passed."
			if !met {
				reason = "The required customer answer is missing."
			}
			finishTaskTestRound(t, s, task, strings.Repeat("b", 40), CoordinationIntent{CallID: newID(), Kind: "finish", Summary: "The exact document is saved.", WorkItemCompletion: &WorkItemCompletion{CriteriaMet: met, Reason: reason}})
			pending, err := s.GetWorkItem(actor, work.ID)
			if err != nil || pending.Status != "running" {
				t.Fatal("an unpublished result closed its Work item", pending, err)
			}
			for range 2 {
				if err = s.ReconcileTask(ctx, task.ID); err != nil {
					t.Fatal(err)
				}
			}
			result, err := s.GetWorkItem(actor, work.ID)
			if err != nil || result.Result == nil || !result.Result.Published || result.Usage.Tasks != 1 || result.Usage.ReservedModelRequests != 24 {
				t.Fatal(result, err)
			}
			if met {
				if result.Status != "completed" || result.PendingCompletion != nil {
					t.Fatal(result)
				}
			} else if result.Status != "waiting_for_person" || result.QuestionKind != orchestration.WorkQuestionJudgment || !strings.Contains(result.Question, reason) {
				t.Fatal(result)
			}
			assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 1)
			assertScalar(t, s, "SELECT count(*) FROM work_item_evidence WHERE kind='task.result'", 1)
		})
	}
}

func TestAutomaticWorkWaitsForEveryReceiptWithoutReplayingOrOverwritingJudgment(t *testing.T) {
	for _, change := range []string{"none", "human-wait", "context", "expiry"} {
		t.Run(change, func(t *testing.T) {
			s, actor, w, task, _ := workActionFixtureWithMode(t, "automatic")
			if w.Status != "waiting" || w.PendingCompletion == nil {
				t.Fatal("completion ignored queued final deliveries", w)
			}
			claimed := []orchestration.Delivery{}
			for range 2 {
				delivery, err := s.ClaimAutomationDelivery(ctx)
				if err != nil || delivery == nil {
					t.Fatal(delivery, err)
				}
				claimed = append(claimed, *delivery)
			}
			w, _ = s.GetWorkItem(actor, w.ID)
			if change == "human-wait" {
				w = decideTestWork(t, s, actor, w, orchestration.WorkItemOperation{Kind: "wait", WaitCondition: "Wait for the board decision", ResumeAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)})
			} else if change == "context" {
				room, _ := s.GetRoom(actor, w.RoomID)
				message := sendAutomationDiscussion(t, s, actor, room, "The acceptance requirements changed.").Message
				w = decideTestWork(t, s, actor, w, orchestration.WorkItemOperation{Kind: "evidence", EvidenceIDs: []string{"room:" + message.ID}})
			} else if change == "expiry" {
				if _, err := s.db.Exec("UPDATE work_items SET value=json_set(value,'$.expiresAt','2020-01-01T00:00:00Z','$.deadline','2020-01-01T00:00:00Z') WHERE id=?", w.ID); err != nil {
					t.Fatal(err)
				}
			}
			for index := range claimed {
				completeTestWorkAction(t, s, &claimed[index], "sent", int64(700+index))
				if index == 0 {
					current, _ := s.GetWorkItem(actor, w.ID)
					if current.Status == "completed" {
						t.Fatal("the first receipt closed a two-destination matter")
					}
				}
			}
			current, err := s.GetWorkItem(actor, w.ID)
			if err != nil {
				t.Fatal(err)
			}
			if change == "none" {
				if current.Status != "completed" || current.PendingCompletion != nil {
					t.Fatal(current)
				}
			} else if current.Status == "completed" {
				t.Fatal("a late receipt reused an obsolete completion judgment", change, current)
			}
			if change == "human-wait" && current.WaitCondition != "Wait for the board decision" {
				t.Fatal("a receipt replaced the person's waiting condition", current)
			}
			assertScalar(t, s, "SELECT count(*) FROM automation_deliveries", 2)
			assertScalar(t, s, "SELECT count(*) FROM work_action_receipts", 2)
			assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 1)
			original, _ := s.GetTaskState(ctx, task.ID)
			encoded, _ := json.Marshal(original.WorkItemCompletion)
			if !strings.Contains(string(encoded), "configured delivery receipts") {
				t.Fatal("the original model judgment was changed", string(encoded))
			}
		})
	}
}

func TestWorkCompletionSettingsPreserveQuestionsAndDoNotAdmitExecution(t *testing.T) {
	s, actor, _, room, _, _ := taskFixture(t)
	work, err := s.CreateWorkItem(actor, room.ID, orchestration.WorkItemInput{RequestID: newID(), Goal: "Deliver the saved plan"})
	if err != nil {
		t.Fatal(err)
	}
	if work.CompletionMode != "human" {
		t.Fatal("legacy creation was silently promoted", work)
	}
	work = decideTestWork(t, s, actor, work, orchestration.WorkItemOperation{Kind: "request_judgment", Question: "Which audience should this address?"})
	input := orchestration.WorkItemOperation{RequestID: "completion-settings", Revision: work.Revision, Kind: "completion", CompletionMode: "automatic", Reason: "Use the saved goal"}
	changed, err := s.OperateWorkItem(actor, work.ID, input, Budget{})
	if err != nil || changed.CompletionMode != "automatic" || changed.CompletionCriteria != work.Goal || changed.Question != work.Question || changed.QuestionKind != work.QuestionKind || changed.Budget != work.Budget || changed.Usage != work.Usage {
		t.Fatal("completion settings changed existing responsibility or grant", changed, err)
	}
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
	replay, err := s.OperateWorkItem(actor, work.ID, input, Budget{})
	if err != nil || replay.Revision != changed.Revision {
		t.Fatal("settings did not replay exact receipt", replay, err)
	}
	input.CompletionMode = "human"
	_, err = s.OperateWorkItem(actor, work.ID, input, Budget{})
	requireCode(t, err, "request_conflict")
	facts, err := s.WorkItemHistory(actor, work.ID, "evidence", "", 20)
	if err != nil || len(facts.Items) != 1 {
		t.Fatal("settings evidence missing", facts, err)
	}
	original, err := s.GetAutomationEvent(actor, changed.Evidence[len(changed.Evidence)-1].EventID)
	if err != nil || original.Kind != "workitem.completion_settings" || !strings.Contains(original.Content, work.Goal) {
		t.Fatal(original, err)
	}
}

func TestAutomaticWorkRechecksRunnerSupportBeforeDispatch(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	if _, err := s.db.Exec("UPDATE runners SET capabilities=json_set(capabilities,'$.workItemCompletionVersion',1,'$.taskInputsVersion',1)"); err != nil {
		t.Fatal(err)
	}
	work, err := s.CreateWorkItemWithBudget(actor, room.ID, orchestration.WorkItemInput{RequestID: newID(), Goal: "Save a checked file", CompletionMode: "automatic", Start: true, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID}, roomBudget)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.GetTaskState(ctx, work.ActiveTaskID)
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.GetRun(ctx, task.ActiveRunID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE runners SET capabilities=json_set(capabilities,'$.workItemCompletionVersion',0) WHERE id=?", run.RunnerID); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.ClaimRemoteRun(ctx, run.ID, run.RunnerID, time.Minute)
	requireCode(t, err, "runner_unavailable")
	assertScalar(t, s, "SELECT count(*) FROM run_attempts", 0)
}
