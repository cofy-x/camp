package storage

import (
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
)

func TestAutomationSavedPolicySurvivesSponsorDemotionButNotExecutionGrantRevocation(t *testing.T) {
	s, actor, member, room, writer, _ := taskFixture(t)
	policy := testRoomAutomationPolicy(t, s, actor, room, writer, "auto")
	sponsor, _ := UserFromContext(actor)
	other, _ := UserFromContext(member)
	adminRole, memberRole := "admin", "member"
	admin, err := s.UpdateUserAccess(actor, other.ID, &adminRole, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateUserAccess(WithUser(ctx, admin), sponsor.ID, &memberRole, nil); err != nil {
		t.Fatal(err)
	}
	// A stale authenticated role cannot edit configuration, but the saved team
	// delegation still uses the sponsor's current ordinary execution authority.
	_, err = s.SaveAutomationPolicy(actor, room.ID, "room", orchestration.PolicyInput{RequestID: newID(), Revision: policy.Revision, Mode: "observe"})
	requireCode(t, err, "forbidden")
	sendAutomationDiscussion(t, s, actor, room, "Prepare a document under the existing team policy.")
	candidate := claimAutomation(t, s)
	if err = s.CompleteAutomationEvaluation(ctx, candidate.ID, orchestration.Assessment{Outcome: "accept", Goal: "Prepare the delegated document.", Reason: "Current membership and execution grants permit this goal."}); err != nil {
		t.Fatal(err)
	}
	tasks, err := s.ListRoomTaskStates(ctx, room.ID)
	if err != nil || len(tasks) != 1 || tasks[0].RequestedByUserID != sponsor.ID {
		t.Fatal("configuration-role demotion silently erased a valid saved delegation", tasks, err)
	}
	sendAutomationDiscussion(t, s, actor, room, "Prepare a separate document before a grant is revoked.")
	inFlight := claimAutomation(t, s)
	shared := false
	if _, err = s.UpdateRunner(actor, tasks[0].Participants[0].RunnerID, RunnerUpdate{TeamShared: &shared}); err != nil {
		t.Fatal(err)
	}
	if err = s.CompleteAutomationEvaluation(ctx, inFlight.ID, orchestration.Assessment{Outcome: "accept", Goal: "This no longer has execution authority.", Reason: "An earlier assessment result."}); err != nil {
		t.Fatal(err)
	}
	row, err := s.queries().GetAutomationEvaluation(ctx, inFlight.ID)
	if err != nil || row.Status != "needs_attention" {
		t.Fatal("a saved policy bypassed current execution-grant revocation", row.Status, err)
	}
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 1)
	request := sendAutomationDiscussion(t, s, actor, room, "A later request while the Runner grant remains revoked.")
	if next, err := s.ClaimAutomationEvaluation(ctx, "model-revision", roomBudget); err != nil || next != nil {
		t.Fatal("revoked execution authority spent another assessment", next, err)
	}
	event, err := s.queries().GetAutomationEvent(ctx, "room:"+request.Message.ID)
	if err != nil || event.Status != "needs_attention" || event.Reason != "authorization_unavailable" {
		t.Fatal(event, err)
	}
	assertScalar(t, s, "SELECT sum(evaluations) FROM automation_usage", 2)
	shared = true
	if _, err = s.UpdateRunner(actor, tasks[0].Participants[0].RunnerID, RunnerUpdate{TeamShared: &shared}); err != nil {
		t.Fatal(err)
	}
	if next, err := s.ClaimAutomationEvaluation(ctx, "model-revision", roomBudget); err != nil || next != nil {
		t.Fatal("restoring authority replayed an unavailable or stale assessment", next, err)
	}
}
