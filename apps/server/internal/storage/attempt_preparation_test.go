package storage

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func preparationFixture(t *testing.T, access string) (*Store, context.Context, Run, Attempt, PreparedAttempt) {
	t.Helper()
	s, actor, _, room, writer, _ := taskFixture(t)
	writer.Access = access
	if _, err := s.SaveAgent(actor, writer); err != nil {
		t.Fatal(err)
	}
	budget := roomBudget
	budget.RequireRunner = true
	sent, err := s.SendMessage(actor, room.ID, SendMessageInput{RequestID: "preparation", Content: "Use the immutable files.", RecipientAgentIDs: []string{writer.ID}, RunnerRevision: &room.RunnerRevision}, budget)
	if err != nil {
		t.Fatal(err)
	}
	run, a, err := s.ClaimRemoteRun(ctx, sent.Runs[0].ID, sent.Runs[0].RunnerID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if a.Preparation == nil {
		t.Fatal("claim did not freeze preparation")
	}
	command, _ := json.Marshal(map[string]string{"id": a.CommandID, "operationId": a.ID, "kind": "run.start"})
	in := PreparedAttempt{Conversation: *a.Preparation, InputDigest: strings.Repeat("a", 64), InputPath: filepath.Join(t.TempDir(), "project.tar"), Command: command, ModelTokenHash: strings.Repeat("b", 64)}
	return s, actor, run, a, in
}

func TestAttemptPreparationFreezeAndAtomicActivation(t *testing.T) {
	s, _, run, a, in := preparationFixture(t, "write")
	for range 3 {
		frozen, err := s.FreezeAttemptPreparation(ctx, a.ID)
		if err != nil || frozen.Preparation == nil || *frozen.Preparation != in.Conversation {
			t.Fatal("allocated another session during rebuild", frozen, err)
		}
	}
	bad := in
	bad.Command = json.RawMessage(`{"id":"wrong-command","operationId":"wrong","kind":"run.start"}`)
	_, err := s.ActivateAttempt(ctx, a.ID, bad)
	requireCode(t, err, "invalid_command")
	before, _ := s.GetAttempt(ctx, a.ID)
	unchanged, _ := s.GetRun(ctx, run.ID)
	if len(before.Command) > 0 || before.InputDigest != "" || before.ModelTokenHash != a.ModelTokenHash || unchanged.SessionID != "" {
		t.Fatal("failed activation partially committed", before, unchanged)
	}
	activated, err := s.ActivateAttempt(ctx, a.ID, in)
	if err != nil || activated.InputDigest != in.InputDigest || string(activated.Command) != string(in.Command) || activated.ModelTokenHash != in.ModelTokenHash {
		t.Fatal(activated, err)
	}
	saved, _ := s.GetRun(ctx, run.ID)
	if saved.SessionID != in.Conversation.SessionID || saved.SessionGeneration != in.Conversation.Generation {
		t.Fatal("session metadata did not activate with the command", saved)
	}
	// Simulate a lost activation reply. The exact payload and capability survive.
	replayed, err := s.ActivateAttempt(ctx, a.ID, in)
	if err != nil || string(replayed.Command) != string(activated.Command) {
		t.Fatal(replayed, err)
	}
	in.ModelTokenHash = strings.Repeat("c", 64)
	_, err = s.ActivateAttempt(ctx, a.ID, in)
	requireCode(t, err, "request_conflict")
	assertScalar(t, s, "SELECT count(*) FROM runner_commands WHERE kind='run.start'", 1)
}

func TestPreparationActivationRechecksAuthorityAndFrozenIdentity(t *testing.T) {
	for _, test := range []struct {
		name, code string
		change     func(*testing.T, *Store, context.Context, Run, Attempt, *PreparedAttempt)
	}{
		{"cancelled", "attempt_finished", func(t *testing.T, s *Store, actor context.Context, r Run, a Attempt, in *PreparedAttempt) {
			if _, err := s.CancelRemoteRun(actor, r.ID, "Cancel before activation."); err != nil {
				t.Fatal(err)
			}
		}},
		{"lease_expired", "attempt_expired", func(t *testing.T, s *Store, _ context.Context, _ Run, a Attempt, _ *PreparedAttempt) {
			if _, err := s.db.Exec("UPDATE run_attempts SET lease_expires_at=? WHERE id=?", remoteStamp(time.Now().Add(-time.Second)), a.ID); err != nil {
				t.Fatal(err)
			}
		}},
		{"deadline_expired", "attempt_expired", func(t *testing.T, s *Store, _ context.Context, _ Run, a Attempt, _ *PreparedAttempt) {
			if _, err := s.db.Exec("UPDATE run_attempts SET deadline=? WHERE id=?", remoteStamp(time.Now().Add(-time.Second)), a.ID); err != nil {
				t.Fatal(err)
			}
		}},
		{"context_changed", "stale_attempt", func(_ *testing.T, _ *Store, _ context.Context, _ Run, _ Attempt, in *PreparedAttempt) {
			in.Conversation.Generation++
		}},
		{"writer_baseline_changed", "resource_conflict", func(t *testing.T, s *Store, _ context.Context, r Run, _ Attempt, _ *PreparedAttempt) {
			if _, err := s.db.Exec("UPDATE resources SET current_commit=? WHERE id=?", strings.Repeat("d", 40), r.ResourceID); err != nil {
				t.Fatal(err)
			}
		}},
		{"writer_fence_changed", "stale_lease", func(t *testing.T, s *Store, _ context.Context, r Run, _ Attempt, _ *PreparedAttempt) {
			if _, err := s.db.Exec("UPDATE resource_writer_leases SET fence=fence+1 WHERE resource_id=?", r.ResourceID); err != nil {
				t.Fatal(err)
			}
		}},
		{"permission_revoked", "attempt_finished", func(t *testing.T, s *Store, actor context.Context, r Run, _ Attempt, _ *PreparedAttempt) {
			agent, err := s.GetAgent(ctx, r.AgentID)
			if err != nil {
				t.Fatal(err)
			}
			agent.Enabled = false
			if _, err = s.SaveAgent(actor, agent); err != nil {
				t.Fatal(err)
			}
		}},
		{"runner_runtime_changed", "stale_attempt", func(t *testing.T, s *Store, _ context.Context, _ Run, a Attempt, _ *PreparedAttempt) {
			runner, err := s.GetRunner(ctx, a.RunnerID)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.ConnectRunner(ctx, runner.ID, RunnerConnection{StorageID: runner.StorageID, Runtime: runner.Runtime, Slots: runner.Slots, Capabilities: json.RawMessage(`{"ready":true,"executor":"fixture","fingerprint":"new-runtime"}`)}); err != nil {
				t.Fatal(err)
			}
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, actor, r, a, in := preparationFixture(t, "write")
			test.change(t, s, actor, r, a, &in)
			_, err := s.ActivateAttempt(ctx, a.ID, in)
			requireCode(t, err, test.code)
			retained, _ := s.GetAttempt(ctx, a.ID)
			if len(retained.Command) > 0 || retained.InputDigest != "" {
				t.Fatal("late result installed execution authority", retained)
			}
			assertScalar(t, s, "SELECT count(*) FROM runner_commands WHERE kind='run.start'", 0)
		})
	}
}

func TestReadonlyAndTaskPreparationRetainPinnedBaselines(t *testing.T) {
	s, _, run, a, in := preparationFixture(t, "read")
	if _, err := s.db.Exec("UPDATE resources SET current_commit=? WHERE id=?", strings.Repeat("d", 40), run.ResourceID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ActivateAttempt(ctx, a.ID, in); err != nil {
		t.Fatal("read-only preparation incorrectly required the latest shared version", err)
	}
	for _, changeFence := range []bool{false, true} {
		t.Run(map[bool]string{false: "shared_revision", true: "draft_fence"}[changeFence], func(t *testing.T) {
			s, actor, _, room, writer, _ := taskFixture(t)
			task := startTask(t, s, actor, room, writer)
			queued, err := s.GetRun(ctx, task.ActiveRunID)
			if err != nil {
				t.Fatal(err)
			}
			run, a, err := s.ClaimRemoteRun(ctx, task.ActiveRunID, queued.RunnerID, time.Minute)
			if err != nil {
				t.Fatal(err)
			}
			command, _ := json.Marshal(map[string]string{"id": a.CommandID, "operationId": a.ID, "kind": "run.start"})
			in := PreparedAttempt{Conversation: *a.Preparation, InputDigest: strings.Repeat("a", 64), InputPath: filepath.Join(t.TempDir(), "project.tar"), Command: command, ModelTokenHash: strings.Repeat("b", 64)}
			if _, err = s.db.Exec("UPDATE resources SET current_commit=? WHERE id=?", strings.Repeat("d", 40), run.ResourceID); err != nil {
				t.Fatal(err)
			}
			if changeFence {
				if _, err = s.db.Exec("UPDATE collaboration_tasks SET snapshot=json_set(snapshot,'$.draftFence',json_extract(snapshot,'$.draftFence')+1) WHERE id=?", task.ID); err != nil {
					t.Fatal(err)
				}
			}
			_, err = s.ActivateAttempt(ctx, a.ID, in)
			if changeFence {
				requireCode(t, err, "stale_attempt")
			} else if err != nil {
				t.Fatal("task activation prematurely checked shared publication CAS", err)
			}
		})
	}
}

func TestLegacyUnsentPreparationRetainsPreviouslySavedSession(t *testing.T) {
	s, _, run, a, in := preparationFixture(t, "write")
	run.SessionID, run.SessionGeneration, run.SessionMode = in.Conversation.SessionID, in.Conversation.Generation, "new"
	if err := saveRun(ctx, s.queries(), run); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE run_attempts SET preparation_context='' WHERE id=?", a.ID); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		frozen, err := s.FreezeAttemptPreparation(ctx, a.ID)
		if err != nil || frozen.Preparation == nil || *frozen.Preparation != in.Conversation {
			t.Fatal("legacy rebuild changed session identity", frozen, err)
		}
	}
}

func TestFrozenPreparationSurvivesStoreReopen(t *testing.T) {
	s, _, _, a, in := preparationFixture(t, "write")
	var filename string
	if err := s.db.QueryRow("SELECT file FROM pragma_database_list WHERE name='main'").Scan(&filename); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	frozen, err := reopened.FreezeAttemptPreparation(ctx, a.ID)
	if err != nil || frozen.Preparation == nil || *frozen.Preparation != in.Conversation || frozen.CommandID != a.CommandID {
		t.Fatal("restart allocated another session", frozen, err)
	}
}
