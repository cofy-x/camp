package storage

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func enrollTestRunner(t *testing.T, s *Store, actor context.Context, name string) (Runner, JoinRunnerInput) {
	t.Helper()
	_, token, err := s.CreateRunnerEnrollment(actor, name, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	join := JoinRunnerInput{Token: token, RegistrationID: "register-" + name, Credential: strings.Repeat(name, 40), StorageID: "store-" + name}
	runner, err := s.JoinRunner(ctx, join)
	if err != nil {
		t.Fatal(err)
	}
	runner, err = s.ConnectRunner(ctx, runner.ID, RunnerConnection{StorageID: join.StorageID, Runtime: "fixture", Slots: 1, Capabilities: json.RawMessage(`{"executor":"fixture","ready":true,"fingerprint":"v1"}`), Sources: []RunnerSource{{ID: "project", Alias: "Project", Revision: "source-v1", Available: true}}})
	if err != nil {
		t.Fatal(err)
	}
	shared := true
	runner, err = s.UpdateRunner(actor, runner.ID, RunnerUpdate{TeamShared: &shared})
	if err != nil {
		t.Fatal(err)
	}
	return runner, join
}
func remoteTestRoom(t *testing.T, s *Store, actor context.Context, runner Runner) (Room, Agent) {
	t.Helper()
	room, err := s.SeedRoom(actor, Resource{ID: "resource", Name: "Shared files", CurrentCommit: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	room, err = s.SetRoomRunner(actor, room.ID, runner.ID, room.RunnerRevision)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := s.GetAgent(ctx, room.DefaultAgentID)
	if err != nil {
		t.Fatal(err)
	}
	return room, agent
}
func sendRemoteTestRun(t *testing.T, s *Store, actor context.Context, room Room, agent Agent, request string) Run {
	t.Helper()
	budget := roomBudget
	budget.RequireRunner = true
	result, err := s.SendMessage(actor, room.ID, SendMessageInput{RequestID: request, Content: "Prepare a document.", RecipientAgentIDs: []string{agent.ID}, RunnerRevision: &room.RunnerRevision}, budget)
	if err != nil {
		t.Fatal(err)
	}
	return result.Runs[0]
}
func prepareTestAttempt(t *testing.T, s *Store, run Run) (Run, Attempt) {
	t.Helper()
	run, attempt, err := s.ClaimRemoteRun(ctx, run.ID, run.RunnerID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	command, _ := json.Marshal(map[string]string{"id": attempt.CommandID, "operationId": attempt.ID, "kind": "run.start"})
	if err = s.SaveAttemptCommand(ctx, attempt.ID, command, strings.Repeat("f", 64)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcceptAttempt(ctx, attempt.ID, run.RunnerID); err != nil {
		t.Fatal(err)
	}
	return run, attempt
}

func TestRunnerEnrollmentReplayRevocationAndConnectionGeneration(t *testing.T) {
	s := testStore(t)
	owner, member := teamUsers(t, s)
	_, _, err := s.CreateRunnerEnrollment(member, "Personal machine", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	runner, join := enrollTestRunner(t, s, owner, "alpha")
	replay, err := s.JoinRunner(ctx, join)
	if err != nil || replay.ID != runner.ID {
		t.Fatal(replay, err)
	}
	changed := join
	changed.Credential = strings.Repeat("other", 40)
	_, err = s.JoinRunner(ctx, changed)
	requireCode(t, err, "request_conflict")
	if authenticated, err := s.AuthenticateRunner(ctx, join.Credential); err != nil || authenticated.ID != runner.ID {
		t.Fatal(authenticated, err)
	}
	oldGeneration := runner.ConnectionGeneration
	runner, err = s.ConnectRunner(ctx, runner.ID, RunnerConnection{StorageID: join.StorageID, Runtime: "fixture", Slots: 1, Capabilities: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatal(err)
	}
	requireCode(t, s.TouchRunner(ctx, runner.ID, oldGeneration), "stale_connection")
	if err = s.TouchRunner(ctx, runner.ID, runner.ConnectionGeneration); err != nil {
		t.Fatal(err)
	}
	_, err = s.ConnectRunner(ctx, runner.ID, RunnerConnection{StorageID: "lost-storage", Runtime: "fixture", Slots: 1, Capabilities: json.RawMessage(`{}`)})
	requireCode(t, err, "runner_storage_changed")
	if _, err = s.UpdateRunner(owner, runner.ID, RunnerUpdate{Revoke: true}); err != nil {
		t.Fatal(err)
	}
	_, err = s.AuthenticateRunner(ctx, join.Credential)
	requireCode(t, err, "unauthenticated")
	_, err = s.JoinRunner(ctx, join)
	requireCode(t, err, "runner_unavailable")
	enabled := true
	updated, err := s.UpdateRunner(owner, runner.ID, RunnerUpdate{Enabled: &enabled})
	if err != nil || updated.Enabled || !updated.Revoked {
		t.Fatal("revocation was reversible", updated, err)
	}
	encoded, _ := json.Marshal(updated)
	if strings.Contains(string(encoded), join.Credential) || strings.Contains(string(encoded), remoteHash([]byte(join.Credential))) {
		t.Fatal("runner response disclosed a credential")
	}
}

func TestRunnerPatchPreservesIndependentChanges(t *testing.T) {
	s := testStore(t)
	owner, _ := teamUsers(t, s)
	runner, _ := enrollTestRunner(t, s, owner, "alpha")
	enabled := false
	if _, err := s.UpdateRunner(owner, runner.ID, RunnerUpdate{Enabled: &enabled}); err != nil {
		t.Fatal(err)
	}
	name := "Renamed while disabled"
	updated, err := s.UpdateRunner(owner, runner.ID, RunnerUpdate{Name: &name})
	if err != nil || updated.Enabled || updated.Name != name {
		t.Fatal("rename changed execution authority", updated, err)
	}
	enabled = true
	updated, err = s.UpdateRunner(owner, runner.ID, RunnerUpdate{Enabled: &enabled})
	if err != nil || !updated.Enabled || updated.Name != name {
		t.Fatal("enable reverted a rename", updated, err)
	}
	invalid := " "
	_, err = s.UpdateRunner(owner, runner.ID, RunnerUpdate{Name: &invalid})
	requireCode(t, err, "invalid_runner")
	updated, err = s.GetRunner(owner, runner.ID)
	if err != nil || !updated.Enabled || updated.Name != name {
		t.Fatal("invalid patch changed the runner", updated, err)
	}
}

func TestRemoteAdmissionFreezesRouteAndReplaysBeforeRevision(t *testing.T) {
	s := testStore(t)
	owner, member := teamUsers(t, s)
	a, _ := enrollTestRunner(t, s, owner, "alpha")
	b, _ := enrollTestRunner(t, s, owner, "bravo")
	room, agent := remoteTestRoom(t, s, owner, a)
	_, err := s.SetRoomRunner(member, room.ID, b.ID, room.RunnerRevision)
	requireCode(t, err, "forbidden")
	budget := roomBudget
	budget.RequireRunner = true
	input := SendMessageInput{RequestID: "original", Content: "Prepare a document.", RecipientAgentIDs: []string{agent.ID}, RunnerRevision: &room.RunnerRevision}
	first, err := s.SendMessage(owner, room.ID, input, budget)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := s.SetRoomRunner(owner, room.ID, b.ID, room.RunnerRevision)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := s.SendMessage(owner, room.ID, input, budget)
	if err != nil || !replay.Replayed || replay.Runs[0].RunnerID != a.ID || replay.Runs[0].ID != first.Runs[0].ID {
		t.Fatal(replay, err)
	}
	input.RequestID = "new-stale"
	_, err = s.SendMessage(owner, room.ID, input, budget)
	requireCode(t, err, "runner_revision_conflict")
	input.RunnerRevision = &changed.RunnerRevision
	next, err := s.SendMessage(owner, room.ID, input, budget)
	if err != nil || next.Runs[0].RunnerID != b.ID {
		t.Fatal(next, err)
	}
	_, _, err = s.ClaimRemoteRun(ctx, first.Runs[0].ID, b.ID, time.Minute)
	requireCode(t, err, "runner_mismatch")
}

func TestRemotePublicationReceiptAndEventsAreAtomicAndIdempotent(t *testing.T) {
	s := testStore(t)
	owner, _ := teamUsers(t, s)
	runner, _ := enrollTestRunner(t, s, owner, "alpha")
	room, agent := remoteTestRoom(t, s, owner, runner)
	run, attempt := prepareTestAttempt(t, s, sendRemoteTestRun(t, s, owner, room, agent, "run"))
	if err := s.SaveAttemptCommand(ctx, attempt.ID, json.RawMessage(`{"id":"wrong"}`), strings.Repeat("f", 64)); err == nil {
		t.Fatal("command changed after acceptance")
	}
	for i := 0; i < 2; i++ {
		replayed, err := s.RecordAttemptEvent(ctx, runner.ID, attempt.ID, 1, "assistant", "One answer.", strings.Repeat("1", 64))
		if err != nil || replayed != (i == 1) {
			t.Fatal(replayed, err)
		}
	}
	_, err := s.RecordAttemptEvent(ctx, runner.ID, attempt.ID, 1, "assistant", "Different answer.", strings.Repeat("1", 64))
	requireCode(t, err, "request_conflict")
	_, err = s.RecordAttemptEvent(ctx, runner.ID, attempt.ID, 3, "assistant", "Gap", strings.Repeat("3", 64))
	requireCode(t, err, "event_sequence_gap")
	run, err = s.GetRun(ctx, run.ID)
	if err != nil || run.Output != "One answer." {
		t.Fatal(run, err)
	}
	_, err = s.BeginFinalizing(ctx, attempt.ID, runner.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.SaveAttemptArtifact(ctx, attempt.ID, runner.ID, strings.Repeat("d", 64), "/private/result.tar"); err != nil {
		t.Fatal(err)
	}
	run.Status = "succeeded"
	run.ResultCommit = strings.Repeat("b", 40)
	if _, err = s.db.Exec(`CREATE TRIGGER reject_remote_completion BEFORE INSERT ON messages WHEN NEW.author_kind='agent' BEGIN SELECT RAISE(ABORT,'injected final failure'); END;`); err != nil {
		t.Fatal(err)
	}
	_, err = s.FinishRemoteRun(ctx, attempt.ID, runner.ID, "completion", strings.Repeat("9", 64), run, "Done", false, true)
	if err == nil {
		t.Fatal("accepted failed publication transaction")
	}
	resource, _ := s.GetResource(ctx, room.ResourceID)
	if resource.CurrentCommit != run.BaselineCommit {
		t.Fatal("published files without terminal receipt")
	}
	after, _ := s.GetAttempt(ctx, attempt.ID)
	if after.Phase == "finished" || after.CompletionID != "" {
		t.Fatal("saved receipt without terminal transaction", after)
	}
	assertScalar(t, s, "SELECT count(*) FROM resource_publications", 0)
	if _, err = s.db.Exec(`DROP TRIGGER reject_remote_completion`); err != nil {
		t.Fatal(err)
	}
	saved, err := s.FinishRemoteRun(ctx, attempt.ID, runner.ID, "completion", strings.Repeat("9", 64), run, "Done", false, true)
	if err != nil || !saved.Published || saved.Status != "succeeded" {
		t.Fatal(saved, err)
	}
	if _, err = s.db.Exec(`UPDATE run_attempts SET lease_expires_at='2000-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	replay, err := s.FinishRemoteRun(ctx, attempt.ID, runner.ID, "completion", strings.Repeat("9", 64), run, "Lost response", false, true)
	if err != nil || replay.ID != saved.ID || !replay.Published {
		t.Fatal(replay, err)
	}
	assertScalar(t, s, "SELECT count(*) FROM resource_publications", 1)
	assertScalar(t, s, "SELECT count(*) FROM messages WHERE author_kind='agent'", 1)
	assertScalar(t, s, "SELECT count(*) FROM inbox", 1)
	_, err = s.FinishRemoteRun(ctx, attempt.ID, runner.ID, "changed", strings.Repeat("8", 64), run, "", false, true)
	requireCode(t, err, "request_conflict")
	lease, err := s.AcquireResourceLease(ctx, room.ResourceID, "human", time.Now().Add(time.Minute))
	if err != nil || lease.Fence <= attempt.WriterFence {
		t.Fatal(lease, err)
	}
}

func TestRemoteCancellationWinsPublicationRace(t *testing.T) {
	s := testStore(t)
	owner, _ := teamUsers(t, s)
	runner, _ := enrollTestRunner(t, s, owner, "alpha")
	room, agent := remoteTestRoom(t, s, owner, runner)
	run, attempt := prepareTestAttempt(t, s, sendRemoteTestRun(t, s, owner, room, agent, "run"))
	if _, err := s.CancelRemoteRun(owner, run.ID, "Stopped by owner."); err != nil {
		t.Fatal(err)
	}
	_, err := s.ValidateAttempt(ctx, attempt.ID, runner.ID, false)
	requireCode(t, err, "run_cancelled")
	run.Status = "succeeded"
	run.ResultCommit = strings.Repeat("b", 40)
	finished, err := s.FinishRemoteRun(ctx, attempt.ID, runner.ID, "completion", strings.Repeat("9", 64), run, "Late answer", false, true)
	if err != nil || finished.Status != "cancelled" || finished.Published {
		t.Fatal(finished, err)
	}
	resource, _ := s.GetResource(ctx, room.ResourceID)
	if resource.CurrentCommit != run.BaselineCommit {
		t.Fatal("cancelled result replaced room files")
	}
	commands, err := s.ListRunnerCommands(ctx, runner.ID)
	if err != nil || len(commands) != 0 {
		t.Fatal(commands, err)
	}
}

func TestDurableWriterFenceSurvivesRestartAndRejectsStaleHumanSave(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "restart.sqlite")
	s, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	owner, _ := teamUsers(t, s)
	runner, _ := enrollTestRunner(t, s, owner, "alpha")
	room, agent := remoteTestRoom(t, s, owner, runner)
	run, attempt := prepareTestAttempt(t, s, sendRemoteTestRun(t, s, owner, room, agent, "run"))
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.AcquireResourceLease(ctx, room.ResourceID, "human", time.Now().Add(time.Minute))
	requireCode(t, err, "resource_busy")
	_, err = s.Publish(owner, PublishInput{ResourceID: room.ResourceID, ExpectedCommit: run.BaselineCommit, Commit: strings.Repeat("c", 40), Kind: "edit", Path: "doc.md", RequestID: "manual", RequestHash: strings.Repeat("a", 64)})
	requireCode(t, err, "resource_busy")
	if _, err = s.db.Exec(`UPDATE resource_writer_leases SET expires_at='2000-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	lease, err := s.AcquireResourceLease(ctx, room.ResourceID, "human", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if lease.Fence <= attempt.WriterFence {
		t.Fatal("fence counter reset across restart")
	}
	run.Status = "succeeded"
	run.ResultCommit = strings.Repeat("b", 40)
	_, err = s.FinishRemoteRun(ctx, attempt.ID, runner.ID, "completion", strings.Repeat("9", 64), run, "", false, true)
	requireCode(t, err, "stale_lease")
	if _, err = s.Publish(WithResourceLease(owner, lease), PublishInput{ResourceID: room.ResourceID, ExpectedCommit: run.BaselineCommit, Commit: strings.Repeat("c", 40), Kind: "edit", Path: "doc.md", RequestID: "manual", RequestHash: strings.Repeat("a", 64)}); err != nil {
		t.Fatal(err)
	}
	if err = s.ReleaseResourceLease(ctx, lease); err != nil {
		t.Fatal(err)
	}
	next, err := s.AcquireResourceLease(ctx, room.ResourceID, "next-human", time.Now().Add(time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Publish(WithResourceLease(owner, lease), PublishInput{ResourceID: room.ResourceID, ExpectedCommit: strings.Repeat("c", 40), Commit: strings.Repeat("d", 40), Kind: "edit", Path: "doc.md", RequestID: "stale-manual", RequestHash: strings.Repeat("b", 64)})
	requireCode(t, err, "stale_lease")
	if next.Fence <= lease.Fence {
		t.Fatal("writer fence did not increase")
	}
}

func TestSourceImportSnapshotReceiptAndCancellation(t *testing.T) {
	s := testStore(t)
	owner, member := teamUsers(t, s)
	assistant, err := s.SaveAgent(ctx, Agent{Name: "Assistant", Handle: "assistant", Access: "write", Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	runner, _ := enrollTestRunner(t, s, owner, "alpha")
	in := SourceImportInput{RequestID: "import", RunnerID: runner.ID, SourceID: "project", ExpectedSourceRevision: "source-v1", RoomName: "Imported project"}
	_, err = s.CreateSourceImport(member, in)
	requireCode(t, err, "forbidden")
	imported, err := s.CreateSourceImport(owner, in)
	if err != nil {
		t.Fatal(err)
	}
	replayed, err := s.CreateSourceImport(owner, in)
	if err != nil || imported.ID != replayed.ID || imported.ResourceID != replayed.ResourceID {
		t.Fatal(replayed, err)
	}
	imported, err = s.ClaimSourceImport(ctx, imported.ID, runner.ID, 5*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	command, _ := json.Marshal(map[string]string{"id": imported.CommandID, "operationId": imported.ID, "kind": "source.capture"})
	if err = s.SaveSourceImportCommand(ctx, imported.ID, command); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		replayed, err := s.RecordSourceImportEvent(ctx, runner.ID, imported.ID, 1, "source", "Captured", strings.Repeat("2", 64))
		if err != nil || replayed != (i == 1) {
			t.Fatal(replayed, err)
		}
	}
	if err = s.SaveSourceImportArtifact(ctx, imported.ID, runner.ID, strings.Repeat("a", 64), "/private/source.tar"); err != nil {
		t.Fatal(err)
	}
	resource := Resource{ID: imported.ResourceID, CurrentCommit: strings.Repeat("b", 40)}
	finished, err := s.FinishSourceImport(ctx, imported.ID, runner.ID, "completion", strings.Repeat("9", 64), resource)
	if err != nil || finished.Status != "succeeded" || finished.Commit != resource.CurrentCommit || finished.SourceAlias != "Project" {
		t.Fatal(finished, err)
	}
	if _, err = s.db.Exec(`UPDATE source_imports SET expires_at='2000-01-01T00:00:00Z'`); err != nil {
		t.Fatal(err)
	}
	again, err := s.FinishSourceImport(ctx, imported.ID, runner.ID, "completion", strings.Repeat("9", 64), resource)
	if err != nil || again.RoomID != finished.RoomID {
		t.Fatal(again, err)
	}
	assertScalar(t, s, "SELECT count(*) FROM resources", 1)
	assertScalar(t, s, "SELECT count(*) FROM rooms", 1)
	room, err := s.GetRoom(ctx, finished.RoomID)
	if err != nil || room.RunnerID != runner.ID || room.RunnerRevision != 1 || room.DefaultAgentID != assistant.ID {
		t.Fatal(room, err)
	}
	members, err := s.ListMembers(ctx, room.ID)
	if err != nil || len(members) != 1 || members[0].ID != assistant.ID {
		t.Fatal("imported room did not join its existing assistant", members, err)
	}
	library, err := s.LibraryResources(ctx)
	if err != nil || library[0].Source == nil || library[0].Source.Commit != resource.CurrentCommit {
		t.Fatal(library, err)
	}
	in.RequestID = "cancelled"
	in.RoomName = "Cancelled import"
	cancelled, err := s.CreateSourceImport(owner, in)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimSourceImport(ctx, cancelled.ID, runner.ID, time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CancelSourceImport(owner, cancelled.ID); err != nil {
		t.Fatal(err)
	}
	_, err = s.FinishSourceImport(ctx, cancelled.ID, runner.ID, "late", strings.Repeat("8", 64), Resource{ID: cancelled.ResourceID, CurrentCommit: strings.Repeat("c", 40)})
	requireCode(t, err, "import_expired")
	assertScalar(t, s, "SELECT count(*) FROM resources", 1)
}

func TestRemotePermissionRevocationCancelsPersistedAttempt(t *testing.T) {
	for _, revoke := range []string{"agent", "requester", "membership"} {
		t.Run(revoke, func(t *testing.T) {
			s := testStore(t)
			owner, member := teamUsers(t, s)
			runner, _ := enrollTestRunner(t, s, owner, "alpha")
			room, agent := remoteTestRoom(t, s, owner, runner)
			run, attempt := prepareTestAttempt(t, s, sendRemoteTestRun(t, s, member, room, agent, "active"))
			queued := sendRemoteTestRun(t, s, member, room, agent, "queued")
			if err := s.BeginAttemptModel(ctx, attempt.ID, runner.ID); err != nil {
				t.Fatal(err)
			}
			var err error
			switch revoke {
			case "agent":
				agent.Enabled = false
				_, err = s.SaveAgent(owner, agent)
			case "requester":
				user, _ := UserFromContext(member)
				enabled := false
				_, err = s.UpdateUserAccess(owner, user.ID, nil, &enabled)
			case "membership":
				err = s.LeaveRoom(owner, room.ID, agent.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			attempt, err = s.GetAttempt(ctx, attempt.ID)
			if err != nil || !attempt.CancelRequested || !attempt.ModelInFlight {
				t.Fatal("revocation did not preserve model uncertainty and revoke its authority", attempt, err)
			}
			current, err := s.GetRun(ctx, run.ID)
			if err != nil || current.Status != "cancelling" || current.CancellationReason == "" {
				t.Fatal(current, err)
			}
			queued, err = s.GetRun(ctx, queued.ID)
			if err != nil || queued.Status != "cancelled" || queued.AttemptID != "" {
				t.Fatal("revoked queued work remained runnable", queued, err)
			}
			if err = s.EndAttemptModel(ctx, attempt.ID); err != nil {
				t.Fatal(err)
			}
			requireCode(t, s.BeginAttemptModel(ctx, attempt.ID, runner.ID), "run_cancelled")
			commands, err := s.ListRunnerCommands(ctx, runner.ID)
			if err != nil {
				t.Fatal(err)
			}
			cancels := 0
			for _, command := range commands {
				if command.Kind == "run.cancel" && command.OperationID == attempt.ID {
					cancels++
				}
			}
			if cancels != 1 {
				t.Fatal("revocation did not persist exactly one cancel command", commands)
			}
			run.Status = "succeeded"
			run.ResultCommit = strings.Repeat("b", 40)
			finished, err := s.FinishRemoteRun(ctx, attempt.ID, runner.ID, "late-result", strings.Repeat("d", 64), run, "Late answer", false, true)
			if err != nil || finished.Status != "cancelled" || finished.Published {
				t.Fatal("revocation rejected cleanup or published a late result", finished, err)
			}
		})
	}
}

func TestRemoteMigrationRetainsHistoricalLocalRuns(t *testing.T) {
	s := unmigratedStore(t)
	sources := migrationSourcesThrough(t, 7)
	if err := s.applyMigrations(ctx, sources); err != nil {
		t.Fatal(err)
	}
	snapshot := `{"id":"run","roomId":"room","agentId":"agent","resourceId":"resource","status":"queued","prompt":"Retain me"}`
	if _, err := s.db.Exec(`INSERT INTO resources(id,name,source_path,current_commit,environment,created_at,updated_at) VALUES('resource','Files','','aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa','{}','then','then'); INSERT INTO rooms(id,name,description,resource_id,created_at,updated_at) VALUES('room','Room','','resource','then','then'); INSERT INTO agents(id,name,handle,icon,instructions,access,enabled,revision,created_at,updated_at) VALUES('agent','Agent','agent','agent','','write',1,1,'then','then'); INSERT INTO messages(id,room_id,seq,author_kind,content,created_at,updated_at) VALUES('message','room',1,'user','Retain me','then','then'); INSERT INTO runs(id,room_id,origin_message_id,agent_id,resource_id,status,snapshot,created_at,updated_at) VALUES('run','room','message','agent','resource','queued',?,'then','then');`, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	run, err := s.GetRun(ctx, "run")
	if err != nil || run.Prompt != "Retain me" || run.RunnerID != "" || run.AttemptID != "" {
		t.Fatal(run, err)
	}
	room, err := s.GetRoom(ctx, "room")
	if err != nil || room.RunnerID != "" || room.RunnerRevision != 0 {
		t.Fatal(room, err)
	}
	assertScalar(t, s, "SELECT count(*) FROM run_attempts", 0)
	assertScalar(t, s, "SELECT count(*) FROM runners", 0)
}

func TestRemoteCancellationBeforeDispatchRequiresNoDaemonCleanup(t *testing.T) {
	s := testStore(t)
	owner, _ := teamUsers(t, s)
	runner, _ := enrollTestRunner(t, s, owner, "alpha")
	room, agent := remoteTestRoom(t, s, owner, runner)
	run := sendRemoteTestRun(t, s, owner, room, agent, "never-dispatched")
	run, attempt, err := s.ClaimRemoteRun(ctx, run.ID, runner.ID, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	finished, err := s.CancelRemoteRun(owner, run.ID, "Cancelled before dispatch.")
	if err != nil || finished.Status != "cancelled" || finished.Published {
		t.Fatal(finished, err)
	}
	attempt, err = s.GetAttempt(ctx, attempt.ID)
	if err != nil || attempt.Phase != "finished" || !attempt.CancelRequested {
		t.Fatal(attempt, err)
	}
	if _, err = s.AcquireResourceLease(ctx, room.ResourceID, "next-writer", time.Now().Add(time.Minute)); err != nil {
		t.Fatal("cancelled preparation retained writer", err)
	}
	command, _ := json.Marshal(map[string]string{"id": attempt.CommandID, "operationId": attempt.ID, "kind": "run.start"})
	requireCode(t, s.SaveAttemptCommand(ctx, attempt.ID, command, strings.Repeat("f", 64)), "attempt_finished")
	item, err := s.CreateSourceImport(owner, SourceImportInput{RequestID: "cancel-source", RunnerID: runner.ID, SourceID: "project", ExpectedSourceRevision: "source-v1", RoomName: "Never captured"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimSourceImport(ctx, item.ID, runner.ID, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	item, err = s.CancelSourceImport(owner, item.ID)
	if err != nil || item.Status != "cancelled" {
		t.Fatal(item, err)
	}
	command, _ = json.Marshal(map[string]string{"id": item.CommandID, "operationId": item.ID, "kind": "source.capture"})
	requireCode(t, s.SaveSourceImportCommand(ctx, item.ID, command), "import_expired")
	commands, err := s.ListRunnerCommands(ctx, runner.ID)
	if err != nil || len(commands) != 0 {
		t.Fatal("undispatched cancellation sent a remote command", commands, err)
	}
}

func TestRemoteCancellationCommittedBeforeFailedResult(t *testing.T) {
	for _, status := range []string{"failed", "interrupted"} {
		t.Run(status, func(t *testing.T) {
			s := testStore(t)
			owner, _ := teamUsers(t, s)
			runner, _ := enrollTestRunner(t, s, owner, "alpha")
			room, agent := remoteTestRoom(t, s, owner, runner)
			run, attempt := prepareTestAttempt(t, s, sendRemoteTestRun(t, s, owner, room, agent, "race"))
			if _, err := s.CancelRemoteRun(owner, run.ID, "Cancel while finalizing."); err != nil {
				t.Fatal(err)
			}
			// The caller obtained its result snapshot before cancellation committed.
			run.Status = status
			expected := "cancelled"
			if status == "interrupted" {
				expected = "interrupted"
			}
			finished, err := s.FinishRemoteRun(ctx, attempt.ID, runner.ID, "completion", strings.Repeat("f", 64), run, "", false, true)
			if err != nil || finished.Status != expected || finished.Published {
				t.Fatal("stale completion overruled cancellation or hid uncertain cleanup", finished, err)
			}
			item, err := s.CreateSourceImport(owner, SourceImportInput{RequestID: "source-race", RunnerID: runner.ID, SourceID: "project", ExpectedSourceRevision: "source-v1", RoomName: "Cancelled source"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.ClaimSourceImport(ctx, item.ID, runner.ID, 5*time.Minute); err != nil {
				t.Fatal(err)
			}
			command, _ := json.Marshal(map[string]string{"id": item.CommandID, "operationId": item.ID, "kind": "source.capture"})
			if err = s.SaveSourceImportCommand(ctx, item.ID, command); err != nil {
				t.Fatal(err)
			}
			if _, err = s.CancelSourceImport(owner, item.ID); err != nil {
				t.Fatal(err)
			}
			item, err = s.FailSourceImport(ctx, item.ID, runner.ID, "completion", strings.Repeat("f", 64), status, "Capture failed.")
			if err != nil || item.Status != expected {
				t.Fatal("source completion overruled committed cancellation", item, err)
			}
		})
	}
}

func TestRemoteRunsAndSourceImportsShareRunnerSlots(t *testing.T) {
	s := testStore(t)
	owner, _ := teamUsers(t, s)
	runner, _ := enrollTestRunner(t, s, owner, "alpha")
	room, agent := remoteTestRoom(t, s, owner, runner)
	run, attempt := prepareTestAttempt(t, s, sendRemoteTestRun(t, s, owner, room, agent, "occupy-slot"))
	item, err := s.CreateSourceImport(owner, SourceImportInput{RequestID: "wait-for-slot", RunnerID: runner.ID, SourceID: "project", ExpectedSourceRevision: "source-v1", RoomName: "Queued source"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ClaimSourceImport(ctx, item.ID, runner.ID, 5*time.Minute)
	requireCode(t, err, "run_busy")
	item, err = s.GetSourceImport(ctx, item.ID)
	if err != nil || item.Status != "queued" || item.ExpiresAt != "" {
		t.Fatal("source deadline started while waiting for a runner slot", item, err)
	}
	run.Status = "failed"
	if _, err = s.FinishRemoteRun(ctx, attempt.ID, runner.ID, "completion", strings.Repeat("f", 64), run, "", false, true); err != nil {
		t.Fatal(err)
	}
	item, err = s.ClaimSourceImport(ctx, item.ID, runner.ID, 5*time.Minute)
	if err != nil || item.Status != "capturing" || item.ExpiresAt == "" {
		t.Fatal(item, err)
	}
	// Claim replay keeps its deadline and does not consume another slot.
	replay, err := s.ClaimSourceImport(ctx, item.ID, runner.ID, time.Minute)
	if err != nil || replay.ExpiresAt != item.ExpiresAt {
		t.Fatal(replay, err)
	}
	next := sendRemoteTestRun(t, s, owner, room, agent, "wait-for-source")
	_, _, err = s.ClaimRemoteRun(ctx, next.ID, runner.ID, time.Minute)
	requireCode(t, err, "run_busy")
	command, _ := json.Marshal(map[string]string{"id": item.CommandID, "operationId": item.ID, "kind": "source.capture"})
	if err = s.SaveSourceImportCommand(ctx, item.ID, command); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CancelSourceImport(owner, item.ID); err != nil {
		t.Fatal(err)
	}
	_, _, err = s.ClaimRemoteRun(ctx, next.ID, runner.ID, time.Minute)
	requireCode(t, err, "run_busy")
	if _, err = s.FailSourceImport(ctx, item.ID, runner.ID, "source-completion", strings.Repeat("a", 64), "cancelled", "Cleanup confirmed."); err != nil {
		t.Fatal(err)
	}
	if _, _, err = s.ClaimRemoteRun(ctx, next.ID, runner.ID, time.Minute); err != nil {
		t.Fatal("confirmed source cleanup did not release its runner slot", err)
	}
}
