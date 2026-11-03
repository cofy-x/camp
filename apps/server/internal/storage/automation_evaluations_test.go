package storage

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func testRoomAutomationPolicy(t *testing.T, s *Store, actor context.Context, room Room, writer Agent, mode string) orchestration.Policy {
	t.Helper()
	u, _ := UserFromContext(actor)
	p, e := s.SaveAutomationPolicy(actor, room.ID, "room", orchestration.PolicyInput{RequestID: newID(), Enabled: true, Mode: mode, Rules: "Prepare the requested document. Ignore context for an existing task.", AllowedActorIDs: []string{u.ID}, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID, DailyEvaluations: 5, DailyTasks: 2})
	if e != nil {
		t.Fatal(e)
	}
	return p
}
func sendAutomationDiscussion(t *testing.T, s *Store, actor context.Context, room Room, body string) SendResult {
	t.Helper()
	r, e := s.SendMessage(actor, room.ID, SendMessageInput{RequestID: newID(), Content: body}, roomBudget)
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func claimAutomation(t *testing.T, s *Store) *orchestration.Candidate {
	t.Helper()
	c, e := s.ClaimAutomationEvaluation(ctx, "model-revision", roomBudget)
	if e != nil || c == nil {
		t.Fatal(c, e)
	}
	return c
}
func TestAutomationObservationVersionAndFiniteAutomaticTask(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	p := testRoomAutomationPolicy(t, s, actor, room, writer, "observe")
	sendAutomationDiscussion(t, s, actor, room, "Prepare the document.")
	c := claimAutomation(t, s)
	if e := s.CompleteAutomationEvaluation(ctx, c.ID, orchestration.Assessment{Outcome: "accept", Goal: "Prepare document", Reason: "Explicitly requested"}); e != nil {
		t.Fatal(e)
	}
	tasks, _ := s.ListRoomTaskStates(ctx, room.ID)
	if len(tasks) != 0 {
		t.Fatal("Observation executed work", tasks)
	}
	event, _ := s.queries().GetAutomationEvent(ctx, c.Events[0].ID)
	if event.Status != "observed" {
		t.Fatal(event)
	}
	u, _ := UserFromContext(actor)
	p, e := s.SaveAutomationPolicy(actor, room.ID, "room", orchestration.PolicyInput{RequestID: newID(), Revision: p.Revision, Enabled: true, Mode: "auto", Rules: p.Rules, AllowedActorIDs: []string{u.ID}, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID, DailyEvaluations: 5, DailyTasks: 2})
	if e != nil {
		t.Fatal(e)
	}
	if c, e := s.ClaimAutomationEvaluation(ctx, "new-model", roomBudget); e != nil || c != nil {
		t.Fatal("Enabling auto replayed history", c, e)
	}
	sendAutomationDiscussion(t, s, actor, room, "Please prepare a new document.")
	c = claimAutomation(t, s)
	var wg sync.WaitGroup
	for range 6 {
		wg.Go(func() {
			if e := s.CompleteAutomationEvaluation(ctx, c.ID, orchestration.Assessment{Outcome: "accept", Goal: "Prepare new document", Reason: "Within the delegated scope"}); e != nil {
				t.Error(e)
			}
		})
	}
	wg.Wait()
	tasks, e = s.ListRoomTaskStates(ctx, room.ID)
	if e != nil || len(tasks) != 1 {
		t.Fatal(tasks, e)
	}
	task := tasks[0]
	if task.MaxTurns != 6 || task.MaxModelRequests != 24 || task.RequestedByUserID != u.ID || task.AutomationPolicyID != p.ID || task.InputRevision != 1 {
		t.Fatal(task)
	}
	source, e := s.GetMessage(ctx, task.OriginMessageID)
	if e != nil || source.AuthorKind != "system" || source.AuthorUserID != "" {
		t.Fatal("Automatic origin impersonated sponsor", source, e)
	}
	usage, e := s.queries().GetAutomationUsage(ctx, sqlgen.GetAutomationUsageParams{PolicyID: p.ID, Day: automationDay()})
	if e != nil || usage.Evaluations != 2 || usage.Tasks != 1 {
		t.Fatal(usage, e)
	}
}

func TestAutomationStaleAssessmentAndRecoveryDoNotReplayProvider(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	p := testRoomAutomationPolicy(t, s, actor, room, writer, "auto")
	sendAutomationDiscussion(t, s, actor, room, "Prepare a proposal.")
	c := claimAutomation(t, s)
	u, _ := UserFromContext(actor)
	_, e := s.SaveAutomationPolicy(actor, room.ID, "room", orchestration.PolicyInput{RequestID: newID(), Revision: p.Revision, Mode: "observe", Rules: p.Rules, AllowedActorIDs: []string{u.ID}, MaxTurns: 6, MaxModelRequests: 24})
	if e != nil {
		t.Fatal(e)
	}
	if e = s.CompleteAutomationEvaluation(ctx, c.ID, orchestration.Assessment{Outcome: "accept", Goal: "Old proposal", Reason: "Old policy"}); e != nil {
		t.Fatal(e)
	}
	evt, _ := s.queries().GetAutomationEvent(ctx, c.Events[0].ID)
	if evt.Status != "needs_attention" || evt.Reason != "stale_assessment" {
		t.Fatal(evt)
	}
	pRow, _ := s.queries().GetAutomationPolicy(ctx, sqlgen.GetAutomationPolicyParams{RoomID: room.ID, SourceID: "room"})
	p, _ = automationPolicy(pRow)
	_, e = s.SaveAutomationPolicy(actor, room.ID, "room", orchestration.PolicyInput{RequestID: newID(), Revision: p.Revision, Enabled: true, Mode: "auto", Rules: p.Rules, AllowedActorIDs: []string{u.ID}, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID, DailyEvaluations: 5, DailyTasks: 2})
	if e != nil {
		t.Fatal(e)
	}
	sendAutomationDiscussion(t, s, actor, room, "Prepare another proposal.")
	c = claimAutomation(t, s)
	if e = s.RecoverAutomationWork(ctx); e != nil {
		t.Fatal(e)
	}
	evt, _ = s.queries().GetAutomationEvent(ctx, c.Events[0].ID)
	if evt.Status != "needs_attention" {
		t.Fatal(evt)
	}
	if c, e = s.ClaimAutomationEvaluation(ctx, "model-revision", roomBudget); e != nil || c != nil {
		t.Fatal("Unknown provider execution replayed", c, e)
	}
	tasks, _ := s.ListRoomTaskStates(ctx, room.ID)
	if len(tasks) != 0 {
		t.Fatal(tasks)
	}
}

func TestAutomationPostOnlyExplicitRunsAndDailyLimits(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	p := testRoomAutomationPolicy(t, s, actor, room, writer, "auto")
	for _, input := range []SendMessageInput{{RequestID: newID(), Content: "Prepare this", PostOnly: true}, {RequestID: newID(), Content: "@" + writer.Handle + " prepare this", RecipientAgentIDs: []string{writer.ID}, RunnerRevision: &room.RunnerRevision}} {
		if _, e := s.SendMessage(actor, room.ID, input, roomBudget); e != nil {
			t.Fatal(e)
		}
	}
	if c, e := s.ClaimAutomationEvaluation(ctx, "model-revision", roomBudget); e != nil || c != nil {
		t.Fatal("Explicit semantics entered automatic assessment", c, e)
	}
	// Usage belongs to the stable policy ID, independently of policy revisions.
	if _, e := s.db.Exec("INSERT INTO automation_usage(policy_id,day,evaluations,tasks) VALUES(?,?,?,?)", p.ID, automationDay(), p.DailyEvaluations, 0); e != nil {
		t.Fatal(e)
	}
	r := sendAutomationDiscussion(t, s, actor, room, "One more request")
	if c, e := s.ClaimAutomationEvaluation(ctx, "model-revision", roomBudget); e != nil || c != nil {
		t.Fatal(c, e)
	}
	evt, e := s.queries().GetAutomationEvent(ctx, "room:"+r.Message.ID)
	if e != nil || evt.Reason != "daily_evaluation_limit" {
		t.Fatal(evt, e)
	}
	for i := range 2 {
		if _, e = s.SendMessage(actor, room.ID, SendMessageInput{RequestID: fmt.Sprintf("duplicate-%d", i), Content: "Discussion", PostOnly: true}, roomBudget); e != nil {
			t.Fatal(e)
		}
	}
}

func TestAutomationCancellationRetiresPendingThreadFrontier(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	testRoomAutomationPolicy(t, s, actor, room, writer, "auto")
	sendAutomationDiscussion(t, s, actor, room, "Prepare a document.")
	c := claimAutomation(t, s)
	if err := s.CompleteAutomationEvaluation(ctx, c.ID, orchestration.Assessment{Outcome: "accept", Goal: "Prepare document", Reason: "Requested"}); err != nil {
		t.Fatal(err)
	}
	tasks, _ := s.ListRoomTaskStates(ctx, room.ID)
	task := tasks[0]
	sendAutomationDiscussion(t, s, actor, room, "An old follow-up queued before cancellation.")
	task, _ = s.GetTaskState(ctx, task.ID)
	if _, err := s.OperateTask(actor, task.ID, "cancel", TaskOperation{RequestID: newID(), Revision: task.Revision, InputProtocol: 1, PagedResponse: true}); err != nil {
		t.Fatal(err)
	}
	if candidate, err := s.ClaimAutomationEvaluation(ctx, "model-revision", roomBudget); err != nil || candidate != nil {
		t.Fatal("Cancellation left an old event able to recreate work", candidate, err)
	}
	sendAutomationDiscussion(t, s, actor, room, "A genuinely new request after cancellation.")
	if candidate, err := s.ClaimAutomationEvaluation(ctx, "model-revision", roomBudget); err != nil || candidate == nil {
		t.Fatal(candidate, err)
	}
}
