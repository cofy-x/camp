package storage

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
)

func TestExplicitTaskCommandsShareCanonicalJournalWithoutAutomaticAdmission(t *testing.T) {
	s, actor, member, room, writer, _ := taskFixture(t)
	testRoomAutomationPolicy(t, s, actor, room, writer, "auto")
	task := startTask(t, s, actor, room, writer)
	input := TaskOperation{RequestID: "canonical-input", Content: "Source text remains in its original task input."}
	var err error
	task, err = s.OperateTask(member, task.ID, "inputs", input)
	if err != nil {
		t.Fatal(err)
	}
	finishTaskTestRound(t, s, task, "", CoordinationIntent{CallID: newID(), Kind: "ask_person", Question: "Which approach?"})
	task, _ = s.GetTaskState(ctx, task.ID)
	task, err = s.OperateTask(member, task.ID, "answers", TaskOperation{RequestID: "canonical-answer", Revision: task.Revision, QuestionID: task.Question.ID, Content: "Use the first approach."})
	if err != nil {
		t.Fatal(err)
	}
	finishTaskTestRound(t, s, task, "", CoordinationIntent{CallID: newID(), Kind: "ask_person", Question: "Is the direction approved?"})
	task, _ = s.GetTaskState(ctx, task.ID)
	task, err = s.OperateTask(actor, task.ID, "continue", TaskOperation{RequestID: "canonical-continue", Revision: task.Revision})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.OperateTask(actor, task.ID, "cancel", TaskOperation{RequestID: "canonical-cancel", Revision: task.Revision}); err != nil {
		t.Fatal(err)
	}
	// Replays locate the original receipt even after cancellation, without a
	// second canonical command, Message or authorization.
	if _, err = s.OperateTask(member, task.ID, "inputs", input); err != nil {
		t.Fatal(err)
	}
	legacy, err := s.OrchestrationPage(actor, room.ID, 0, nil, 20)
	if err != nil || len(legacy.Items) != 5 {
		t.Fatal(legacy, err)
	}
	page, err := s.AutomationEvents(actor, room.ID, 0, nil, 20)
	if err != nil || len(page.Items) != 5 {
		t.Fatal(page, err)
	}
	for index, record := range legacy.Items {
		event := page.Items[index]
		expected := map[string]string{"accept": "accepted", "context": "context", "stop": "stopped"}[record.Decision.Outcome]
		if event.ID != "task:"+record.Trigger.EventID || event.SourceID != "task" || event.Status != expected || event.TaskID != task.ID || event.Request == nil || *event.Request != *record.Trigger.Request || event.ActorID != record.Trigger.ActorID || event.MessageID == "" || event.Content != "" {
			t.Fatal("canonical command lost receipt, source identity or truthful decision", event, record)
		}
	}
	if candidate, err := s.ClaimAutomationEvaluation(ctx, "unused", roomBudget); err != nil || candidate != nil {
		t.Fatal("an explicit task command entered automatic admission", candidate, err)
	}
	assertScalar(t, s, "SELECT count(*) FROM automation_evaluations", 0)
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 1)
}

func TestAutomationEvaluationViewIsFrozenSafeAndDoesNotExecute(t *testing.T) {
	s, actor, member, room, writer, _ := taskFixture(t)
	policy := testRoomAutomationPolicy(t, s, actor, room, writer, "observe")
	sendAutomationDiscussion(t, s, actor, room, "Private source body excluded from the decision view.")
	candidate := claimAutomation(t, s)
	running, err := s.GetAutomationEvaluation(member, candidate.ID)
	if err != nil || running.Assessment != nil || running.Status != "running" || running.Policy.Revision != policy.Revision || running.ModelRevision != candidate.ModelRevision {
		t.Fatal(running, err)
	}
	encoded, _ := json.Marshal(running)
	for _, private := range []string{"Private source body", "runtimeKey", "credentialRef", "taskFrontier"} {
		if strings.Contains(string(encoded), private) {
			t.Fatal("private execution state or source text escaped", private)
		}
	}
	assertScalar(t, s, "SELECT count(*) FROM automation_evaluations WHERE status='running'", 1)
	assertScalar(t, s, "SELECT sum(evaluations) FROM automation_usage", 1)
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
	assessment := orchestration.Assessment{Outcome: "accept", Goal: "Prepare a faithful document.", Reason: "The configured rules permit the request."}
	if err = s.CompleteAutomationEvaluation(ctx, candidate.ID, assessment); err != nil {
		t.Fatal(err)
	}
	finished, err := s.GetAutomationEvaluation(member, candidate.ID)
	if err != nil || finished.Status != "observed" || finished.Assessment == nil || *finished.Assessment != assessment || finished.Policy.Rules != policy.Rules {
		t.Fatal(finished, err)
	}
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
	if _, err = s.db.Exec("UPDATE users SET enabled=0 WHERE id=?", actorID(member)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.GetAutomationEvaluation(member, candidate.ID); err == nil {
		t.Fatal("disabled member read decision history")
	}
}

func TestAutomationEvaluationViewBoundsWholeTaskSummaries(t *testing.T) {
	s, actor, member, room, writer, _ := taskFixture(t)
	testRoomAutomationPolicy(t, s, actor, room, writer, "observe")
	sendAutomationDiscussion(t, s, actor, room, "Assess this new request.")
	candidate := claimAutomation(t, s)
	for index := range 40 {
		candidate.Tasks = append(candidate.Tasks, orchestration.TaskSummary{ID: fmt.Sprintf("task-%d", index), Goal: strings.Repeat("<", 8192), Status: "completed", Revision: 1})
	}
	stored, _ := json.Marshal(candidate)
	if _, err := s.db.Exec("UPDATE automation_evaluations SET candidate=? WHERE id=?", string(stored), candidate.ID); err != nil {
		t.Fatal(err)
	}
	view, err := s.GetAutomationEvaluation(member, candidate.ID)
	encoded, _ := json.Marshal(view)
	if err != nil || len(encoded)+1 > automationEvaluationViewBytes || !view.TasksOmitted || view.TotalTasks != 40 || len(view.Tasks) == 0 || len(view.Tasks) >= 40 || view.Tasks[0].Goal != candidate.Tasks[0].Goal {
		t.Fatal("decision history exceeded its limit or truncated a goal", len(encoded), len(view.Tasks), view.TotalTasks, err)
	}
}
