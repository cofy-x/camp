package storage

import (
	"strings"
	"testing"
	"time"
)

func TestUncertainCleanupAndRunnerSuspensionCommitTogether(t *testing.T) {
	for _, operation := range []string{"run", "source"} {
		t.Run(operation, func(t *testing.T) {
			s := testStore(t)
			owner, _ := teamUsers(t, s)
			runner, _ := enrollTestRunner(t, s, owner, "alpha")
			room, agent := remoteTestRoom(t, s, owner, runner)
			var finish func() error
			var terminal func() bool
			if operation == "run" {
				run, attempt := prepareTestAttempt(t, s, sendRemoteTestRun(t, s, owner, room, agent, "active"))
				run.Status = "succeeded"
				run.ResultCommit = strings.Repeat("b", 40)
				finish = func() error {
					_, err := s.FinishRemoteRun(ctx, attempt.ID, runner.ID, "completion", strings.Repeat("d", 64), run, "Done", true, false)
					return err
				}
				terminal = func() bool {
					current, err := s.GetRun(ctx, run.ID)
					return err == nil && current.Status == "interrupted" && !current.Published
				}
			} else {
				item, err := s.CreateSourceImport(owner, SourceImportInput{RequestID: "active", RunnerID: runner.ID, SourceID: "project", ExpectedSourceRevision: "source-v1", RoomName: "Uncertain capture"})
				if err != nil {
					t.Fatal(err)
				}
				if _, err = s.ClaimSourceImport(ctx, item.ID, runner.ID, time.Minute); err != nil {
					t.Fatal(err)
				}
				finish = func() error {
					_, err := s.CompleteSourceImportFailure(ctx, item.ID, runner.ID, "completion", strings.Repeat("d", 64), "failed", "Capture failed.", false)
					return err
				}
				terminal = func() bool {
					current, err := s.GetSourceImport(ctx, item.ID)
					return err == nil && current.Status == "interrupted" && current.RoomID == ""
				}
			}
			queued := sendRemoteTestRun(t, s, owner, room, agent, "queued")
			queuedSource, err := s.CreateSourceImport(owner, SourceImportInput{RequestID: "queued", RunnerID: runner.ID, SourceID: "project", ExpectedSourceRevision: "source-v1", RoomName: "Queued capture"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err = s.db.Exec(`CREATE TRIGGER reject_runner_suspension BEFORE UPDATE OF enabled ON runners WHEN NEW.enabled=0 BEGIN SELECT RAISE(ABORT,'injected suspension failure'); END;`); err != nil {
				t.Fatal(err)
			}
			if err = finish(); err == nil || terminal() {
				t.Fatal("completion committed without required runner suspension", err)
			}
			current, err := s.GetRunner(ctx, runner.ID)
			if err != nil || !current.Enabled {
				t.Fatal("failed transaction partially suspended the runner", current, err)
			}
			if _, err = s.db.Exec(`DROP TRIGGER reject_runner_suspension`); err != nil {
				t.Fatal(err)
			}
			if err = finish(); err != nil || !terminal() {
				t.Fatal("cleanup failure was not recorded", err)
			}
			current, err = s.GetRunner(ctx, runner.ID)
			if err != nil || current.Enabled {
				t.Fatal("uncertain cleanup left runner available", current, err)
			}
			if err = finish(); err != nil {
				t.Fatal("lost completion response could not replay after suspension", err)
			}
			queued, err = s.GetRun(ctx, queued.ID)
			if err != nil || queued.Status != "cancelled" {
				t.Fatal("queued work survived runner suspension", queued.Status, err)
			}
			queuedSource, err = s.GetSourceImport(ctx, queuedSource.ID)
			if err != nil || queuedSource.Status != "cancelled" {
				t.Fatal("queued source survived runner suspension", queuedSource.Status, err)
			}
			resource, err := s.GetResource(ctx, room.ResourceID)
			if err != nil || resource.CurrentCommit != strings.Repeat("a", 40) {
				t.Fatal("uncertain result replaced published files", resource.CurrentCommit, err)
			}
		})
	}
}
