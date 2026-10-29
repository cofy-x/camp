package nativeexecutor

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"testing"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

func TestCoordinationPersistsOneApprovedDecisionBeforeAcknowledging(t *testing.T) {
	var mu sync.Mutex
	var order []string
	server, err := newCoordination(context.Background(), p.Collaboration{ID: "task", AgentID: "writer", Participants: []p.Participant{{ID: "writer"}, {ID: "reviewer"}}}, func(_ context.Context, i p.CoordinationIntent) error {
		mu.Lock()
		defer mu.Unlock()
		order = append(order, "persist")
		return nil
	}, func(context.Context) error { mu.Lock(); defer mu.Unlock(); order = append(order, "seal"); return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer server.close()
	send := func(intent p.CoordinationIntent, token string) int {
		t.Helper()
		body, _ := json.Marshal(intent)
		req, _ := http.NewRequest("POST", server.endpoint()+"/intent", bytes.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}
	intent := p.CoordinationIntent{CallID: "call", Kind: "handoff", TargetAgentID: "unknown", Instruction: "Review", ArtifactPaths: []string{"result.txt"}}
	if code := send(intent, server.token); code != 400 {
		t.Fatal(code)
	}
	intent.TargetAgentID = "reviewer"
	if code := send(intent, "wrong"); code != 403 {
		t.Fatal(code)
	}
	if server.result() != nil {
		t.Fatal("unapproved decision persisted")
	}
	if code := send(intent, server.token); code != 200 {
		t.Fatal(code)
	}
	if code := send(intent, server.token); code != 200 {
		t.Fatal("replay", code)
	}
	changed := intent
	changed.Instruction = "Different"
	if code := send(changed, server.token); code != 409 {
		t.Fatal("changed replay", code)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "seal" || order[1] != "persist" {
		t.Fatal("ACK did not follow sealing and one persist", order)
	}
}
func TestCoordinationRejectsUnsafeArtifactsAndJournalFailure(t *testing.T) {
	task := p.Collaboration{ID: "task", AgentID: "writer", Participants: []p.Participant{{ID: "writer"}}}
	for _, file := range []string{"../outside", "/host", "a/../b", "a//b", "a\\b", "."} {
		if validateCoordination(task, p.CoordinationIntent{CallID: "call", Kind: "finish", Summary: "Done", ArtifactPaths: []string{file}}) == nil {
			t.Fatal("unsafe artifact", file)
		}
	}
	server, err := newCoordination(context.Background(), task, func(context.Context, p.CoordinationIntent) error { return errors.New("disk full") }, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer server.close()
	body := bytes.NewBufferString(`{"callId":"call","kind":"finish","summary":"Done","artifactPaths":[]}`)
	request, _ := http.NewRequest("POST", server.endpoint()+"/intent", body)
	request.Header.Set("Authorization", "Bearer "+server.token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	if response.StatusCode != 503 || server.result() != nil {
		t.Fatal("undurable decision acknowledged", response.StatusCode)
	}
}
