package storage

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestTaskModelRefreshIsExplicitAndRetainsEarlierRunsAndReceipts(t *testing.T) {
	s, admin, member, room, writer, reviewer := taskFixture(t)
	task := startTask(t, s, admin, room, writer, reviewer)
	first, err := s.GetRun(admin, task.ActiveRunID)
	if err != nil {
		t.Fatal(err)
	}
	first, attempt := prepareTaskTestAttempt(t, s, first)
	if err = s.BeginAttemptModel(admin, attempt.ID, first.RunnerID); err != nil {
		t.Fatal(err)
	}
	if err = s.EndAttemptModel(admin, attempt.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.InterruptAttempt(admin, attempt.ID, "The model response reached its output limit."); err != nil {
		t.Fatal(err)
	}
	paused, err := s.GetTask(admin, task.ID)
	if err != nil || paused.Status != "needs_attention" {
		t.Fatal("task did not retain a recoverable failed execution", err)
	}
	fresh := Budget{RequireRunner: true, Model: "current-model", RuntimeKey: "current-private-revision", MaxTokens: 2048, MaxSteps: 16, TimeoutSeconds: 600}
	plain := TaskOperation{RequestID: newID(), Revision: paused.Revision}
	kept, err := s.OperateTaskWithModel(admin, paused.ID, "continue", plain, fresh)
	if err != nil || !reflect.DeepEqual(kept.Budget, paused.Budget) {
		t.Fatal("ordinary Continue silently changed model settings", err)
	}
	second, err := s.GetRun(admin, kept.ActiveRunID)
	if err != nil || second.RuntimeKey != first.RuntimeKey || second.MaxTokens != first.MaxTokens {
		t.Fatal("ordinary Continue changed its original per-request grant", err)
	}
	second, nextAttempt := prepareTaskTestAttempt(t, s, second)
	if _, err = s.InterruptAttempt(admin, nextAttempt.ID, "Explicit configuration refresh is required."); err != nil {
		t.Fatal(err)
	}
	paused, err = s.GetTask(admin, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	in := TaskOperation{RequestID: newID(), Revision: paused.Revision, RefreshModelSettings: true}
	_, err = s.OperateTaskWithModel(member, task.ID, "continue", in, fresh)
	requireCode(t, err, "forbidden")
	_, err = s.OperateTask(admin, task.ID, "continue", in)
	requireCode(t, err, "invalid_task")
	updated, err := s.OperateTaskWithModel(admin, task.ID, "continue", in, fresh)
	if err != nil || !reflect.DeepEqual(updated.Budget, fresh) {
		t.Fatal("explicit current settings were not frozen", err)
	}
	if updated.MaxTurns != paused.MaxTurns || updated.MaxModelRequests != paused.MaxModelRequests || updated.ModelRequestsUsed != paused.ModelRequestsUsed || updated.TurnsUsed != paused.TurnsUsed {
		t.Fatal("refresh changed cumulative allowance or charged extra model requests")
	}
	current, err := s.GetRun(admin, updated.ActiveRunID)
	if err != nil || current.RuntimeKey != fresh.RuntimeKey || current.Model != fresh.Model || current.MaxTokens != fresh.MaxTokens || current.MaxSteps != fresh.MaxSteps || current.TimeoutSeconds != fresh.TimeoutSeconds {
		t.Fatal("new execution did not retain its refreshed model grant", err)
	}
	old, err := s.GetRun(admin, first.ID)
	if err != nil || old.RuntimeKey != first.RuntimeKey || old.Model != first.Model || old.MaxTokens != first.MaxTokens {
		t.Fatal("refresh rewrote an earlier execution", err)
	}
	// An uncertain response is recovered even if today's model is unavailable.
	replay, err := s.OperateTaskWithModel(admin, task.ID, "continue", in, Budget{})
	retained := fresh
	retained.RequireRunner = false // Admission-only hint, excluded from persisted settings.
	if err != nil || replay.ActiveRunID != updated.ActiveRunID || !reflect.DeepEqual(replay.Budget, retained) || replay.TurnsUsed != updated.TurnsUsed {
		t.Fatal("replaying the accepted authorization used newer settings or ran twice", err)
	}
	changed := in
	changed.RefreshModelSettings = false
	_, err = s.OperateTaskWithModel(admin, task.ID, "continue", changed, fresh)
	requireCode(t, err, "request_conflict")
	page, err := s.OrchestrationPage(admin, room.ID, 0, nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, record := range page.Items {
		if record.Trigger.Request != nil && record.Trigger.Request.ID == in.RequestID {
			found = len(record.Mandates) == 1 && record.Mandates[0].MaxTokens == fresh.MaxTokens && record.Mandates[0].MaxModelRequests == updated.MaxModelRequests
		}
	}
	if !found {
		t.Fatal("refreshed continuation did not append its finite admission evidence")
	}
}

func TestLegacyTaskOperationsOmitModelRefreshFromCanonicalBody(t *testing.T) {
	data, err := json.Marshal(TaskOperation{RequestID: "legacy"})
	if err != nil || strings.Contains(string(data), "refreshModelSettings") || strings.Contains(string(data), "fromRunId") {
		t.Fatal("legacy receipts changed their canonical request body", err)
	}
}

func TestChangedReasoningRevisionCannotResumeSameModelSession(t *testing.T) {
	s, room, _ := roomFixture(t)
	budget := roomBudget
	budget.Model = "same-model"
	budget.RuntimeKey = "private-config-with-thinking-off"
	send := func(request string) Run {
		t.Helper()
		result, err := sendToAssistant(s, ctx, room.ID, SendMessageInput{RequestID: request, Content: "Continue."}, budget)
		if err != nil {
			t.Fatal(err)
		}
		run, err := s.ClaimRun(ctx, result.Runs[0].ID)
		if err != nil {
			t.Fatal(err)
		}
		return run
	}
	first := send("off")
	session, err := s.GetConversation(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	first.SessionID, first.SessionGeneration, first.Status = session.SessionID, session.Generation, "succeeded"
	if _, err = s.FinishRun(ctx, first, "Accepted complete result.", true); err != nil {
		t.Fatal(err)
	}
	budget.RuntimeKey = "private-config-with-provider-default"
	second := send("provider-default")
	next, err := s.GetConversation(ctx, second)
	if err != nil || next.Resumable || next.Generation != session.Generation+1 || next.SessionID == session.SessionID || second.Model != first.Model {
		t.Fatal("a changed reasoning configuration resumed the earlier same-model session", err)
	}
}
