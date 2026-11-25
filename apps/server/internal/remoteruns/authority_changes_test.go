package remoteruns

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/collaboration"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestAgentOwnerCanDisableAgentWhileTeammateWorkIsRunning(t *testing.T) {
	m, store, requester, _, _ := proxyFixture(t, "http://127.0.0.1:1")
	ctx := t.Context()
	expires := time.Now().Add(time.Hour)
	if _, err := store.CreateInvite(requester, "agent-owner-invite", expires); err != nil {
		t.Fatal(err)
	}
	member, err := store.AcceptInvite(ctx, "agent-owner-invite", "agent-owner", "Agent owner", strings.Repeat("h", 64), "agent-owner-session", expires.Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	owner := storage.WithUser(ctx, member)
	_, token, err := store.CreateRunnerEnrollment(owner, "Owner's Runner", expires)
	if err != nil {
		t.Fatal(err)
	}
	runner, err := store.JoinRunner(ctx, storage.JoinRunnerInput{Token: token, RegistrationID: "agent-owner-runner", Credential: strings.Repeat("e", 64), StorageID: "agent-owner-storage"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.ConnectRunner(ctx, runner.ID, storage.RunnerConnection{StorageID: runner.StorageID, Runtime: "native", Slots: 1, Capabilities: json.RawMessage(`{"executor":"native","ready":true,"reasoningEffortVersion":1}`)}); err != nil {
		t.Fatal(err)
	}
	rooms, err := collaboration.New(ctx, store, m, m.config.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	room, err := rooms.CreateRoom(owner, "Shared work", "")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := rooms.CreateRoomAgent(owner, room.ID, storage.Agent{Name: "Shared Writer", Handle: "shared-writer", Access: "write", Enabled: true, PreferredRunnerID: runner.ID})
	if err != nil {
		t.Fatal(err)
	}
	send := func(requestID string) storage.Run {
		t.Helper()
		result, err := m.Send(requester, room.ID, storage.SendMessageInput{RequestID: requestID, Content: "Prepare a short note.", RecipientAgentIDs: []string{agent.ID}})
		if err != nil || len(result.Runs) != 1 {
			t.Fatal("shared Agent work was not accepted", result, err)
		}
		return result.Runs[0]
	}
	requested := send("active-shared-work")
	run, attempt, err := store.ClaimRemoteRun(ctx, requested.ID, runner.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.prepare(ctx, run, attempt); err != nil {
		t.Fatal(err)
	}
	if _, err = store.AcceptAttempt(ctx, attempt.ID, runner.ID); err != nil {
		t.Fatal(err)
	}
	queued := send("queued-shared-work")
	select {
	case <-m.wake:
	default:
	}

	agent.Enabled = false
	saved, err := rooms.SaveAgent(owner, agent)
	if err != nil || saved.Enabled || saved.Revision != agent.Revision+1 {
		t.Fatal("disabling the shared Agent did not return the committed revision", saved, err)
	}
	select {
	case <-m.wake:
	default:
		t.Fatal("scheduler was not notified of committed cancellation")
	}
	active, err := store.GetAttempt(ctx, attempt.ID)
	if err != nil || !active.CancelRequested {
		t.Fatal("the running Attempt retained execution authority", active.CancelRequested, err)
	}
	cancelled, err := store.GetRun(ctx, queued.ID)
	if err != nil || cancelled.Status != "cancelled" || cancelled.AttemptID != "" {
		t.Fatal("queued work was not cancelled before execution", cancelled.Status, cancelled.AttemptID, err)
	}
}

func TestSchedulerSettlesRevokedLegacyQueueWithoutAssigningRunner(t *testing.T) {
	m, store, actor, _, _ := proxyFixture(t, "http://127.0.0.1:1")
	rooms, err := collaboration.New(t.Context(), store, m, m.config.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	room, err := rooms.CreateRoom(actor, "Legacy requests", "")
	if err != nil {
		t.Fatal(err)
	}
	sent, err := store.SendMessage(actor, room.ID, storage.SendMessageInput{
		RequestID: "legacy-request", Content: "Draft a note.", RecipientAgentIDs: []string{room.DefaultAgentID},
	}, storage.Budget{Model: "legacy-model", MaxTokens: 1024, MaxSteps: 2, TimeoutSeconds: 60})
	if err != nil || len(sent.Runs) != 1 || sent.Runs[0].RunnerID != "" {
		t.Fatal("could not record a legacy local request", sent, err)
	}
	if err := store.LeaveRoom(actor, room.ID, room.DefaultAgentID); err != nil {
		t.Fatal(err)
	}
	revoked, err := store.GetRun(t.Context(), sent.Runs[0].ID)
	if err != nil || revoked.Status != "queued" || revoked.CancellationReason == "" {
		t.Fatal("legacy request lost its stored revocation", revoked, err)
	}
	if err := m.tick(t.Context()); err != nil {
		t.Fatal(err)
	}
	settled, err := store.GetRun(t.Context(), revoked.ID)
	if err != nil || settled.Status != "cancelled" || settled.RunnerID != "" || settled.CancellationReason != revoked.CancellationReason {
		t.Fatal("legacy request did not settle with its origin and reason", settled, err)
	}
}
