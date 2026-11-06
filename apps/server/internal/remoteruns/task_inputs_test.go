package remoteruns

import (
	"encoding/json"
	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/nativeexecutor"
	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestTaskInputHTTPSCapabilityPreservesFrozenScopeAcrossRestart(t *testing.T) {
	h := newRemoteHarness(t, configuration.ModelInput{})
	// Claim and prepare manually without racing scheduler recovery of the same
	// unprepared durable Attempt. Restart below still creates a real scheduler.
	h.manager.Load().cancel()
	<-h.manager.Load().done
	config := h.join(t, "history-owner", "fixture", nil, nativeexecutor.Config{})
	other := h.join(t, "history-other", "fixture", nil, nativeexecutor.Config{})
	if _, err := h.store.ConnectRunner(h.ctx, config.RunnerID, storage.RunnerConnection{StorageID: config.StorageID, Runtime: "fixture", Slots: 1, Capabilities: json.RawMessage(`{"executor":"fixture","ready":true,"taskInputsVersion":1}`)}); err != nil {
		t.Fatal(err)
	}
	room, writer := h.room(t, "Input history", config.RunnerID)
	task, err := h.manager.Load().CreateTask(h.actor, room.ID, storage.CreateTaskInput{InputProtocol: 1, PagedResponse: true, RequestID: newID(), Goal: "Keep original constraints.", ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID})
	if err != nil {
		t.Fatal(err)
	}
	for range 25 {
		task, err = h.store.OperateTask(h.actor, task.ID, "inputs", storage.TaskOperation{InputProtocol: 1, PagedResponse: true, RequestID: newID(), Content: strings.Repeat("x", 3000) + " Immutable original <content>"})
		if err != nil {
			t.Fatal(err)
		}
	}
	run, attempt, err := h.store.ClaimRemoteRun(h.ctx, task.ActiveRunID, config.RunnerID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = h.manager.Load().prepare(h.ctx, run, attempt); err != nil {
		t.Fatal(err)
	}
	get := func(path, token string, headers map[string]string) (int, []byte) {
		t.Helper()
		req, _ := http.NewRequestWithContext(h.ctx, http.MethodGet, h.server.URL+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		res, err := h.server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		data, err := io.ReadAll(io.LimitReader(res.Body, (64<<10)+1))
		if err != nil {
			t.Fatal(err)
		}
		return res.StatusCode, data
	}
	grantPath := "/api/daemon/operations/" + attempt.ID + "/task-input-capability"
	if status, _ := get(grantPath, config.Credential, nil); status != 410 {
		t.Fatal("dispatching Attempt acquired a read grant", status)
	}
	if _, err = h.store.AcceptAttempt(h.ctx, attempt.ID, config.RunnerID); err != nil {
		t.Fatal(err)
	}
	if status, _ := get(grantPath, other.Credential, nil); status != 410 {
		t.Fatal("foreign Runner received grant", status)
	}
	status, data := get(grantPath, config.Credential, nil)
	var grant p.TaskInputGrant
	if status != 200 || json.Unmarshal(data, &grant) != nil || grant.Scope != (p.TaskInputScope{Version: 1, After: 0, Through: 20}) {
		t.Fatal(status, string(data))
	}
	command, _ := h.store.GetAttempt(h.ctx, attempt.ID)
	if strings.Contains(string(command.Command), grant.Token) {
		t.Fatal("read credential leaked into command")
	}
	h.restartTaskServer(t)
	status, data = get(grantPath, config.Credential, nil)
	var replay p.TaskInputGrant
	if status != 200 || json.Unmarshal(data, &replay) != nil || replay != grant {
		t.Fatal("restart rotated the existing grant", status, string(data))
	}
	task, err = h.store.OperateTask(h.actor, task.ID, "inputs", storage.TaskOperation{InputProtocol: 1, PagedResponse: true, RequestID: newID(), Content: "Late input must remain invisible"})
	if err != nil {
		t.Fatal(err)
	}
	pagePath := grant.URL + "?after=0&limit=20"
	status, data = get(pagePath, grant.Token, nil)
	var page p.TaskInputPage
	if status != 200 || json.Unmarshal(data, &page) != nil || len(page.Items) != 20 || page.Through != 20 || len(data) > 64<<10 || strings.Contains(string(data), "Late input") {
		t.Fatal(status, string(data))
	}
	for _, auth := range []string{config.Credential, other.Credential, "invalid"} {
		if status, _ = get(pagePath, auth, nil); status != 410 {
			t.Fatal("wrong credential read originals", status)
		}
	}
	for _, headers := range []map[string]string{{"Cookie": "camp=browser"}, {"Origin": h.server.URL}} {
		if status, _ = get(pagePath, grant.Token, headers); status != 403 {
			t.Fatal("browser reached machine read", status)
		}
	}
	for _, query := range []string{"?after=21&limit=1", "?after=0&limit=21", "?after=0&limit=1&taskId=other", "?after=0&after=1&limit=1"} {
		if status, _ = get(grant.URL+query, grant.Token, nil); status == 200 {
			t.Fatal("foreign or oversized range read", query)
		}
	}
	if _, err = h.store.OperateTask(h.actor, task.ID, "cancel", storage.TaskOperation{PagedResponse: true, RequestID: newID(), Revision: task.Revision}); err != nil {
		t.Fatal(err)
	}
	if status, _ = get(pagePath, grant.Token, nil); status != 410 {
		t.Fatal("cancelled Attempt retained history", status)
	}
}

func TestTaskInputHTTPSPreservesExternalProvenance(t *testing.T) {
	h := newRemoteHarness(t, configuration.ModelInput{})
	// Claim and prepare manually without racing scheduler recovery of the same
	// unprepared durable Attempt.
	h.manager.Load().cancel()
	<-h.manager.Load().done
	config := h.join(t, "external-history-owner", "fixture", nil, nativeexecutor.Config{})
	if _, err := h.store.ConnectRunner(h.ctx, config.RunnerID, storage.RunnerConnection{StorageID: config.StorageID, Runtime: "fixture", Slots: 1, Capabilities: json.RawMessage(`{"executor":"fixture","ready":true,"taskInputsVersion":1}`)}); err != nil {
		t.Fatal(err)
	}
	room, writer := h.room(t, "External input history", config.RunnerID)
	credential := strings.Repeat("a", 48)
	source, err := h.store.CreateGitHubSource(h.actor, room.ID, storage.CreateGitHubSourceInput{RequestID: newID(), Repository: "owner/repo", Secret: strings.Repeat("s", 40)}, credential)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := h.store.ReceiveGitHubDelivery(h.ctx, source.ID, credential, "00000000-0000-4000-8000-000000000001", "issues", automationPayload("issues", "opened", "Original issue", 123, 70)); err != nil {
		t.Fatal(err)
	}
	task, err := h.manager.Load().CreateTask(h.actor, room.ID, storage.CreateTaskInput{InputProtocol: 1, PagedResponse: true, RequestID: newID(), Goal: "Preserve external input identities.", ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.SetTaskSubscription(h.actor, task.ID, orchestration.SubscriptionInput{RequestID: newID(), SourceID: source.ID, ThreadKey: "10:7", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	event, _, err := h.store.ReceiveGitHubDelivery(h.ctx, source.ID, credential, "00000000-0000-4000-8000-000000000002", "issue_comment", automationPayload("issue_comment", "created", strings.Repeat("External original ", 1000), 123, 201))
	if err != nil {
		t.Fatal(err)
	}
	run, attempt, err := h.store.ClaimRemoteRun(h.ctx, task.ActiveRunID, config.RunnerID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.manager.Load().prepare(h.ctx, run, attempt); err != nil {
		t.Fatal(err)
	}
	if _, err := h.store.AcceptAttempt(h.ctx, attempt.ID, config.RunnerID); err != nil {
		t.Fatal(err)
	}
	get := func(path, token string, value any) {
		t.Helper()
		req, err := http.NewRequestWithContext(h.ctx, http.MethodGet, h.server.URL+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
		response, err := h.server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
		if err != nil || response.StatusCode != 200 || len(data) > 64<<10 || json.Unmarshal(data, value) != nil {
			t.Fatalf("history response: status=%d body=%s error=%v", response.StatusCode, data, err)
		}
	}
	var grant p.TaskInputGrant
	get("/api/daemon/operations/"+attempt.ID+"/task-input-capability", config.Credential, &grant)
	var page p.TaskInputPage
	get(grant.URL+"?after=0&limit=20", grant.Token, &page)
	originals, err := h.store.TaskInputsPage(h.actor, task.ID, 0, nil, 20)
	if err != nil || len(page.Items) < 2 || len(page.Items) != len(originals.Items) {
		t.Fatal("external originals were not returned in full", page, originals, err)
	}
	for index, item := range page.Items {
		original := originals.Items[index]
		if item.AuthorKind != "external" || item.ExternalActorID != "123" || item.SourceEventID != "github:"+event.ID || item.AuthorUserID != "" || item.MessageID != original.MessageID || item.PartIndex != index+1 || item.Content != original.Content || item.ID != original.ID {
			t.Fatalf("history reread lost external provenance or bytes: %+v original=%+v", item, original)
		}
	}
}
