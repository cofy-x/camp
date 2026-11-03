package documenttext

import (
	"testing"
	"time"
)

func TestStaleProbeCannotReplaceSuspendedReaderOwnership(t *testing.T) {
	engine := New(Config{})
	engine.runMu.Lock()
	engine.suspended = errReaderOwnership
	engine.markUnavailable()
	done := make(chan struct{})
	go func() {
		engine.publishCheck(Check{Format: "pdf", Status: "ready", Detail: "Earlier successful probe"})
		close(done)
	}()
	select {
	case <-done:
		engine.runMu.Unlock()
		t.Fatal("probe published while execution ownership was being changed")
	case <-time.After(20 * time.Millisecond):
	}
	engine.runMu.Unlock()
	<-done
	for _, check := range engine.Readiness() {
		if check.Status != "unavailable" {
			t.Fatal("stale probe replaced an unknown-owner observation", check)
		}
	}
}
