package remoteruns

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/cofy-x/camp/apps/server/internal/nativeexecutor"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/runs"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestRejectedSourceCompletionImmediatelyReleasesRunnerSlot(t *testing.T) {
	h := newRemoteHarness(t, runs.Config{})
	config := h.join(t, "source-review", "fixture", nil, nativeexecutor.Config{})
	_, err := h.store.ConnectRunner(h.ctx, config.RunnerID, storage.RunnerConnection{StorageID: config.StorageID, Runtime: "fixture", Slots: 1, Capabilities: json.RawMessage(`{"executor":"fixture","ready":true}`), Sources: []storage.RunnerSource{{ID: "project", Alias: "Project", Revision: "source-v1", Available: true}}})
	if err != nil {
		t.Fatal(err)
	}
	input := storage.SourceImportInput{RequestID: "first", RunnerID: config.RunnerID, SourceID: "project", ExpectedSourceRevision: "source-v1", RoomName: "Review source"}
	item, err := h.store.CreateSourceImport(h.actor, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.store.ClaimSourceImport(h.ctx, item.ID, config.RunnerID, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(p.Result{CompletionID: "missing-artifact", Status: "succeeded", CleanupConfirmed: true, Checks: []p.CheckResult{}})
	receipt := h.request(t, config, "POST", "/api/daemon/operations/"+item.ID+"/complete", body, nil)
	if receipt.Disposition != "rejected" {
		t.Fatal(receipt)
	}
	item, err = h.store.GetSourceImport(h.ctx, item.ID)
	if err != nil || !terminal(item.Status) {
		t.Fatalf("rejected source still owns its execution slot: %+v %v", item, err)
	}
	input.RequestID = "second"
	next, err := h.store.CreateSourceImport(h.actor, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.store.ClaimSourceImport(h.ctx, next.ID, config.RunnerID, time.Minute); err != nil {
		t.Fatalf("rejected source did not release its Runner slot: %v", err)
	}
}

func TestDuplicateAcceptanceAfterCommitDoesNotRejectDurableCompletion(t *testing.T) {
	h := newRemoteHarness(t, runs.Config{})
	d := h.start(t, h.join(t, "acceptance-review", "fixture", nil, nativeexecutor.Config{}))
	room, agent := h.room(t, "Acceptance review", d.config.RunnerID)
	run := h.finished(t, h.send(t, room, agent, "Complete once.").ID)
	if run.Status != "succeeded" {
		t.Fatal(run)
	}
	d.stop(t)
	attempt, err := h.store.GetAttempt(h.ctx, run.AttemptID)
	if err != nil {
		t.Fatal(err)
	}
	connection, _, err := websocket.Dial(h.ctx, strings.Replace(h.server.URL, "https:", "wss:", 1)+"/api/daemon/connect", &websocket.DialOptions{HTTPClient: h.server.Client(), HTTPHeader: http.Header{"Authorization": []string{"Bearer " + d.config.Credential}}})
	if err != nil {
		t.Fatal(err)
	}
	defer connection.CloseNow()
	if err = wsjson.Write(h.ctx, connection, p.Message{Type: "hello", RequestID: "review-hello", Hello: &p.Hello{Version: p.Version, StorageID: d.config.StorageID, BootID: "review", Capabilities: p.Capabilities{Executor: "fixture", Ready: true, Slots: 1}}}); err != nil {
		t.Fatal(err)
	}
	var welcome p.Message
	if err = wsjson.Read(h.ctx, connection, &welcome); err != nil || welcome.Type != "welcome" {
		t.Fatal(welcome, err)
	}
	if err = wsjson.Write(h.ctx, connection, p.Message{Type: "accepted", Epoch: welcome.Epoch, RequestID: "duplicate-acceptance", OperationID: attempt.ID, CommandID: attempt.CommandID}); err != nil {
		t.Fatal(err)
	}
	// The next request is a read barrier: its deterministic response proves the
	// duplicate acceptance was processed without an earlier rejection frame.
	if err = wsjson.Write(h.ctx, connection, p.Message{Type: "heartbeat", Epoch: welcome.Epoch, RequestID: "barrier", Active: []string{"unknown-review-operation"}}); err != nil {
		t.Fatal(err)
	}
	var response p.Message
	if err = wsjson.Read(h.ctx, connection, &response); err != nil || response.Type != "rejected" || response.OperationID != "unknown-review-operation" {
		t.Fatalf("duplicate acceptance rejected a committed operation: %+v %v", response, err)
	}
}

func TestRejectedArchiveUploadImmediatelySealsOperations(t *testing.T) {
	for _, kind := range []string{"run", "source"} {
		t.Run(kind, func(t *testing.T) {
			h := newRemoteHarness(t, runs.Config{})
			// Claim and prepare manually without racing the normal scheduler's
			// restart recovery of an unprepared durable attempt.
			h.manager.Load().cancel()
			<-h.manager.Load().done
			config := h.join(t, "upload-review", "fixture", nil, nativeexecutor.Config{})
			_, err := h.store.ConnectRunner(h.ctx, config.RunnerID, storage.RunnerConnection{StorageID: config.StorageID, Runtime: "fixture", Slots: 1, Capabilities: json.RawMessage(`{"executor":"fixture","ready":true}`), Sources: []storage.RunnerSource{{ID: "project", Alias: "Project", Revision: "source-v1", Available: true}}})
			if err != nil {
				t.Fatal(err)
			}
			input := storage.SourceImportInput{RequestID: "source", RunnerID: config.RunnerID, SourceID: "project", ExpectedSourceRevision: "source-v1", RoomName: "Upload review"}
			var operationID string
			if kind == "source" {
				item, err := h.store.CreateSourceImport(h.actor, input)
				if err != nil {
					t.Fatal(err)
				}
				if _, err = h.store.ClaimSourceImport(h.ctx, item.ID, config.RunnerID, 5*time.Minute); err != nil {
					t.Fatal(err)
				}
				operationID = item.ID
			} else {
				room, agent := h.room(t, "Upload review", config.RunnerID)
				run := h.send(t, room, agent, "Complete once.")
				claimed, attempt, err := h.store.ClaimRemoteRun(h.ctx, run.ID, config.RunnerID, time.Minute)
				if err != nil {
					t.Fatal(err)
				}
				if err = h.manager.Load().prepare(h.ctx, claimed, attempt); err != nil {
					t.Fatal(err)
				}
				if _, err = h.store.AcceptAttempt(h.ctx, attempt.ID, config.RunnerID); err != nil {
					t.Fatal(err)
				}
				operationID = attempt.ID
			}
			base := "/api/daemon/operations/" + operationID
			if receipt := h.request(t, config, "POST", base+"/finalizing", []byte(`{}`), nil); receipt.Disposition != "accepted" {
				t.Fatal(receipt)
			}
			receipt := h.request(t, config, "PUT", base+"/artifact", []byte("corrupted archive"), map[string]string{"X-Camp-SHA256": strings.Repeat("f", 64), "Content-Type": "application/x-tar"})
			if receipt.Disposition != "rejected" {
				t.Fatal(receipt)
			}
			if kind == "source" {
				item, err := h.store.GetSourceImport(h.ctx, operationID)
				if err != nil || !terminal(item.Status) {
					t.Fatalf("source retained a slot after terminal upload rejection: %+v %v", item, err)
				}
			} else {
				attempt, err := h.store.GetAttempt(h.ctx, operationID)
				if err != nil || attempt.Phase != "finished" {
					t.Fatalf("run retained a slot after terminal upload rejection: %+v %v", attempt, err)
				}
			}
			input.RequestID = "next-source"
			input.RoomName = "Next imported snapshot"
			next, err := h.store.CreateSourceImport(h.actor, input)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = h.store.ClaimSourceImport(h.ctx, next.ID, config.RunnerID, time.Minute); err != nil {
				t.Fatalf("terminal upload rejection did not release its Runner slot: %v", err)
			}
		})
	}
}
