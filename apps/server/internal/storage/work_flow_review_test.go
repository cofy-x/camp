package storage

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func reviewWaitingWork(t *testing.T, s *Store, actor context.Context, room Room, writer Agent, timer bool) (orchestration.WorkItem, orchestration.Policy) {
	t.Helper()
	policy := testRoomAutomationPolicy(t, s, actor, room, writer, "auto")
	sendAutomationDiscussion(t, s, actor, room, "Prepare the release when the review arrives.")
	c := claimAutomation(t, s)
	a := orchestration.Assessment{Outcome: "wait", Goal: "Prepare the release", CompletionCriteria: "The reviewed release document is published.", WaitCondition: "The review arrives", Reason: "Wait for the recorded review before preparing the release."}
	if timer {
		a.ResumeAt = time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
	}
	if err := s.CompleteAutomationEvaluation(ctx, c.ID, a); err != nil {
		t.Fatal(err)
	}
	page, err := s.WorkItems(actor, room.ID, 0, 20)
	if err != nil || len(page.Items) != 1 {
		t.Fatal(page, err)
	}
	w, err := s.GetWorkItem(actor, page.Items[0].ID)
	if err != nil || w.Status != "waiting" || w.ActiveTaskID != "" {
		t.Fatal(w, err)
	}
	return w, policy
}
func reviewFireWorkTimer(t *testing.T, s *Store, w orchestration.WorkItem) {
	t.Helper()
	if _, err := s.db.Exec("UPDATE work_item_timers SET due_at=? WHERE work_item_id=? AND status='pending'", time.Now().Add(-time.Minute).UTC().Format(time.RFC3339Nano), w.ID); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Go(func() {
			if err := s.FireWorkItemTimers(ctx); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
}
func reviewPolicyInput(p orchestration.Policy) orchestration.PolicyInput {
	return orchestration.PolicyInput{RequestID: newID(), Revision: p.Revision, Enabled: p.Enabled, Mode: p.Mode, Rules: p.Rules, AllowedActorIDs: p.AllowedActorIDs, ParticipantAgentIDs: p.ParticipantAgentIDs, FirstAgentID: p.FirstAgentID, MaxTurns: p.MaxTurns, MaxModelRequests: p.MaxModelRequests, DailyEvaluations: p.DailyEvaluations, DailyTasks: p.DailyTasks, AllowAnswers: p.AllowAnswers, AllowComments: p.AllowComments}
}

func TestWorkFlowReviewTimerAssessmentFiniteTaskResultAndCompletion(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	w, _ := reviewWaitingWork(t, s, actor, room, writer, true)
	reviewFireWorkTimer(t, s, w)
	c := claimAutomation(t, s)
	if len(c.Events) != 1 || c.Events[0].Kind != "timer.fired" || c.Events[0].WorkItemID != w.ID {
		t.Fatal("timer did not produce targeted durable assessment evidence", c)
	}
	a := orchestration.Assessment{Outcome: "execute", WorkItemID: w.ID, Reason: "The follow-up confirms the release can be prepared."}
	for range 2 {
		if err := s.CompleteAutomationEvaluation(ctx, c.ID, a); err != nil {
			t.Fatal(err)
		}
	}
	w, err := s.GetWorkItem(actor, w.ID)
	if err != nil || w.Usage.Tasks != 1 || w.Usage.FollowUps != 1 || w.ActiveTaskID == "" {
		t.Fatal("timer retry duplicated execution or reset budget", w, err)
	}
	task, err := s.GetTaskState(ctx, w.ActiveTaskID)
	if err != nil {
		t.Fatal(err)
	}
	enableTaskInputs(t, s, task.Participants[0].RunnerID)
	finishTaskTestRound(t, s, task, strings.Repeat("b", 40), CoordinationIntent{CallID: newID(), Kind: "finish", Summary: "The reviewed release document is ready."})
	if err := s.ReconcileTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	result := claimAutomation(t, s)
	if result.Events[0].Kind != "task.result" || result.Events[0].CausationID != task.ID || result.Events[0].WorkItemID != w.ID {
		t.Fatal("task result was not immutable causal evidence", result)
	}
	if err := s.CompleteAutomationEvaluation(ctx, result.ID, orchestration.Assessment{Outcome: "complete", WorkItemID: w.ID, Reason: "The published release document satisfies the completion criteria."}); err != nil {
		t.Fatal(err)
	}
	w, err = s.GetWorkItem(actor, w.ID)
	if err != nil || w.Status != "completed" || w.ActiveTaskID != "" || w.Usage.Tasks != 1 || w.Usage.Evaluations != 3 {
		t.Fatal("closed loop did not finish exactly one bounded task", w, err)
	}
	if next, err := s.ClaimAutomationEvaluation(ctx, "model", roomBudget); err != nil || next != nil {
		t.Fatal("completion resumed work", next, err)
	}
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 1)
	assertScalar(t, s, "SELECT count(*) FROM automation_events WHERE source_id='timer'", 1)
}

func TestWorkFlowReviewWatchedFeishuEvidenceUsesOwningPolicyAndPreservesActor(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	w, origin := reviewWaitingWork(t, s, actor, room, writer, false)
	source, err := s.CreateFeishuSource(actor, room.ID, CreateFeishuSourceInput{RequestID: newID(), AppID: "cli_app", TenantKey: "tenant", ChatID: "oc_chat"}, feishuCredential)
	if err != nil {
		t.Fatal(err)
	}
	first, _, err := s.ReceiveFeishuDelivery(ctx, source.ID, feishuCredential, []byte(`{"encrypt":"first"}`), feishuTestPayload("first-delivery", "The external review conversation."))
	if err != nil {
		t.Fatal(err)
	}
	w = decideTestWork(t, s, actor, w, orchestration.WorkItemOperation{Kind: "evidence", EvidenceIDs: []string{"feishu:" + first.ID}})
	w = decideTestWork(t, s, actor, w, orchestration.WorkItemOperation{Kind: "follow", SourceID: source.ID, ThreadKey: first.ThreadKey()})
	plain := bytes.Replace(feishuTestPayload("review-delivery", "Review approved; prepare the release."), []byte("om_message"), []byte("om_review"), 1)
	second, _, err := s.ReceiveFeishuDelivery(ctx, source.ID, feishuCredential, []byte(`{"encrypt":"second"}`), plain)
	if err != nil {
		t.Fatal(err)
	}
	if _, duplicate, err := s.ReceiveFeishuDelivery(ctx, source.ID, feishuCredential, []byte(`{"encrypt":"other-IV"}`), plain); err != nil || !duplicate {
		t.Fatal("source replay was not deduplicated", err)
	}
	c := claimAutomation(t, s)
	if c.Policy.ID != origin.ID || len(c.Events) != 1 || c.Events[0].Kind != "workitem.evidence" || c.Events[0].WorkItemID != w.ID || c.Events[0].CausationID != "feishu:"+second.ID {
		t.Fatal("watched evidence did not follow the matter's original delegation", c)
	}
	original, err := s.GetAutomationEvent(actor, "feishu:"+second.ID)
	if err != nil || original.ActorKind != "user" || original.ActorID != "tenant:ou_author" || original.Feishu == nil || original.WorkItemID != "" || original.ExternalEventID != "" {
		t.Fatal("cross-source routing forged original identity", original, err)
	}
	if err := s.CompleteAutomationEvaluation(ctx, c.ID, orchestration.Assessment{Outcome: "execute", WorkItemID: w.ID, Reason: "The explicitly followed review satisfies the original waiting condition."}); err != nil {
		t.Fatal(err)
	}
	w, err = s.GetWorkItem(actor, w.ID)
	if err != nil || w.Status != "running" || w.Usage.Tasks != 1 {
		t.Fatal(w, err)
	}
	inputs, err := s.TaskInputsPage(actor, w.ActiveTaskID, 0, nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, input := range inputs.Items {
		if input.SourceEventID == original.ID {
			found = true
			if input.AuthorUserID != "" || input.ExternalActorID != "tenant:ou_author" {
				t.Fatal("external evidence became an authenticated Camp instruction", input)
			}
		}
	}
	if !found {
		t.Fatal("new finite task lost original external evidence", inputs)
	}
	assertScalar(t, s, "SELECT count(*) FROM work_items", 1)
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 1)
	assertScalar(t, s, "SELECT count(*) FROM automation_events WHERE kind='workitem.evidence'", 1)
}

func TestWorkFlowReviewUnfollowAndSourceDisableFenceInflightWatchedAssessment(t *testing.T) {
	for _, change := range []string{"unfollow", "disable"} {
		t.Run(change, func(t *testing.T) {
			s, actor, _, room, writer, _ := taskFixture(t)
			w, _ := reviewWaitingWork(t, s, actor, room, writer, false)
			source, err := s.CreateFeishuSource(actor, room.ID, CreateFeishuSourceInput{RequestID: newID(), AppID: "cli_app", TenantKey: "tenant", ChatID: "oc_chat"}, feishuCredential)
			if err != nil {
				t.Fatal(err)
			}
			first, _, err := s.ReceiveFeishuDelivery(ctx, source.ID, feishuCredential, []byte(`{"encrypt":"first"}`), feishuTestPayload("first-delivery", "Initial external context."))
			if err != nil {
				t.Fatal(err)
			}
			w = decideTestWork(t, s, actor, w, orchestration.WorkItemOperation{Kind: "evidence", EvidenceIDs: []string{"feishu:" + first.ID}})
			w = decideTestWork(t, s, actor, w, orchestration.WorkItemOperation{Kind: "follow", SourceID: source.ID, ThreadKey: first.ThreadKey()})
			plain := bytes.Replace(feishuTestPayload("followup-delivery", "Review ready."), []byte("om_message"), []byte("om_followup"), 1)
			if _, _, err := s.ReceiveFeishuDelivery(ctx, source.ID, feishuCredential, []byte(`{"encrypt":"second"}`), plain); err != nil {
				t.Fatal(err)
			}
			c := claimAutomation(t, s)
			if change == "unfollow" {
				w, err = s.GetWorkItem(actor, w.ID)
				if err != nil {
					t.Fatal(err)
				}
				decideTestWork(t, s, actor, w, orchestration.WorkItemOperation{Kind: "unfollow", SourceID: source.ID, ThreadKey: first.ThreadKey()})
			} else {
				if _, err := s.UpdateFeishuSource(actor, source.ID, UpdateFeishuSourceInput{RequestID: newID(), Revision: source.Revision, Enabled: new(false)}, ""); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.CompleteAutomationEvaluation(ctx, c.ID, orchestration.Assessment{Outcome: "execute", WorkItemID: w.ID, Reason: "An older assessment proposed execution."}); err != nil {
				t.Fatal(err)
			}
			row, err := s.queries().GetAutomationEvaluation(ctx, c.ID)
			if err != nil || row.Status != "needs_attention" {
				t.Fatal("changed watched authority admitted old work", row, err)
			}
			assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
		})
	}
}

func TestWorkFlowReviewExplicitRunAddsContextWithoutAnotherAssessment(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	w, _ := reviewWaitingWork(t, s, actor, room, writer, false)
	request, err := s.SendMessage(actor, room.ID, SendMessageInput{RequestID: newID(), Content: "@" + writer.Handle + " prepare the release explicitly.", RecipientAgentIDs: []string{writer.ID}, RunnerRevision: &room.RunnerRevision}, roomBudget)
	if err != nil || len(request.Runs) != 1 {
		t.Fatal(request, err)
	}
	if c, err := s.ClaimAutomationEvaluation(ctx, "unused", roomBudget); err != nil || c != nil {
		t.Fatal("explicit Run woke a second automatic task", c, err)
	}
	w, err = s.GetWorkItem(actor, w.ID)
	if err != nil || w.Status != "waiting" || len(w.Evidence) != 2 {
		t.Fatal("explicit work lost context or expanded matter authority", w, err)
	}
	assertScalar(t, s, "SELECT count(*) FROM automation_evaluations", 1)
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
}

func TestWorkFlowReviewBlockedTimerRequiresJudgmentAndFreshHumanAnswer(t *testing.T) {
	for _, change := range []string{"policy_off", "runner_revoked"} {
		t.Run(change, func(t *testing.T) {
			s, actor, _, room, writer, _ := taskFixture(t)
			w, policy := reviewWaitingWork(t, s, actor, room, writer, true)
			runner, err := s.ResolveAgentRunner(actor, room.ID, writer.ID)
			if err != nil {
				t.Fatal(err)
			}
			if change == "policy_off" {
				in := reviewPolicyInput(policy)
				in.Enabled = false
				policy, err = s.SaveAutomationPolicy(actor, room.ID, "room", in)
			} else {
				_, err = s.UpdateRunner(actor, runner.ID, RunnerUpdate{TeamShared: new(false)})
			}
			if err != nil {
				t.Fatal(err)
			}
			reviewFireWorkTimer(t, s, w)
			if c, err := s.ClaimAutomationEvaluation(ctx, "unused", roomBudget); err != nil || c != nil {
				t.Fatal("lost origin authority spent a model allowance", c, err)
			}
			w, err = s.GetWorkItem(actor, w.ID)
			if err != nil || w.Status != "needs_attention" || w.Question == "" {
				t.Fatal("blocked timer stranded a matter without actionable judgment", w, err)
			}
			assertScalar(t, s, "SELECT count(*) FROM automation_evaluations", 1)
			if change == "policy_off" {
				in := reviewPolicyInput(policy)
				in.Enabled = true
				_, err = s.SaveAutomationPolicy(actor, room.ID, "room", in)
			} else {
				_, err = s.UpdateRunner(actor, runner.ID, RunnerUpdate{TeamShared: new(true)})
			}
			if err != nil {
				t.Fatal(err)
			}
			if c, err := s.ClaimAutomationEvaluation(ctx, "unused", roomBudget); err != nil || c != nil {
				t.Fatal("restoring authority silently replayed blocked work", c, err)
			}
			w = decideTestWork(t, s, actor, w, orchestration.WorkItemOperation{Kind: "answer", Reason: "The review is complete. Resume assessment under the current policy."})
			c := claimAutomation(t, s)
			if c.Events[0].Kind != "workitem.answer" || c.Events[0].ActorKind != "user" || c.Events[0].ActorID != actorID(actor) || c.Events[0].WorkItemID != w.ID {
				t.Fatal("human answer lost its authenticated source proof", c)
			}
			if err := s.CompleteAutomationEvaluation(ctx, c.ID, orchestration.Assessment{Outcome: "complete", WorkItemID: w.ID, Reason: "The authenticated judgment and evidence satisfy the completion criteria."}); err != nil {
				t.Fatal(err)
			}
			current, err := s.GetWorkItem(actor, w.ID)
			if err != nil || current.Status != "completed" {
				t.Fatal(current, err)
			}
			assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
		})
	}
}

func TestWorkFlowReviewTaskCancellationCannotResumeThroughWatchedBacklog(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	w, _ := reviewWaitingWork(t, s, actor, room, writer, true)
	reviewFireWorkTimer(t, s, w)
	c := claimAutomation(t, s)
	if err := s.CompleteAutomationEvaluation(ctx, c.ID, orchestration.Assessment{Outcome: "execute", WorkItemID: w.ID, Reason: "Prepare the approved release."}); err != nil {
		t.Fatal(err)
	}
	w, err := s.GetWorkItem(actor, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := s.GetTaskState(ctx, w.ActiveTaskID)
	if err != nil {
		t.Fatal(err)
	}
	sendAutomationDiscussion(t, s, actor, room, "Old review context queued before cancellation.")
	task, err = s.GetTaskState(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err = s.OperateTask(actor, task.ID, "cancel", TaskOperation{RequestID: newID(), Revision: task.Revision, InputProtocol: 1, PagedResponse: true})
	if err != nil || task.Status != "cancelled" {
		t.Fatal(task, err)
	}
	w, err = s.GetWorkItem(actor, w.ID)
	if err != nil || w.Status != "needs_attention" || w.ActiveTaskID != "" {
		t.Fatal("task cancellation did not preserve a human decision boundary", w, err)
	}
	if c, err := s.ClaimAutomationEvaluation(ctx, "unused", roomBudget); err != nil || c != nil {
		t.Fatal("cancelled task resumed from old source backlog", c, err)
	}
	if err := s.FireWorkItemTimers(ctx); err != nil {
		t.Fatal(err)
	}
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 1)
	current, err := s.GetTaskState(ctx, task.ID)
	if err != nil || current.Status != "cancelled" {
		t.Fatal(current, err)
	}
}

func TestWorkFlowReviewUnreceiptedInternalMetadataHasNoAuthority(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	w, _ := reviewWaitingWork(t, s, actor, room, writer, false)
	if err := s.transaction(ctx, func(tx *sql.Tx) error {
		return insertWorkFact(ctx, sqlgen.New(tx), &w, orchestration.SourceEvent{ID: "timer:unreceipted", SourceID: "timer", RoomID: room.ID, ThreadKey: "workitem:" + w.ID, WorkItemID: w.ID, Kind: "timer.fired", ActorKind: "system", ActorID: "timer", Content: "Forged metadata cannot authorize execution.", Status: "pending", OccurredAt: now()})
	}); err != nil {
		t.Fatal(err)
	}
	if c, err := s.ClaimAutomationEvaluation(ctx, "unused", roomBudget); err != nil || c != nil {
		t.Fatal("unproven source metadata was trusted as a timer receipt", c, err)
	}
	assertScalar(t, s, "SELECT count(*) FROM automation_evaluations", 1)
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
}

func TestWorkFlowReviewNewWatchedEvidenceSupersedesOnlyUnchangedDelegation(t *testing.T) {
	for _, change := range []string{"evidence_only", "policy_changed", "human_decision"} {
		t.Run(change, func(t *testing.T) {
			s, actor, _, room, writer, _ := taskFixture(t)
			w, policy := reviewWaitingWork(t, s, actor, room, writer, false)
			source, err := s.CreateFeishuSource(actor, room.ID, CreateFeishuSourceInput{RequestID: newID(), AppID: "cli_app", TenantKey: "tenant", ChatID: "oc_chat"}, feishuCredential)
			if err != nil {
				t.Fatal(err)
			}
			first, _, err := s.ReceiveFeishuDelivery(ctx, source.ID, feishuCredential, []byte(`{"encrypt":"first"}`), feishuTestPayload("first-delivery", "Initial external context."))
			if err != nil {
				t.Fatal(err)
			}
			w = decideTestWork(t, s, actor, w, orchestration.WorkItemOperation{Kind: "evidence", EvidenceIDs: []string{"feishu:" + first.ID}})
			w = decideTestWork(t, s, actor, w, orchestration.WorkItemOperation{Kind: "follow", SourceID: source.ID, ThreadKey: first.ThreadKey()})
			plain := bytes.Replace(feishuTestPayload("first-followup", "The first review information."), []byte("om_message"), []byte("om_first_followup"), 1)
			if _, _, err := s.ReceiveFeishuDelivery(ctx, source.ID, feishuCredential, []byte(`{"encrypt":"followup"}`), plain); err != nil {
				t.Fatal(err)
			}
			old := claimAutomation(t, s)
			newer := bytes.Replace(feishuTestPayload("newer-followup", "The review was amended while assessment was in flight."), []byte("om_message"), []byte("om_newer"), 1)
			if _, _, err := s.ReceiveFeishuDelivery(ctx, source.ID, feishuCredential, []byte(`{"encrypt":"newer"}`), newer); err != nil {
				t.Fatal(err)
			}
			if change == "policy_changed" {
				in := reviewPolicyInput(policy)
				in.Rules += " Require another review."
				if _, err := s.SaveAutomationPolicy(actor, room.ID, "room", in); err != nil {
					t.Fatal(err)
				}
			}
			if change == "human_decision" {
				w, err = s.GetWorkItem(actor, w.ID)
				if err != nil {
					t.Fatal(err)
				}
				w = decideTestWork(t, s, actor, w, orchestration.WorkItemOperation{Kind: "ignore", Reason: "A person reviewed the pending proposal; do not silently reuse its authority."})
			}
			if err := s.CompleteAutomationEvaluation(ctx, old.ID, orchestration.Assessment{Outcome: "execute", WorkItemID: w.ID, Reason: "An older advisory response."}); err != nil {
				t.Fatal(err)
			}
			current, err := s.GetWorkItem(actor, w.ID)
			if err != nil {
				t.Fatal(err)
			}
			if change != "evidence_only" {
				if current.Status != "needs_attention" {
					t.Fatal("configuration or human judgment was superseded by source arrival", current)
				}
				assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
				return
			}
			if current.Status != "waiting" || current.Question != "" {
				t.Fatal("ordinary new evidence required an unnecessary human interruption", current)
			}
			found := false
			for _, decision := range current.Decisions {
				if decision.Kind == "superseded" && decision.EvaluationID == old.ID {
					found = true
				}
			}
			if !found {
				t.Fatal("superseded provider outcome lost its audit record")
			}
			next := claimAutomation(t, s)
			if next.ID == old.ID || len(next.Events) != 1 || next.Events[0].CausationID == old.Events[0].CausationID {
				t.Fatal("fresh evidence replayed the same provider candidate", old, next)
			}
			if err := s.CompleteAutomationEvaluation(ctx, next.ID, orchestration.Assessment{Outcome: "execute", WorkItemID: w.ID, Reason: "The new frozen review evidence permits the original finite goal."}); err != nil {
				t.Fatal(err)
			}
			current, err = s.GetWorkItem(actor, w.ID)
			if err != nil || current.Status != "running" || current.Usage.Evaluations != 3 || current.Usage.Tasks != 1 {
				t.Fatal("fresh follow-up did not make bounded progress", current, err)
			}
			assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 1)
		})
	}
}

func TestWorkFlowReviewDisabledOwningPolicyRetainsArrivalForHumanJudgment(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	w, p := reviewWaitingWork(t, s, actor, room, writer, false)
	in := reviewPolicyInput(p)
	in.Enabled = false
	p, err := s.SaveAutomationPolicy(actor, room.ID, "room", in)
	if err != nil {
		t.Fatal(err)
	}
	sendAutomationDiscussion(t, s, actor, room, "A review arrived after the owning delegation was disabled.")
	w, err = s.GetWorkItem(actor, w.ID)
	if err != nil || w.Status != "needs_attention" || w.Question == "" {
		t.Fatal("disabled policy stranded watched evidence", w, err)
	}
	if c, err := s.ClaimAutomationEvaluation(ctx, "unused", roomBudget); err != nil || c != nil {
		t.Fatal("disabled delegation made a provider request", c, err)
	}
	in = reviewPolicyInput(p)
	in.Enabled = true
	if _, err := s.SaveAutomationPolicy(actor, room.ID, "room", in); err != nil {
		t.Fatal(err)
	}
	if c, err := s.ClaimAutomationEvaluation(ctx, "unused", roomBudget); err != nil || c != nil {
		t.Fatal("Auto restoration replayed old watched evidence", c, err)
	}
	assertScalar(t, s, "SELECT count(*) FROM automation_evaluations", 1)
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
}

func TestWorkFlowReviewManualWaitArrivalNotifiesOnceWithoutGrantingWork(t *testing.T) {
	s, actor, _, room, _, _ := taskFixture(t)
	first := sendAutomationDiscussion(t, s, actor, room, "A manually tracked matter.")
	w := createTestWork(t, s, actor, room, "room:"+first.Message.ID)
	w = decideTestWork(t, s, actor, w, orchestration.WorkItemOperation{Kind: "follow", SourceID: "room", ThreadKey: room.ID + ":room"})
	w = decideTestWork(t, s, actor, w, orchestration.WorkItemOperation{Kind: "wait", WaitCondition: "A person reviews the new evidence.", ResumeAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)})
	input := SendMessageInput{RequestID: newID(), Content: "New evidence for the manually tracked matter."}
	for range 2 {
		if _, err := s.SendMessage(actor, room.ID, input, roomBudget); err != nil {
			t.Fatal(err)
		}
	}
	current, err := s.GetWorkItem(actor, w.ID)
	if err != nil || current.Status != "waiting" || current.WaitCondition != w.WaitCondition || len(current.Timers) != 1 || current.Timers[0].Status != "pending" || current.Usage.Evaluations != 0 || current.Usage.Actions != 0 {
		t.Fatal("context arrival changed a manual wait or spent authority", current, err)
	}
	inbox, err := s.ListInbox(actor)
	if err != nil || len(inbox) != 1 || inbox[0].WorkItemID != w.ID {
		t.Fatal("manual wait evidence did not create one causal notice", inbox, err)
	}
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
	assertScalar(t, s, "SELECT count(*) FROM automation_evaluations", 0)
}

func TestWorkFlowReviewSourceFanOutDoesNotLoseTheHundredAndFirstMatter(t *testing.T) {
	s, actor, _, room, _, _ := taskFixture(t)
	first := sendAutomationDiscussion(t, s, actor, room, "A conversation followed by many independent matters.")
	if err := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		for range 101 {
			w, err := insertWorkItem(ctx, q, room.ID, actorID(actor), orchestration.WorkItemInput{Goal: "An independent matter.", CompletionCriteria: "Review its own evidence.", EvidenceIDs: []string{"room:" + first.Message.ID}})
			if err != nil {
				return err
			}
			if err := q.SaveWorkWatch(ctx, sqlgen.SaveWorkWatchParams{WorkItemID: w.ID, SourceID: "room", ThreadKey: room.ID + ":room", Enabled: 1}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	arrival := sendAutomationDiscussion(t, s, actor, room, "Evidence relevant to every explicitly followed matter.")
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM work_item_evidence WHERE event_id=?", "room:"+arrival.Message.ID).Scan(&count); err != nil || count != 101 {
		t.Fatal("source intake silently omitted part of its explicit fan-out", count, err)
	}
}

func TestWorkFlowReviewDispatchChargeBeforeReceiptPreservesBoundedCompletion(t *testing.T) {
	s, actor, w, task, _ := workActionFixture(t)
	// Restore the actual publication-result lane before any external POST starts.
	// The sender then claims its durable action while this assessment is frozen.
	if err := s.queries().UpdateAutomationEvent(ctx, sqlgen.UpdateAutomationEventParams{ID: "task-result:" + task.ID, Status: "pending"}); err != nil {
		t.Fatal(err)
	}
	old := claimAutomation(t, s)
	if len(old.Events) != 1 || old.Events[0].Kind != "task.result" || old.WorkItems[0].Usage.Actions != 0 {
		t.Fatal("fixture did not freeze the publication result before dispatch", old)
	}
	first, err := s.ClaimAutomationDelivery(ctx)
	if err != nil || first == nil {
		t.Fatal(first, err)
	}
	afterClaim, err := s.GetWorkItem(actor, w.ID)
	if err != nil || afterClaim.Usage.Actions != 1 {
		t.Fatal("dispatch did not reserve its finite action allowance", afterClaim, err)
	}
	assertScalar(t, s, "SELECT count(*) FROM work_action_receipts", 0)
	if err := s.CompleteAutomationEvaluation(ctx, old.ID, orchestration.Assessment{Outcome: "complete", WorkItemID: w.ID, Reason: "The published task output satisfies its criteria; wait for the configured external receipts."}); err != nil {
		t.Fatal(err)
	}
	waiting, err := s.GetWorkItem(actor, w.ID)
	if err != nil || waiting.Status != "waiting" || !strings.Contains(waiting.WaitCondition, "action") || waiting.Question != "" || waiting.Usage.Actions != 1 || waiting.Usage.Evaluations != 1 {
		t.Fatal("ordinary dispatch bookkeeping stranded the matter or replaced current usage with frozen usage", waiting, err)
	}
	for _, decision := range waiting.Decisions {
		if decision.EvaluationID == old.ID && decision.Kind == "request_judgment" {
			t.Fatal("dispatch reservation incorrectly required human judgment", decision)
		}
	}
	completeTestWorkAction(t, s, first, "sent", 501)
	second, err := s.ClaimAutomationDelivery(ctx)
	if err != nil || second == nil || second.ID == first.ID {
		t.Fatal(second, err)
	}
	completeTestWorkAction(t, s, second, "sent", 502)
	next := claimAutomation(t, s)
	if next.ID == old.ID || len(next.Events) != 2 {
		t.Fatal("confirmed effects did not create a distinct bounded candidate", old, next)
	}
	for _, event := range next.Events {
		if event.Kind != "action.result" || event.WorkItemID != w.ID {
			t.Fatal("receipt candidate lost durable effect provenance", event)
		}
	}
	if err := s.CompleteAutomationEvaluation(ctx, next.ID, orchestration.Assessment{Outcome: "complete", WorkItemID: w.ID, Reason: "Both external destinations now have confirmed receipts."}); err != nil {
		t.Fatal(err)
	}
	finished, err := s.GetWorkItem(actor, w.ID)
	if err != nil || finished.Status != "completed" || finished.Usage.Actions != 2 || finished.Usage.Evaluations != 2 {
		t.Fatal("confirmed effects failed to close the matter within its cumulative budget", finished, err)
	}
	assertScalar(t, s, "SELECT count(*) FROM work_action_receipts", 2)
	assertScalar(t, s, "SELECT count(*) FROM automation_evaluations", 2)
}
