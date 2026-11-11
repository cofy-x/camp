package storage

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func TestRunStatesReadOnlyCurrentColumnsWithoutSnapshots(t *testing.T) {
	var reads []string
	s := observedReadStore(t, func(query string) { reads = append(reads, query) })
	room, err := s.SeedRoom(ctx, Resource{ID: "state-files", Name: "Files", CurrentCommit: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	active := sendRoom(t, s, room, "active-state").Runs[0]
	// Metadata must use the authoritative indexed columns even when unrelated
	// historical fields cannot be decoded as a Run and contain large originals.
	raw, _ := json.Marshal(map[string]any{"status": "failed", "agentId": "wrong-agent", "output": map[string]any{"retained": strings.Repeat("PRIVATE_OUTPUT", 18000)}, "files": []map[string]string{{"path": "app.txt", "after": strings.Repeat("PRIVATE_FILE", 50000)}}})
	if err = s.queries().SaveRun(ctx, sqlgen.SaveRunParams{ID: active.ID, Status: "checking", Snapshot: string(raw), UpdatedAt: active.UpdatedAt}); err != nil {
		t.Fatal(err)
	}
	queued := sendRoom(t, s, room, "queued-state").Runs[0]
	terminal := sendRoom(t, s, room, "terminal-state").Runs[0]
	terminal.Status = "failed"
	if err = saveRun(ctx, s.queries(), terminal); err != nil {
		t.Fatal(err)
	}
	reads = nil
	states, err := s.ListRunStates(ctx)
	if err != nil || len(states) != 2 || states[0] != (RunState{ID: queued.ID, AgentID: queued.AgentID, Status: "queued"}) || states[1] != (RunState{ID: active.ID, AgentID: active.AgentID, Status: "checking"}) {
		t.Fatal("state order or authority changed", states, err)
	}
	if len(reads) != 1 || strings.Contains(strings.ToLower(reads[0]), "snapshot") {
		t.Fatal("workspace state materialized frozen content", reads)
	}
	var after string
	if err := s.db.QueryRow("SELECT snapshot FROM runs WHERE id=?", active.ID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != string(raw) {
		t.Fatal("state read modified the original")
	}
}
