package daemon

import (
	"errors"
	"runtime"
	"testing"
)

func TestUnavailableBackendDoesNotInventRuntimeVersionsOrAdapters(t *testing.T) {
	d := &Daemon{config: Config{Executor: "native", Slots: 1}, failure: errors.New("runtime verification failed")}
	observed := d.capabilities()
	if observed.Ready || observed.Reason == "" || observed.Platform != runtime.GOOS+"/"+runtime.GOARCH {
		t.Fatal("failed startup lost its host or failure observation", observed)
	}
	if observed.DSHVersion != "" || observed.LayerFSVersion != "" || observed.Workspace != "" || observed.Sandbox != "" || observed.Checkpoint != "" {
		t.Fatal("an unopened runtime was reported as an installed execution adapter", observed)
	}
}
