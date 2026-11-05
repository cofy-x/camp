package httpapi

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestAutomationEvaluationContextHTTPFrozenReadsAndBounds(t *testing.T) {
	f, runner := remoteTeamFixture(t)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PATCH", "/api/runners/"+runner.ID, map[string]any{"teamShared": true}), 200)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PATCH", "/api/execution-defaults", map[string]any{"scope": "team", "runnerId": runner.ID, "revision": 0}), 200)
	actor := storage.WithUser(context.Background(), f.admin)
	policy, err := f.store.SaveAutomationPolicy(actor, f.room.ID, "room", orchestration.PolicyInput{RequestID: "context-policy", Enabled: true, Mode: "observe", Rules: "Observe clear requests", AllowedActorIDs: []string{f.admin.ID}, ParticipantAgentIDs: []string{f.room.DefaultAgentID}, FirstAgentID: f.room.DefaultAgentID, DailyEvaluations: 5, DailyTasks: 2})
	if err != nil {
		t.Fatal(err)
	}
	memory, err := f.store.SaveMemory(actor, f.room.ID, "", orchestration.MemoryInput{RequestID: "remember", Content: "Frozen human note", Provenance: orchestration.MemoryProvenance{Kind: "human_note"}})
	if err != nil {
		t.Fatal(err)
	}
	message, err := f.store.SendMessage(actor, f.room.ID, storage.SendMessageInput{RequestID: "assess-context", Content: "Review the request"}, storage.Budget{MaxTokens: 4096, MaxSteps: 8, TimeoutSeconds: 180})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = f.store.CreateWorkItem(actor, f.room.ID, orchestration.WorkItemInput{RequestID: "matter", Goal: "Follow request", CompletionCriteria: "A confirmed answer", PolicyID: policy.ID, EvidenceIDs: []string{"room:" + message.Message.ID}}); err != nil {
		t.Fatal(err)
	}
	candidate, err := f.store.ClaimAutomationEvaluation(context.Background(), "frozen-model", storage.Budget{MaxTokens: 4096, MaxSteps: 8, TimeoutSeconds: 180})
	if err != nil || candidate == nil {
		t.Fatal(candidate, err)
	}
	path := "/api/automation-evaluations/" + candidate.ID + "/context/memories"
	requireHTTP(t, teamRequest(t, f, nil, "GET", path, nil), 401)
	response := teamRequest(t, f, f.memberCookie, "GET", path+"?limit=1", nil)
	requireHTTP(t, response, 200)
	var page storage.AutomationContextPage
	if err = json.Unmarshal(response.Body.Bytes(), &page); err != nil || len(page.Items) != 1 || page.Total != 1 || page.NextAfter != 1 {
		t.Fatal(response.Body.String(), err)
	}
	var frozen orchestration.Memory
	if err = json.Unmarshal(page.Items[0], &frozen); err != nil || frozen.ID != memory.ID || frozen.Revision != 1 || frozen.Content != memory.Content {
		t.Fatal(frozen, err)
	}
	for _, query := range []string{"after=-1", "after=2", "after=0&after=1", "limit=11", "limit=1&limit=2"} {
		requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", path+"?"+query, nil), 400)
	}
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/automation-evaluations/"+candidate.ID+"/context/work-items", nil), 200)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/automation-evaluations/"+candidate.ID+"/context/unknown", nil), 400)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/automation-evaluations/missing/context/memories", nil), 404)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "POST", path, map[string]any{}), 404)
}
