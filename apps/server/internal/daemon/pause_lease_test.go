package daemon

import (
	"context"
	"sync"
	"testing"
	"time"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

// Represent elapsed control-plane time using real monotonic timestamps rather
// than sleeping a host or inventing an OS-native guarantee. This composes the
// delayed-grant boundary with the actual scheduler watchdog and durable inbox.
func TestControlledLongPauseCannotRenewExpiredWorkOrReplayIt(t *testing.T) {
	for _, pause := range []time.Duration{10 * time.Second, 3 * time.Minute} {
		t.Run(pause.String(), func(t *testing.T) {
			ctx := context.Background()
			journal, err := openJournal(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			defer journal.db.Close()
			command := testCommand()
			if _, err = journal.accept(ctx, command); err != nil {
				t.Fatal(err)
			}
			if started, err := journal.begin(ctx, command.OperationID); err != nil || !started {
				t.Fatal("Fixture did not reach running state", started, err)
			}
			deadline := time.Now().Add(-time.Second)
			stopped := make(chan struct{})
			var once sync.Once
			d := Daemon{journal: journal, grants: map[string]grant{command.OperationID: {until: deadline}}, grantRequests: map[string]time.Time{"before-pause": time.Now().Add(-pause)}, active: map[string]*activeOperation{command.OperationID: {deadline: deadline, cancel: func() { once.Do(func() { close(stopped) }) }}}}
			// A delayed reply cannot borrow delivery time or a future wall-clock expiry
			// to revive the old grant, even if it arrives before the watchdog runs again.
			d.setGrant(command.OperationID, "before-pause", 5000, time.Now().Add(time.Hour).Format(time.RFC3339Nano))
			if !d.grants[command.OperationID].until.Equal(deadline) || !d.active[command.OperationID].deadline.Equal(deadline) {
				t.Fatal("Delayed control reply revived expired authority")
			}
			running, cancel := context.WithCancel(ctx)
			done := make(chan struct{})
			go func() { defer close(done); d.schedule(running) }()
			select {
			case <-stopped:
			case <-time.After(3 * time.Second):
				cancel()
				<-done
				t.Fatal("Resumed watchdog did not cancel expired work")
			}
			cancel()
			<-done
			// Once cleanup records interruption, redelivery/reconnection cannot begin
			// the same durable command again, regardless of a later control connection.
			if err = journal.finish(ctx, command.OperationID, p.Result{CompletionID: "paused-interruption", Status: "interrupted", CleanupConfirmed: true, Checks: []p.CheckResult{}}, ""); err != nil {
				t.Fatal(err)
			}
			if created, err := journal.accept(ctx, command); err != nil || created {
				t.Fatal("Redelivery created another paused execution", created, err)
			}
			if started, err := journal.begin(ctx, command.OperationID); err != nil || started {
				t.Fatal("Reconnection replayed interrupted execution", started, err)
			}
			events, err := journal.events(ctx)
			if err != nil || len(events) != 0 {
				t.Fatal("Paused execution emitted fresh tool/model events", events, err)
			}
		})
	}
}
