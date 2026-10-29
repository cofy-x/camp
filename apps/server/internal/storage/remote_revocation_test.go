package storage

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestSourceRevocationCannotBeUndoneByRestoringAccess(t *testing.T) {
	for _, phase := range []string{"queued", "claimed", "dispatched"} {
		for _, cause := range []string{"user_disabled", "user_demoted", "runner_disabled"} {
			t.Run(phase+"/"+cause, func(t *testing.T) {
				s := testStore(t)
				owner, other := teamUsers(t, s)
				user, _ := UserFromContext(other)
				adminRole, memberRole := "admin", "member"
				user, err := s.UpdateUserAccess(owner, user.ID, &adminRole, nil)
				if err != nil {
					t.Fatal(err)
				}
				actor := WithUser(ctx, user)
				runner, _ := enrollTestRunner(t, s, owner, "alpha")
				item, err := s.CreateSourceImport(actor, SourceImportInput{RequestID: "source", RunnerID: runner.ID, SourceID: "project", ExpectedSourceRevision: "source-v1", RoomName: "Imported"})
				if err != nil {
					t.Fatal(err)
				}
				if phase != "queued" {
					if _, err = s.ClaimSourceImport(ctx, item.ID, runner.ID, time.Minute); err != nil {
						t.Fatal(err)
					}
				}
				if phase == "dispatched" {
					command, _ := json.Marshal(map[string]string{"id": item.CommandID, "operationId": item.ID, "kind": "source.capture"})
					if err = s.SaveSourceImportCommand(ctx, item.ID, command); err != nil {
						t.Fatal(err)
					}
				}
				disabled, enabled := false, true
				switch cause {
				case "user_disabled":
					_, err = s.UpdateUserAccess(owner, user.ID, nil, &disabled)
					if err == nil {
						_, err = s.UpdateUserAccess(owner, user.ID, nil, &enabled)
					}
				case "user_demoted":
					_, err = s.UpdateUserAccess(owner, user.ID, &memberRole, nil)
					if err == nil {
						_, err = s.UpdateUserAccess(owner, user.ID, &adminRole, nil)
					}
				case "runner_disabled":
					_, err = s.UpdateRunner(owner, runner.ID, RunnerUpdate{Enabled: &disabled})
					if err == nil {
						_, err = s.UpdateRunner(owner, runner.ID, RunnerUpdate{Enabled: &enabled})
					}
				}
				if err != nil {
					t.Fatal(err)
				}
				item, err = s.GetSourceImport(ctx, item.ID)
				want := "cancelled"
				if phase == "dispatched" {
					want = "cancelling"
				}
				if err != nil || item.Status != want || item.Error == "" {
					t.Fatalf("revoked import: status=%s want=%s reason=%q err=%v", item.Status, want, item.Error, err)
				}
				_, err = s.FinishSourceImport(ctx, item.ID, runner.ID, "late", strings.Repeat("a", 64), Resource{ID: item.ResourceID, CurrentCommit: strings.Repeat("b", 40)})
				requireCode(t, err, "import_expired")
				if phase == "dispatched" {
					commands, err := s.ListRunnerCommands(ctx, runner.ID)
					if err != nil {
						t.Fatal(err)
					}
					found := false
					for _, command := range commands {
						found = found || command.Kind == "source.cancel"
					}
					if !found {
						t.Fatal("revocation did not persist a stop command")
					}
					finished, err := s.FailSourceImport(ctx, item.ID, runner.ID, "cleanup", strings.Repeat("c", 64), "failed", "Capture ended.")
					if err != nil || finished.Status != "cancelled" {
						t.Fatal(finished, err)
					}
				}
			})
		}
	}
}
