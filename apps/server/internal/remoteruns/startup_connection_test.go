package remoteruns

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/nativeexecutor"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

func TestRemoteStartupRejectsStaleOnlineReadiness(t *testing.T) {
	h := newRemoteHarness(t, configuration.ModelInput{})
	running := h.start(t, h.join(t, "prior-ready-boot", "fixture", nil, nativeexecutor.Config{}))
	before, err := h.store.GetRunner(h.ctx, running.config.RunnerID)
	if err != nil {
		t.Fatal(err)
	}
	var caps p.Capabilities
	if err := json.Unmarshal(before.Capabilities, &caps); err != nil || !caps.Ready || !h.manager.Load().IsOnline(before.ID) {
		t.Fatal("negative control requires a ready prior connection", caps, err)
	}
	// The former start helper accepted these exact stale online/ready facts.
	// A blocked replacement has not yet sent a HELLO or proved native cleanup.
	if currentTestConnection(h.manager.Load(), before, before.ConnectionGeneration) {
		t.Fatal("prior online readiness was accepted as replacement startup")
	}
	// ConnectRunner persists an epoch before replacing the in-memory peer. An
	// advanced database observation alone must not select the previous peer.
	advanced := before
	advanced.ConnectionGeneration++
	if currentTestConnection(h.manager.Load(), advanced, before.ConnectionGeneration) {
		t.Fatal("an advanced observation selected the prior peer epoch")
	}
	h.manager.Load().StopRunner(before.ID)
	remoteEventually(t, 15*time.Second, "fresh peer did not satisfy startup observation", func() bool {
		current, err := h.store.GetRunner(h.ctx, before.ID)
		return err == nil && currentTestConnection(h.manager.Load(), current, before.ConnectionGeneration)
	})
}
