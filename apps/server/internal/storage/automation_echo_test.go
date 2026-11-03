package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func TestAutomationOwnOutputUsesProtocolTailAfterManyQuotedMarkers(t *testing.T) {
	s, task, policy := automationDeliveryFixture(t)
	actor := WithUser(ctx, User{ID: policy.OwnerUserID})
	if _, err := s.SaveAutomationPolicy(actor, task.RoomID, policy.SourceID, orchestration.PolicyInput{RequestID: newID(), Revision: policy.Revision, Enabled: true, Mode: "auto", Rules: "Address new requests.", AllowedActorIDs: []string{"9"}, ParticipantAgentIDs: policy.ParticipantAgentIDs, FirstAgentID: policy.FirstAgentID, DailyEvaluations: 10, DailyTasks: 3, AllowComments: true}); err != nil {
		t.Fatal(err)
	}
	var body strings.Builder
	body.WriteString("Quoted source-data marker examples:\n")
	for index := range 32 {
		fmt.Fprintf(&body, "<!-- camp-effect:%064x -->\n", index+1)
	}
	if err := s.transaction(ctx, func(tx *sql.Tx) error {
		return queueAutomationTaskNotice(ctx, sqlgen.New(tx), task, "task_attention", body.String())
	}); err != nil {
		t.Fatal(err)
	}
	delivery, err := s.ClaimAutomationDelivery(ctx)
	if err != nil || delivery == nil {
		t.Fatal(delivery, err)
	}
	source, err := s.queries().GetGitHubSource(ctx, policy.SourceID)
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(map[string]any{"action": "created", "repository": map[string]any{"id": delivery.RepositoryID, "full_name": delivery.Repository}, "sender": map[string]any{"id": delivery.ActorID, "login": "camp-comment-account", "type": "User"}, "issue": map[string]any{"id": 99, "number": delivery.Number, "title": "Observed issue"}, "comment": map[string]any{"id": 301, "body": delivery.Body + "\n\n" + delivery.Marker}})
	observed, _, err := s.ReceiveGitHubDelivery(ctx, source.ID, source.SecretRef, "aaaaaaaa-bbbb-cccc-dddd-"+newID()[:12], "issue_comment", payload)
	if err != nil {
		t.Fatal(err)
	}
	event, err := s.queries().GetAutomationEvent(ctx, "github:"+observed.ID)
	if err != nil || event.Status != "ignored" || event.Reason != "own_output" || event.EvaluationID != "" {
		t.Fatal("quoted marker data caused a frozen Camp comment to enter intake again", event, err)
	}
	unchanged, err := s.GetTaskState(ctx, task.ID)
	if err != nil || unchanged.InputRevision != task.InputRevision {
		t.Fatal("the marker-rich own comment entered a subscribed task", unchanged.InputRevision, task.InputRevision, err)
	}
}

func TestAutomationDelayedOwnOutputUsesFrozenActorAfterCredentialRotation(t *testing.T) {
	s, admin, _, room, writer, _ := taskFixture(t)
	source := testGitHubSource(t, s, admin, room)
	receiveTestGitHub(t, s, source, testGitHubPayload("Observed issue", "Original source context."))
	policy, err := s.SaveAutomationPolicy(admin, room.ID, source.ID, orchestration.PolicyInput{RequestID: newID(), Enabled: true, Mode: "auto", Rules: "Address new requests.", AllowedActorIDs: []string{"7", "8"}, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID, DailyEvaluations: 10, DailyTasks: 3, AllowComments: true})
	if err != nil {
		t.Fatal(err)
	}
	oldRef := strings.Repeat("b", 48)
	receipt, err := s.SetAutomationCredential(admin, source.ID, AutomationCredentialInput{RequestID: newID(), Revision: policy.Revision, Token: "private-old-token", ActorID: 7}, oldRef)
	if err != nil {
		t.Fatal(err)
	}
	task := startTask(t, s, admin, room, writer)
	subscribeTaskTest(t, s, admin, task, source.ID, "123:3")
	id := remoteHash([]byte("delayed-own-output"))
	delivery := orchestration.Delivery{ID: id, TaskID: task.ID, SourceID: source.ID, PolicyID: policy.ID, RepositoryID: 123, Repository: source.Repository, Number: 3, ActorID: 7, Body: "Camp prepared the requested document.", Marker: "<!-- camp-effect:" + id + " -->", CredentialRef: oldRef}
	raw, _ := json.Marshal(delivery)
	if err := s.queries().InsertAutomationDelivery(ctx, sqlgen.InsertAutomationDeliveryParams{ID: id, TaskID: task.ID, SourceID: source.ID, PolicyID: policy.ID, Value: string(raw), CredentialRef: oldRef, CreatedAt: now()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetAutomationCredential(admin, source.ID, AutomationCredentialInput{RequestID: newID(), Revision: receipt.Policy.Revision, Token: "private-new-token", ActorID: 9}, strings.Repeat("c", 48)); err != nil {
		t.Fatal(err)
	}
	receiveComment := func(body string, actor, number, object int64) orchestration.SourceEvent {
		t.Helper()
		payload, _ := json.Marshal(map[string]any{"action": "created", "repository": map[string]any{"id": 123, "full_name": source.Repository}, "sender": map[string]any{"id": actor, "login": fmt.Sprintf("person-%d", actor), "type": "User"}, "issue": map[string]any{"id": 99, "number": number, "title": "Observed issue"}, "comment": map[string]any{"id": object, "body": body}})
		event, _, err := s.ReceiveGitHubDelivery(ctx, source.ID, source.SecretRef, "aaaaaaaa-bbbb-cccc-dddd-"+newID()[:12], "issue_comment", payload)
		if err != nil {
			t.Fatal(err)
		}
		row, err := s.queries().GetAutomationEvent(ctx, "github:"+event.ID)
		if err != nil {
			t.Fatal(err)
		}
		result, err := automationEvent(row)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	body := delivery.Body + "\n\n" + delivery.Marker
	echo := receiveComment(body, 7, 3, 301)
	if echo.Status != "ignored" || echo.Reason != "own_output" {
		t.Fatal("rotated sender's exact output was not suppressed", echo)
	}
	unchanged, err := s.GetTaskState(ctx, task.ID)
	if err != nil || unchanged.InputRevision != 0 {
		t.Fatal("own output entered the subscribed task", unchanged, err)
	}
	forged := receiveComment(body, 8, 3, 302)
	if forged.Status != "pending" || forged.Reason == "own_output" {
		t.Fatal("another sender's marker forged an own-output receipt", forged)
	}
	changed := receiveComment("Changed request\n\n"+delivery.Marker, 7, 3, 303)
	if changed.Status != "pending" || changed.Reason == "own_output" {
		t.Fatal("an altered body was suppressed as our output", changed)
	}
	otherThread := receiveComment(body, 7, 4, 304)
	if otherThread.Status != "pending" || otherThread.Reason == "own_output" {
		t.Fatal("a marker from another issue forged an own-output receipt", otherThread)
	}
	inputs, err := s.TaskInputsPage(admin, task.ID, 0, nil, 20)
	if err != nil || len(inputs.Items) != 2 {
		t.Fatal("external requests did not preserve ordinary subscription delivery", inputs, err)
	}
	candidate := claimAutomation(t, s)
	if len(candidate.Events) != 2 || candidate.Events[0].ID != forged.ID || candidate.Events[1].ID != changed.ID {
		t.Fatal("own output reached assessment or forged markers were dropped", candidate)
	}
}

func TestAutomationRoomEventRecordsExplicitRunAndPostOnlyDecisions(t *testing.T) {
	s, admin, _, room, writer, _ := taskFixture(t)
	for _, scenario := range []struct {
		input          SendMessageInput
		status, reason string
	}{
		{SendMessageInput{RequestID: newID(), Content: "Perform this explicit work.", RecipientAgentIDs: []string{writer.ID}}, "accepted", "explicit_run"},
		{SendMessageInput{RequestID: newID(), Content: "Discussion only.", PostOnly: true}, "ignored", "post_only"},
	} {
		result, err := s.SendMessage(admin, room.ID, scenario.input, roomBudget)
		if err != nil {
			t.Fatal(err)
		}
		row, err := s.queries().GetAutomationEvent(ctx, "room:"+result.Message.ID)
		if err != nil || row.Status != scenario.status || row.Reason != scenario.reason {
			t.Fatal("source event lost its explicit decision", row, err)
		}
	}
}

func TestAutomationCommentAccountGenuineRequestsRemainEligible(t *testing.T) {
	s, admin, _, room, writer, _ := taskFixture(t)
	source := testGitHubSource(t, s, admin, room)
	policy, err := s.SaveAutomationPolicy(admin, room.ID, source.ID, orchestration.PolicyInput{RequestID: newID(), Enabled: true, Mode: "auto", Rules: "Address the account owner's explicit requests.", AllowedActorIDs: []string{"7"}, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID, DailyEvaluations: 5, DailyTasks: 2, AllowComments: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetAutomationCredential(admin, source.ID, AutomationCredentialInput{RequestID: newID(), Revision: policy.Revision, Token: "private-comment-account-token", ActorID: 7}, strings.Repeat("b", 48)); err != nil {
		t.Fatal(err)
	}
	event := receiveTestGitHub(t, s, source, testGitHubPayload("A genuine request", "Please prepare the guide requested by this account."))
	row, err := s.queries().GetAutomationEvent(ctx, "github:"+event.ID)
	if err != nil || row.Status != "pending" || row.Reason == "own_output" {
		t.Fatal("the comment account's genuine issue was mistaken for generated output", row, err)
	}
	candidate := claimAutomation(t, s)
	if len(candidate.Events) != 1 || candidate.Events[0].ActorID != "7" || candidate.Events[0].ExternalEventID != event.ID {
		t.Fatal("the account's genuine request did not reach the normal gate", candidate)
	}
}
