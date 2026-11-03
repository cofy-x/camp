package storage

import (
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
)

func TestAutomationDisabledOriginPolicyDoesNotBlockManuallyFollowedOtherSource(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	source := testGitHubSource(t, s, actor, room)
	receiveTestGitHub(t, s, source, testGitHubPayload("Original issue", "A conversation that can be followed."))
	policy := testRoomAutomationPolicy(t, s, actor, room, writer, "auto")
	sendAutomationDiscussion(t, s, actor, room, "Prepare a useful document.")
	initial := claimAutomation(t, s)
	if err := s.CompleteAutomationEvaluation(ctx, initial.ID, orchestration.Assessment{Outcome: "accept", Goal: "Prepare the document.", Reason: "Requested by an approved member."}); err != nil {
		t.Fatal(err)
	}
	tasks, err := s.ListRoomTaskStates(ctx, room.ID)
	if err != nil || len(tasks) != 1 {
		t.Fatal(tasks, err)
	}
	task := tasks[0]
	subscribeTaskTest(t, s, actor, task, source.ID, "123:3")
	if _, err = s.SaveAutomationPolicy(actor, room.ID, source.ID, orchestration.PolicyInput{RequestID: newID(), Enabled: true, Mode: "observe", Rules: "Observe new requests and existing delivered context.", AllowedActorIDs: []string{"7"}, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID, DailyEvaluations: 5, DailyTasks: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SaveAutomationPolicy(actor, room.ID, "room", orchestration.PolicyInput{RequestID: newID(), Revision: policy.Revision, Enabled: false, Mode: "auto"}); err != nil {
		t.Fatal(err)
	}
	sendAutomationDiscussion(t, s, actor, room, "Room policy is off, so this origin-source input stays paused.")
	state, err := s.GetTaskState(ctx, task.ID)
	if err != nil || state.InputRevision != 1 {
		t.Fatal("disabling the origin policy stopped guarding its own source", state, err)
	}
	followup := receiveTestGitHub(t, s, source, testGitHubPayload("Followed issue", "Manual cross-source context must still arrive."))
	state, err = s.GetTaskState(ctx, task.ID)
	if err != nil || state.InputRevision != 2 || state.ActiveRunID != task.ActiveRunID || state.Status != task.Status || state.MaxTurns != task.MaxTurns || state.MaxModelRequests != task.MaxModelRequests {
		t.Fatal("an unrelated disabled policy blocked manual source following or expanded its authority", state, err)
	}
	inputs, err := s.TaskInputsPage(actor, task.ID, 1, nil, 20)
	if err != nil || len(inputs.Items) != 1 || inputs.Items[0].SourceEventID != "github:"+followup.ID || inputs.Items[0].AuthorKind != "external" {
		t.Fatal("manual cross-source context lost its source evidence", inputs, err)
	}
	candidate := claimAutomation(t, s)
	if candidate.Policy.SourceID != source.ID || len(candidate.Tasks) != 1 || candidate.Tasks[0].ID != task.ID || !candidate.Tasks[0].ReceivesInputs || len(candidate.Tasks[0].ReceivedEventIDs) != 1 || candidate.Tasks[0].ReceivedEventIDs[0] != "github:"+followup.ID {
		t.Fatal("assessment routing facts disagreed with actual cross-source delivery", candidate)
	}
	if err = s.CompleteAutomationEvaluation(ctx, candidate.ID, orchestration.Assessment{Outcome: "ignore", Reason: "This is context already delivered to the followed task."}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateGitHubSource(actor, source.ID, UpdateGitHubSourceInput{RequestID: newID(), Revision: source.Revision, Enabled: new(false)}, ""); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.ReceiveGitHubDelivery(ctx, source.ID, source.SecretRef, "aaaaaaaa-bbbb-cccc-dddd-"+newID()[:12], "issues", testGitHubPayload("Disabled source", "This ingress must still be rejected."))
	requireCode(t, err, "source_unavailable")
	state, err = s.GetTaskState(ctx, task.ID)
	if err != nil || state.InputRevision != 2 {
		t.Fatal("a disabled source bypassed ingress authorization", state, err)
	}
}
