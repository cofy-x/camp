package remoteruns

import (
	"reflect"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestAgentStatesMatchesQueueAndActivePriorityWithoutOriginalPayloads(t *testing.T) {
	m, s, actor, attempt, _ := proxyFixture(t, "http://127.0.0.1:1")
	active, err := s.GetRun(actor, attempt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	room, err := s.GetRoom(actor, active.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = m.Send(actor, room.ID, storage.SendMessageInput{RequestID: "queued-behind-active", Content: "Wait", RecipientAgentIDs: []string{active.AgentID}, RunnerRevision: &room.RunnerRevision}); err != nil {
		t.Fatal(err)
	}
	active.Output = strings.Repeat("PRIVATE_OUTPUT", 18000)
	active.Files = []storage.RunFile{{Path: "app.txt", Status: "added", After: strings.Repeat("PRIVATE_FILE", 50000)}}
	for _, phase := range []string{"running", "checking", "cancelling"} {
		active.Status = phase
		if active, err = s.UpdateRun(actor, active); err != nil {
			t.Fatal(err)
		}
		queued, err := s.ListQueuedRuns(actor)
		if err != nil {
			t.Fatal(err)
		}
		unfinished, err := s.ListUnfinishedRuns(actor)
		if err != nil {
			t.Fatal(err)
		}
		want := map[string]string{}
		for _, run := range append(queued, unfinished...) {
			want[run.AgentID] = run.Status
		}
		states, err := m.AgentStates(actor)
		if err != nil || !reflect.DeepEqual(states, want) || states[active.AgentID] != phase {
			t.Fatal("metadata changed active/queued state precedence", states, want, err)
		}
	}
}
