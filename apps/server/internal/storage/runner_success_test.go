package storage

import (
	"encoding/json"
	"strings"
	"testing"
)

func reportNativeRunner(t *testing.T, s *Store, id string) {
	t.Helper()
	runner, err := s.GetRunner(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.ConnectRunner(ctx, id, RunnerConnection{StorageID: runner.StorageID, Runtime: "native", Slots: 1, Capabilities: json.RawMessage(`{"executor":"native","ready":true}`)})
	if err != nil {
		t.Fatal(err)
	}
}

func TestFirstRunnerSuccessRequiresNativeCommittedCompletion(t *testing.T) {
	for _, test := range []struct {
		name, runtime, status string
		cleanup               bool
		want                  bool
	}{
		{"native reader", "native", "succeeded", true, true},
		{"fixture reader", "fixture", "succeeded", true, false},
		{"failed", "native", "failed", true, false},
		{"interrupted", "native", "interrupted", true, false},
		{"cleanup unconfirmed", "native", "succeeded", false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			s := testStore(t)
			admin, _ := teamUsers(t, s)
			runner, _ := enrollTestRunner(t, s, admin, "alpha")
			room, reader := remoteTestRoom(t, s, admin, runner)
			reader.Access = "read"
			reader, err := s.SaveAgent(admin, reader)
			if err != nil {
				t.Fatal(err)
			}
			if test.runtime == "native" {
				reportNativeRunner(t, s, runner.ID)
			}
			run, attempt := prepareTestAttempt(t, s, sendRemoteTestRun(t, s, admin, room, reader, "read"))
			if _, err := s.BeginFinalizing(ctx, attempt.ID, runner.ID); err != nil {
				t.Fatal(err)
			}
			if id, stamp, err := s.FirstRunnerSuccess(ctx, runner.ID); err != nil || id != "" || stamp != "" {
				t.Fatal("uploading execution qualified without a terminal receipt", id, stamp, err)
			}
			run.Status = test.status
			// Completion data must not rewrite Server-recorded execution provenance.
			run.Executor = "untrusted-result-runtime"
			finished, err := s.FinishRemoteRun(ctx, attempt.ID, runner.ID, "complete", strings.Repeat("f", 64), run, "Read the supplied files.", false, test.cleanup)
			if err != nil {
				t.Fatal(err)
			}
			id, stamp, err := s.FirstRunnerSuccess(ctx, runner.ID)
			if err != nil || (id == run.ID && stamp != "") != test.want || (!test.want && (id != "" || stamp != "")) {
				t.Fatal("wrong first execution milestone", id, stamp, err)
			}
			if finished.Published {
				t.Fatal("read-only completion published files")
			}
			if finished.Executor != test.runtime {
				t.Fatal("completion replaced frozen executor", finished.Executor)
			}
		})
	}
}

func TestFirstRunnerSuccessNeverInfersHistoricalExecutorFromCurrentRunner(t *testing.T) {
	s := testStore(t)
	admin, _ := teamUsers(t, s)
	runner, _ := enrollTestRunner(t, s, admin, "alpha")
	room, reader := remoteTestRoom(t, s, admin, runner)
	reader.Access = "read"
	reader, err := s.SaveAgent(admin, reader)
	if err != nil {
		t.Fatal(err)
	}
	run, attempt := prepareTestAttempt(t, s, sendRemoteTestRun(t, s, admin, room, reader, "fixture-success"))
	// Reconnecting the same registration as native cannot relabel this fixture Run.
	reportNativeRunner(t, s, runner.ID)
	run.Executor = "native"
	run.Status = "succeeded"
	finished, err := s.FinishRemoteRun(ctx, attempt.ID, runner.ID, "complete", strings.Repeat("f", 64), run, "Fixture completed.", false, true)
	if err != nil || finished.Executor != "fixture" {
		t.Fatal(finished.Executor, err)
	}
	if id, _, err := s.FirstRunnerSuccess(ctx, runner.ID); err != nil || id != "" {
		t.Fatal("current native Runner relabeled earlier fixture work", id, err)
	}
	if id, _, err := s.FirstRunnerUnrecordedSuccess(ctx, runner.ID); err != nil || id != "" {
		t.Fatal("known fixture execution became unrecorded success", id, err)
	}
	// Model an old database receipt without runtime provenance. It is readable
	// history, never native validation; no production backfill is permitted.
	if _, err = s.db.Exec(`UPDATE runs SET snapshot=json_remove(snapshot,'$.executor') WHERE id=?`, run.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE run_attempts SET result=json_remove(result,'$.cleanupConfirmed') WHERE id=?`, attempt.ID); err != nil {
		t.Fatal(err)
	}
	if id, _, err := s.FirstRunnerSuccess(ctx, runner.ID); err != nil || id != "" {
		t.Fatal("unknown historical executor became native validation", id, err)
	}
	if id, stamp, err := s.FirstRunnerUnrecordedSuccess(ctx, runner.ID); err != nil || id != run.ID || stamp == "" {
		t.Fatal("historical success was hidden", id, stamp, err)
	}
}

func TestFirstRunnerSuccessIncludesDurableCollaborationCheckpoint(t *testing.T) {
	s, admin, _, room, writer, reviewer := taskFixture(t)
	runner, err := s.ResolveAgentRunner(admin, room.ID, writer.ID)
	if err != nil {
		t.Fatal(err)
	}
	reportNativeRunner(t, s, runner.ID)
	task := startTask(t, s, admin, room, writer, reviewer)
	run, attempt := finishTaskTestRound(t, s, task, strings.Repeat("b", 40), CoordinationIntent{CallID: "handoff", Kind: "handoff", TargetAgentID: reviewer.ID, Instruction: "Review the draft."})
	if run.Published {
		t.Fatal("checkpoint was published prematurely")
	}
	id, stamp, err := s.FirstRunnerSuccess(ctx, runner.ID)
	if err != nil || id != run.ID || stamp == "" {
		t.Fatal("successful draft round did not qualify", id, stamp, err)
	}
	// A run snapshot alone or a rejection cannot substitute for the final receipt.
	if _, err = s.db.Exec(`UPDATE run_attempts SET result='{"disposition":"rejected","status":"succeeded"}' WHERE id=?`, attempt.ID); err != nil {
		t.Fatal(err)
	}
	if id, _, err := s.FirstRunnerSuccess(ctx, runner.ID); err != nil || id != "" {
		t.Fatal("rejected receipt qualified", id, err)
	}
}
