package storage

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
)

const feishuCredential = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

func TestFeishuPolicyAcceptsFullScopedSenderIdentity(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	source, err := s.CreateFeishuSource(actor, room.ID, CreateFeishuSourceInput{RequestID: newID(), AppID: "cli_app", TenantKey: "tenant", ChatID: "oc_chat", VerificationToken: "synthetic-verification-token", EncryptKey: "synthetic-encryption-key"}, feishuCredential)
	if err != nil {
		t.Fatal(err)
	}
	tenant, open := strings.Repeat("t", 96), strings.Repeat("o", 96)
	identity := tenant + ":" + open
	_, err = s.SaveAutomationPolicy(actor, room.ID, source.ID, orchestration.PolicyInput{RequestID: newID(), Enabled: true, Mode: "observe", Rules: "Review incoming context.", AllowedActorIDs: []string{identity}, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID, DailyEvaluations: 5, DailyTasks: 2})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err = json.Unmarshal(feishuTestPayload("long-identity", "Source identity remains external."), &payload); err != nil {
		t.Fatal(err)
	}
	sender := payload["event"].(map[string]any)["sender"].(map[string]any)
	sender["tenant_key"] = tenant
	sender["sender_id"] = map[string]string{"open_id": open}
	plain, _ := json.Marshal(payload)
	if _, _, err = s.ReceiveFeishuDelivery(ctx, source.ID, feishuCredential, []byte(`{"encrypt":"synthetic"}`), plain); err != nil {
		t.Fatal(err)
	}
	c := claimAutomation(t, s)
	if len(c.Events) != 1 || c.Events[0].ActorID != identity {
		t.Fatal("full source identity could not reach bounded assessment", c)
	}
}

func feishuTestPayload(delivery, body string) []byte {
	text, _ := json.Marshal(map[string]string{"text": body})
	raw, _ := json.Marshal(map[string]any{"schema": "2.0", "header": map[string]string{"event_id": delivery, "event_type": "im.message.receive_v1", "app_id": "cli_app", "tenant_key": "tenant", "token": "synthetic-verification-token"}, "event": map[string]any{"sender": map[string]any{"sender_type": "user", "tenant_key": "tenant", "sender_id": map[string]string{"open_id": "ou_author"}}, "message": map[string]string{"message_id": "om_message", "root_id": "om_root", "chat_id": "oc_chat", "create_time": "1700000000000", "message_type": "text", "content": string(text)}}})
	return raw
}

func TestFeishuSourceScopedDeduplicationOriginalsAndCredentialFence(t *testing.T) {
	s, actor, _, room, _, _ := taskFixture(t)
	in := CreateFeishuSourceInput{RequestID: newID(), AppID: "cli_app", TenantKey: "tenant", ChatID: "oc_chat", VerificationToken: "synthetic-verification-token", EncryptKey: "synthetic-encryption-key"}
	source, err := s.CreateFeishuSource(actor, room.ID, in, feishuCredential)
	if err != nil {
		t.Fatal(err)
	}
	if old, err := s.CreateFeishuSource(actor, room.ID, in, strings.Repeat("b", 48)); err != nil || old.ID != source.ID {
		t.Fatal("configuration replay changed credential", old, err)
	}
	if current, err := s.FeishuSourceCredential(ctx, source.ID); err != nil || current.CredentialRef != feishuCredential {
		t.Fatal("configuration replay wrote a replacement credential", err)
	}
	safe, _ := json.Marshal(source)
	if bytes.Contains(safe, []byte(in.VerificationToken)) || bytes.Contains(safe, []byte(in.EncryptKey)) || bytes.Contains(safe, []byte(feishuCredential)) {
		t.Fatal("source exposed private state")
	}
	plain := feishuTestPayload("delivery1", "  Authored\ntext \n")
	original := []byte(`{"encrypt":"synthetic-encrypted-original"}`)
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for range 8 {
		wg.Go(func() {
			observed, _, err := s.ReceiveFeishuDelivery(ctx, source.ID, feishuCredential, original, plain)
			if err != nil {
				t.Error(err)
				return
			}
			ids <- observed.ID
		})
	}
	wg.Wait()
	close(ids)
	eventID := ""
	for id := range ids {
		if eventID != "" && id != eventID {
			t.Fatal("concurrent duplicate created another fact")
		}
		eventID = id
	}
	if eventID == "" {
		t.Fatal("delivery did not commit")
	}
	observed, err := s.GetFeishuEvent(actor, eventID)
	if err != nil || observed.Content != "  Authored\ntext \n" || observed.ActorID() != "tenant:ou_author" {
		t.Fatal(observed, err)
	}
	payload, err := s.FeishuPayload(actor, eventID)
	if err != nil || !bytes.Equal(payload, original) {
		t.Fatal("immutable original changed", err)
	}
	redelivery := bytes.Replace(plain, []byte("delivery1"), []byte("delivery2"), 1)
	old, duplicate, err := s.ReceiveFeishuDelivery(ctx, source.ID, feishuCredential, []byte(`{"encrypt":"new-IV"}`), redelivery)
	if err != nil || !duplicate || old.ID != eventID {
		t.Fatal("new transport identity duplicated fact", old, err)
	}
	if _, _, err := s.ReceiveFeishuDelivery(ctx, source.ID, feishuCredential, original, feishuTestPayload("delivery1", "changed")); err == nil {
		t.Fatal("reused event ID with different content")
	}
	wrong := bytes.Replace(plain, []byte("oc_chat"), []byte("oc_other"), 1)
	if _, _, err := s.ReceiveFeishuDelivery(ctx, source.ID, feishuCredential, original, wrong); err == nil {
		t.Fatal("accepted another chat")
	}
	page, err := s.AutomationActivity(actor, room.ID, AutomationEventQuery{SourceID: source.ID})
	if err != nil || len(page.Items) != 1 || page.Items[0].Feishu == nil || page.Items[0].Feishu.EventID != eventID || page.Items[0].ExternalEventID != "" || page.Items[0].Preview != "Authored text" || page.Items[0].Status != "context" {
		t.Fatal(page, err)
	}
	tasks, _ := s.ListRoomTaskStates(ctx, room.ID)
	if len(tasks) != 0 {
		t.Fatal("source text granted execution")
	}
	rotated, err := s.UpdateFeishuSource(actor, source.ID, UpdateFeishuSourceInput{RequestID: newID(), Revision: source.Revision, VerificationToken: in.VerificationToken + "-new", EncryptKey: in.EncryptKey + "-new"}, strings.Repeat("b", 48))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ReceiveFeishuDelivery(ctx, source.ID, feishuCredential, original, feishuTestPayload("delivery3", "after rotation")); err == nil {
		t.Fatal("old in-flight credential survived rotation")
	}
	if _, err = s.UpdateFeishuSource(actor, source.ID, UpdateFeishuSourceInput{RequestID: newID(), Revision: rotated.Revision, Enabled: new(false)}, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ReceiveFeishuDelivery(ctx, source.ID, strings.Repeat("b", 48), original, feishuTestPayload("delivery4", "disabled")); err == nil {
		t.Fatal("disabled source received new evidence")
	}
	if _, err := s.GetFeishuEvent(actor, eventID); err != nil {
		t.Fatal("disabled source lost old evidence", err)
	}
	var leaked int
	if err := s.db.QueryRow(`SELECT count(*) FROM feishu_events WHERE instr(value,?)>0 OR instr(CAST(payload AS TEXT),?)>0`, in.VerificationToken, in.EncryptKey).Scan(&leaked); err != nil || leaked != 0 {
		t.Fatal("private credential entered team data", err)
	}
}

func TestFeishuPolicyUsesExternalIdentityAndExistingFiniteTaskAdmission(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	source, err := s.CreateFeishuSource(actor, room.ID, CreateFeishuSourceInput{RequestID: newID(), AppID: "cli_app", TenantKey: "tenant", ChatID: "oc_chat"}, feishuCredential)
	if err != nil {
		t.Fatal(err)
	}
	in := orchestration.PolicyInput{RequestID: newID(), Enabled: true, Mode: "auto", Rules: "Prepare the requested document.", AllowedActorIDs: []string{"tenant:ou_author"}, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID, DailyEvaluations: 5, DailyTasks: 2}
	policy, err := s.SaveAutomationPolicy(actor, room.ID, source.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	for _, change := range []func(*orchestration.PolicyInput){func(p *orchestration.PolicyInput) { p.AllowedActorIDs = []string{"7"} }, func(p *orchestration.PolicyInput) { p.AllowComments = true }, func(p *orchestration.PolicyInput) { p.AllowAnswers = true }} {
		bad := in
		bad.RequestID = newID()
		bad.Revision = policy.Revision
		change(&bad)
		if _, err := s.SaveAutomationPolicy(actor, room.ID, source.ID, bad); err == nil {
			t.Fatal("accepted invalid Feishu delegation")
		}
	}
	first, _, err := s.ReceiveFeishuDelivery(ctx, source.ID, feishuCredential, []byte(`{"encrypt":"first"}`), feishuTestPayload("delivery1", "Please prepare this document."))
	if err != nil {
		t.Fatal(err)
	}
	candidate := claimAutomation(t, s)
	if len(candidate.Events) != 1 || candidate.Events[0].ActorID != "tenant:ou_author" || candidate.Events[0].Feishu == nil {
		t.Fatal(candidate)
	}
	if err := s.CompleteAutomationEvaluation(ctx, candidate.ID, orchestration.Assessment{Outcome: "accept", Goal: "Prepare the requested document", Reason: "Within the delegated scope"}); err != nil {
		t.Fatal(err)
	}
	tasks, err := s.ListRoomTaskStates(ctx, room.ID)
	if err != nil || len(tasks) != 1 {
		t.Fatal(tasks, err)
	}
	task := tasks[0]
	if task.SourceEventID != "" || task.AutomationSourceEventID != "feishu:"+first.ID || task.MaxTurns != 6 || task.MaxModelRequests != 24 {
		t.Fatal("Feishu bypassed finite task grant or reused GitHub identity", task)
	}
	second := feishuTestPayload("delivery2", "New context for this task.")
	second = bytes.Replace(second, []byte("om_message"), []byte("om_followup"), 1)
	if _, _, err := s.ReceiveFeishuDelivery(ctx, source.ID, feishuCredential, []byte(`{"encrypt":"second"}`), second); err != nil {
		t.Fatal(err)
	}
	updated, err := s.GetTaskState(ctx, task.ID)
	if err != nil || updated.InputRevision != 2 {
		t.Fatal("subscription did not append external evidence", updated, err)
	}
	inputs, err := s.TaskInputsPage(actor, task.ID, 0, nil, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(inputs.Items) != 2 || inputs.Items[1].AuthorUserID != "" || inputs.Items[1].ExternalActorID != "tenant:ou_author" {
		t.Fatal("external sender impersonated Camp user", inputs)
	}
}

func TestFeishuIntakeRollsBackOriginalAndReceiptWhenCanonicalEventFails(t *testing.T) {
	s, actor, _, room, _, _ := taskFixture(t)
	source, err := s.CreateFeishuSource(actor, room.ID, CreateFeishuSourceInput{RequestID: newID(), AppID: "cli_app", TenantKey: "tenant", ChatID: "oc_chat"}, feishuCredential)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_feishu_event BEFORE INSERT ON automation_events WHEN NEW.kind='im.message.receive_v1' BEGIN SELECT RAISE(ABORT,'synthetic event failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.ReceiveFeishuDelivery(ctx, source.ID, feishuCredential, []byte(`{"encrypt":"original"}`), feishuTestPayload("delivery1", "Evidence")); err == nil {
		t.Fatal("admitted failed canonical event")
	}
	for _, table := range []string{"feishu_events", "feishu_deliveries"} {
		var count int
		if err := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil || count != 0 {
			t.Fatal("failed intake left partial durable records", table, count, err)
		}
	}
}

func TestFeishuCancelledUnknownConfigurationFencesDelayedWrites(t *testing.T) {
	s, actor, _, room, _, _ := taskFixture(t)
	in := CreateFeishuSourceInput{RequestID: "cancel-before-create", AppID: "cli_app", TenantKey: "tenant", ChatID: "oc_chat"}
	if receipt, err := s.CancelFeishuRequest(actor, in.RequestID); err != nil || receipt.Kind != "cancelled" {
		t.Fatal(receipt, err)
	}
	if _, err := s.CreateFeishuSource(actor, room.ID, in, feishuCredential); err == nil {
		t.Fatal("delayed source creation ignored cancellation")
	}
	in.RequestID = "committed-create"
	source, err := s.CreateFeishuSource(actor, room.ID, in, feishuCredential)
	if err != nil {
		t.Fatal(err)
	}
	if receipt, err := s.CancelFeishuRequest(actor, in.RequestID); err != nil || receipt.Source == nil || receipt.Source.ID != source.ID {
		t.Fatal("cancellation lost concurrently committed receipt", receipt, err)
	}
	update := UpdateFeishuSourceInput{RequestID: "cancel-before-update", Revision: source.Revision, Enabled: new(false)}
	if _, err := s.CancelFeishuRequest(actor, update.RequestID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateFeishuSource(actor, source.ID, update, ""); err == nil {
		t.Fatal("delayed source update ignored cancellation")
	}
	if current, err := s.FeishuSourceCredential(ctx, source.ID); err != nil || !current.Enabled {
		t.Fatal("cancelled update changed source", current, err)
	}
}

func TestFeishuCancellationAndOriginalCreationHaveOneDurableWinner(t *testing.T) {
	s, actor, _, room, _, _ := taskFixture(t)
	in := CreateFeishuSourceInput{RequestID: "racing-create", AppID: "cli_app", TenantKey: "tenant", ChatID: "oc_chat"}
	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		<-start
		_, err := s.CreateFeishuSource(actor, room.ID, in, feishuCredential)
		if err != nil {
			var p *Error
			if !errors.As(err, &p) || p.Code != "request_cancelled" {
				t.Error(err)
			}
		}
	}()
	go func() {
		defer wg.Done()
		<-start
		if _, err := s.CancelFeishuRequest(actor, in.RequestID); err != nil {
			t.Error(err)
		}
	}()
	close(start)
	wg.Wait()
	receipt, err := s.FeishuRequest(actor, in.RequestID)
	if err != nil {
		t.Fatal(err)
	}
	sources, err := s.ListFeishuSources(actor, room.ID)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.Kind == "cancelled" {
		if len(sources) != 0 {
			t.Fatal("cancelled receipt admitted delayed creation")
		}
	} else if receipt.Source == nil || len(sources) != 1 || receipt.Source.ID != sources[0].ID {
		t.Fatal("committed creation lost its winning receipt", receipt, sources)
	}
}
