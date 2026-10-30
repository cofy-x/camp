package storage

import (
	"strings"
	"testing"
	"time"
)

func TestRemoteAgentQueueCannotBeOvertakenOnAnotherRunner(t *testing.T) {
	for _, scope := range []string{"same-room", "other-room", "other-agent"} {
		t.Run(scope, func(t *testing.T) {
			s := testStore(t)
			owner, _ := teamUsers(t, s)
			a, _ := enrollTestRunner(t, s, owner, "alpha")
			b, _ := enrollTestRunner(t, s, owner, "bravo")
			room, agent := remoteTestRoom(t, s, owner, a)
			older := sendRemoteTestRun(t, s, owner, room, agent, "older")
			var err error
			if scope == "other-room" {
				room, err = s.CreateRoomWithResource(owner, Room{Name: "Another room", DefaultAgentID: agent.ID}, Resource{Name: "Other files", CurrentCommit: strings.Repeat("b", 40)})
				if err != nil {
					t.Fatal(err)
				}
			}
			room, err = s.SetRoomRunner(owner, room.ID, b.ID, room.RunnerRevision)
			if err != nil {
				t.Fatal(err)
			}
			if scope == "other-agent" {
				agent, err = s.CreateRoomAgent(owner, room.ID, Agent{Name: "Other Agent", Handle: "other-agent", Access: "write", Enabled: true, PreferredRunnerID: b.ID})
				if err != nil {
					t.Fatal(err)
				}
			}
			newer := sendRemoteTestRun(t, s, owner, room, agent, "newer")
			if older.RunnerID != a.ID || newer.RunnerID != b.ID {
				t.Fatal("admission did not preserve the two Runner targets")
			}
			_, _, err = s.ClaimRemoteRun(ctx, newer.ID, b.ID, time.Minute)
			if scope == "other-agent" {
				if err != nil {
					t.Fatal("another Agent was blocked by an unrelated queue", err)
				}
				return
			}
			requireCode(t, err, "run_busy")
			waiting, err := s.GetRun(ctx, newer.ID)
			if err != nil || waiting.Status != "queued" || waiting.AttemptID != "" || waiting.WriterFence != 0 {
				t.Fatal("a waiting request acquired execution state", waiting, err)
			}
			cancelled, err := s.CancelRemoteRun(owner, older.ID, "Cancel the queued predecessor.")
			if err != nil || cancelled.Status != "cancelled" {
				t.Fatal(cancelled, err)
			}
			claimed, _, err := s.ClaimRemoteRun(ctx, newer.ID, b.ID, time.Minute)
			if err != nil || claimed.RunnerID != b.ID || claimed.Status != "preparing" {
				t.Fatal("cancelling the predecessor did not release its successor", claimed, err)
			}
		})
	}
}
