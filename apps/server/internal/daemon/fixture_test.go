package daemon

import (
	"context"
	"strings"
	"testing"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

func TestFixtureIgnoresFaultMarkersInEarlierContext(t *testing.T) {
	d := &Daemon{config: Config{Name: "fixture"}}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, _, err := d.fixture(ctx, p.ExecutionSpec{Access: "read", Request: "Summarize this revision.", Prompt: "An old cancelled request contained [fixture:wait] and [fixture:fail].\nCurrent request: Summarize this revision."}, t.TempDir(), func(string, string) error { return nil })
	if err != nil || result.Status != "succeeded" {
		t.Fatalf("prior history injected a fixture failure: %s %v", result.Status, err)
	}
	_, _, err = d.fixture(context.Background(), p.ExecutionSpec{Access: "read", Request: "[fixture:fail]", Prompt: "Visible context."}, t.TempDir(), func(string, string) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "fixture execution failure") {
		t.Fatal("current fixture scenario was not applied", err)
	}
}
