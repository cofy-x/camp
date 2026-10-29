package remoteruns

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestInvalidTaskIntentHasImmediateDurableRejection(t *testing.T) {
	for _, intent := range []*p.CoordinationIntent{nil, {CallID: "handoff", Kind: "handoff", TargetAgentID: "unapproved", Instruction: "Wrong target", ArtifactPaths: []string{}}} {
		t.Run(map[bool]string{true: "missing", false: "unapproved"}[intent == nil], func(t *testing.T) {
			manager, store, actor, previous, _ := proxyFixture(t, "http://127.0.0.1:1")
			prior, err := store.InterruptAttempt(actor, previous.ID, "Release focused fixture execution.")
			if err != nil {
				t.Fatal(err)
			}
			agent, err := store.CreateRoomAgent(actor, prior.RoomID, storage.Agent{Name: "Reader", Handle: "reader", Access: "read", Enabled: true, PreferredRunnerID: prior.RunnerID})
			if err != nil {
				t.Fatal(err)
			}
			task, err := store.CreateTask(actor, prior.RoomID, storage.CreateTaskInput{RequestID: "collaboration", Goal: "Read and finish.", ParticipantAgentIDs: []string{agent.ID}, FirstAgentID: agent.ID}, storage.Budget{MaxTokens: 4096, MaxSteps: 8, TimeoutSeconds: 180, Model: prior.Model, RuntimeKey: prior.RuntimeKey})
			if err != nil {
				t.Fatal(err)
			}
			run, attempt, err := store.ClaimRemoteRun(actor, task.ActiveRunID, prior.RunnerID, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			if err = manager.prepare(actor, run, attempt); err != nil {
				t.Fatal(err)
			}
			if _, err = store.AcceptAttempt(actor, attempt.ID, prior.RunnerID); err != nil {
				t.Fatal(err)
			}
			if _, err = store.BeginFinalizing(actor, attempt.ID, prior.RunnerID); err != nil {
				t.Fatal(err)
			}
			runner, err := store.GetRunner(actor, prior.RunnerID)
			if err != nil {
				t.Fatal(err)
			}
			result := p.Result{CompletionID: "result", Status: "succeeded", Summary: "Done", Checks: []p.CheckResult{}, CleanupConfirmed: true, Intent: intent}
			raw, _ := json.Marshal(result)
			for range 2 {
				request := httptest.NewRequest("POST", "http://localhost/complete", bytes.NewReader(raw)).WithContext(actor)
				request.SetPathValue("id", attempt.ID)
				response := httptest.NewRecorder()
				manager.complete(response, request, runner)
				var receipt p.Receipt
				if json.Unmarshal(response.Body.Bytes(), &receipt) != nil || receipt.Disposition != "rejected" || !strings.Contains(receipt.Reason, "Runner result rejected") {
					t.Fatal("missing durable rejection", response.Body.String())
				}
			}
			current, err := store.GetTask(actor, task.ID)
			if err != nil || current.Status != "needs_attention" || current.ActiveRunID != "" {
				t.Fatal("task retained rejected attempt", current, err)
			}
			saved, err := store.GetAttempt(actor, attempt.ID)
			if err != nil || saved.Phase != "finished" {
				t.Fatal("attempt remained live after rejected receipt", saved, err)
			}
		})
	}
}
