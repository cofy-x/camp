package runs

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestAgentStatesRetainsLocalAuthorityAndRejectsUnsavedTerminalQueue(t *testing.T) {
	ctx := context.Background()
	s, err := storage.Open(filepath.Join(t.TempDir(), "states.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	room, err := s.SeedRoom(ctx, storage.Resource{ID: "files", Name: "Files", CurrentCommit: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	failedAgent := newAgent(t, s, room, "unsaved", "read")
	storedAgent := newAgent(t, s, room, "stored-only", "read")
	send := func(key, agent string) storage.Run {
		t.Helper()
		result, err := s.SendMessage(ctx, room.ID, storage.SendMessageInput{RequestID: key, Content: "Work", RecipientAgentIDs: []string{agent}}, storage.Budget{MaxTokens: 32, MaxSteps: 2, TimeoutSeconds: 2})
		if err != nil {
			t.Fatal(err)
		}
		return result.Runs[0]
	}
	active := send("active", room.DefaultAgentID)
	send("queued-behind-active", room.DefaultAgentID)
	unsaved := send("cancelled-with-final-write-failure", failedAgent.ID)
	stored := send("stored-active-with-no-local-process", storedAgent.ID)
	stored.Status = "preparing"
	if _, err = s.UpdateRun(ctx, stored); err != nil {
		t.Fatal(err)
	}
	active.Status, unsaved.Status = "cancelling", "failed"
	m := &Manager{store: s, active: map[string]*execution{active.ID: {run: active}}, unsaved: map[string]storage.Run{unsaved.ID: unsaved}}
	eventsBefore, _ := s.ListWorkspaceEvents(ctx, 0)
	states, err := m.AgentStates(ctx)
	if err != nil || len(states) != 1 || states[active.AgentID] != "cancelling" {
		t.Fatal("local process priority or terminal authority lost", states, err)
	}
	full, err := m.Get(ctx, unsaved.ID)
	if err != nil || full.Status != "failed" {
		t.Fatal(full, err)
	}
	summary, err := m.GetRunSummary(ctx, unsaved.ID)
	if err != nil || summary.Status != full.Status {
		t.Fatal("summary and Agent state authority diverged", summary, err)
	}
	retained, err := s.GetRun(ctx, unsaved.ID)
	if err != nil || retained.Status != "queued" {
		t.Fatal("state read persisted terminal overlay", retained, err)
	}
	eventsAfter, _ := s.ListWorkspaceEvents(ctx, 0)
	if len(eventsAfter) != len(eventsBefore) {
		t.Fatal("state reads emitted events")
	}
	// A distinct legitimate request for the same Agent remains queued.
	send("other-queued-request", failedAgent.ID)
	states, err = m.AgentStates(ctx)
	if err != nil || states[failedAgent.ID] != "queued" || states[active.AgentID] != "cancelling" {
		t.Fatal("terminal overlay hid another request", states, err)
	}
}
