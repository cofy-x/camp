package storage

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

func TestRunnerCapabilitiesMatchProtocolBoundaries(t *testing.T) {
	s := testStore(t)
	owner, _ := teamUsers(t, s)
	runner, join := enrollTestRunner(t, s, owner, "alpha")
	valid := []RunnerSource{{ID: strings.Repeat("a", p.MaxSourceIDLength), Alias: "Human-readable source title", Revision: "v1", Available: true}}
	for i := 1; i < p.MaxSources; i++ {
		valid = append(valid, RunnerSource{ID: fmt.Sprintf("source-%d", i), Alias: "Source", Revision: "v1"})
	}
	for _, test := range []struct {
		sources []RunnerSource
		slots   int
	}{
		{[]RunnerSource{{ID: "Upper", Alias: "Title", Revision: "v1"}}, 1},
		{append(append([]RunnerSource{}, valid...), RunnerSource{ID: "extra", Alias: "Title", Revision: "v1"}), 1},
		{nil, p.MaxSlots + 1},
	} {
		_, err := s.ConnectRunner(ctx, runner.ID, RunnerConnection{StorageID: join.StorageID, Runtime: "fixture", Slots: test.slots, Capabilities: json.RawMessage(`{}`), Sources: test.sources})
		requireCode(t, err, "invalid_runner")
		current, err := s.GetRunner(ctx, runner.ID)
		if err != nil || current.ConnectionGeneration != runner.ConnectionGeneration {
			t.Fatal("invalid report displaced an existing connection", current, err)
		}
	}
	connected, err := s.ConnectRunner(ctx, runner.ID, RunnerConnection{StorageID: join.StorageID, Runtime: "fixture", Slots: p.MaxSlots, Capabilities: json.RawMessage(`{}`), Sources: valid})
	if err != nil || len(connected.Sources) != p.MaxSources || connected.Slots != p.MaxSlots {
		t.Fatal("valid boundary capabilities rejected", connected, err)
	}
}
