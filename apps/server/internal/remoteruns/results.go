package remoteruns

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cofy-x/camp/apps/server/internal/projectenv"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func (m *Manager) complete(w http.ResponseWriter, r *http.Request, runner storage.Runner) {
	var result p.Result
	if !decodeMachine(w, r, &result, 2<<20) {
		return
	}
	raw, _ := json.Marshal(result)
	hash := digest(raw)
	a, err := m.store.GetAttempt(r.Context(), r.PathValue("id"))
	if errorCode(err) == "not_found" {
		m.completeSource(w, r, runner, result, hash)
		return
	}
	if err != nil {
		m.disposition(w, err)
		return
	}
	if a.RunnerID != runner.ID {
		machineError(w, 403, "Operation belongs to another runner.")
		return
	}
	if a.CompletionID != "" {
		if a.CompletionID != result.CompletionID || a.ResultDigest != hash {
			m.disposition(w, problem("request_conflict", "This execution already has a different completion."))
			return
		}
		sendJSON(w, 200, storedReceipt(a))
		return
	}
	if receipt := storedReceipt(a); receipt != nil {
		sendJSON(w, 200, receipt)
		return
	}
	if _, err = m.store.ValidateAttempt(r.Context(), a.ID, runner.ID, true); err != nil {
		m.disposition(w, err)
		return
	}
	run, err := m.store.GetRun(r.Context(), a.RunID)
	if err != nil {
		m.disposition(w, err)
		return
	}
	if err = validateResult(run, result); err != nil {
		m.rejectResult(w, r, a, err)
		return
	}
	run.Intent = result.Intent
	run.ChecksDeferred = run.TaskID != "" && result.Intent != nil && result.Intent.Kind != "finish"
	run.Status = result.Status
	run.Error = m.redact(result.Error)
	run.ExitCode = result.ExitCode
	run.TestExitCode = result.TestExitCode
	for i := range result.Checks {
		result.Checks[i].Output = m.redact(result.Checks[i].Output)
	}
	run.Checks, _ = json.Marshal(result.Checks)
	run.Files = []storage.RunFile{}
	if !result.CleanupConfirmed {
		run.Status = "interrupted"
		run.Error = "Runner could not confirm execution cleanup. Its execution capability is suspended."
		result.Resumable = false
	}
	if a.CancelRequested && result.CleanupConfirmed {
		run.Status = "cancelled"
		result.Resumable = false
	}
	if result.Archive != nil {
		if a.Phase != "uploading" || a.ArtifactDigest == "" || result.Archive.SHA256 != a.ArtifactDigest {
			m.rejectResult(w, r, a, problem("invalid_artifact", "The final manifest does not match the accepted archive."))
			return
		}
		manifest, e := artifactManifest(a.ArtifactPath)
		if e != nil {
			m.disposition(w, e)
			return
		}
		if manifest.SHA256 != result.Archive.SHA256 || manifest.Bytes != result.Archive.Bytes {
			m.rejectResult(w, r, a, problem("invalid_artifact", "The stored archive failed integrity verification."))
			return
		}
		archive, e := os.Open(a.ArtifactPath)
		if e != nil {
			m.disposition(w, e)
			return
		}
		var profile projectenv.Profile
		_ = json.Unmarshal(run.Environment, &profile)
		run.ResultCommit, run.Files, err = m.files.CaptureArchive(r.Context(), run.ResourceID, run.BaselineCommit, archive, projectenv.SnapshotPolicy(profile))
		archive.Close()
		if err != nil {
			m.rejectResult(w, r, a, problem("invalid_artifact", "The result archive failed the resource policy: "+m.redact(err.Error())))
			return
		}
		for i := range run.Files {
			run.Files[i].Before = m.redact(run.Files[i].Before)
			run.Files[i].After = m.redact(run.Files[i].After)
		}
	}
	if run.TaskID != "" && result.Status == "succeeded" && result.Intent != nil {
		commit := run.ResultCommit
		if commit == "" {
			commit = run.BaselineCommit
		}
		for _, name := range result.Intent.ArtifactPaths {
			if _, e := m.files.ReadFile(r.Context(), run.ResourceID, commit, name); e != nil {
				m.rejectResult(w, r, a, problem("invalid_intent", "A declared task artifact is missing from the immutable checkpoint."))
				return
			}
		}
	}
	run.Output = bounded(run.Output+"\n[Camp] Execution resources "+map[bool]string{true: "were released.", false: "could not be confirmed clean."}[result.CleanupConfirmed], storage.MaxRunOutputSize)
	saved, err := m.store.FinishRemoteRun(r.Context(), a.ID, runner.ID, result.CompletionID, hash, run, m.redact(result.Summary), result.Resumable && run.Status == "succeeded", result.CleanupConfirmed)
	if err != nil {
		if errorCode(err) == "invalid_intent" {
			m.rejectResult(w, r, a, err)
		} else {
			m.disposition(w, err)
		}
		return
	}
	m.stopProxy(a.ID)
	m.Wake()
	if !result.CleanupConfirmed {
		m.StopRunner(runner.ID)
	}
	sendJSON(w, 200, p.Receipt{Disposition: "committed", RunID: saved.ID, Status: saved.Status})
}
func (m *Manager) rejectResult(w http.ResponseWriter, r *http.Request, a storage.Attempt, reason error) {
	m.stopProxy(a.ID)
	if _, err := m.store.InterruptAttempt(r.Context(), a.ID, "Runner result rejected: "+m.redact(reason.Error())); err != nil {
		m.disposition(w, err)
		return
	}
	m.Wake()
	current, err := m.store.GetAttempt(r.Context(), a.ID)
	if err != nil {
		m.disposition(w, err)
		return
	}
	// A concurrent completion may already have won the transaction. Return its
	// durable receipt rather than turning that committed success into rejection.
	sendJSON(w, 200, storedReceipt(current))
}
func validateResult(run storage.Run, result p.Result) error {
	if run.TaskID == "" && result.Intent != nil {
		return problem("invalid_result", "Ordinary work cannot create a collaboration intent.")
	}
	if !validResultText(result.CompletionID, 128, false) || !terminal(result.Status) || !validResultText(result.Error, storage.MaxRunErrorSize, true) || !validResultText(result.Summary, 32<<10, true) {
		return problem("invalid_result", "The terminal result is invalid.")
	}
	checks, err := json.Marshal(result.Checks)
	if err != nil || len(checks) > storage.MaxRunOutputSize {
		return problem("invalid_result", "The combined check results exceed their storage limit.")
	}
	if result.Archive != nil && (run.AgentSnapshot.Access != "write" || result.Archive.Bytes < 0 || result.Archive.Bytes > p.MaxArchiveBytes || len(result.Archive.SHA256) != 64 || result.Archive.URL != "") {
		return problem("invalid_result", "The result archive manifest is invalid for this role.")
	}
	if result.Status == "succeeded" && run.AgentSnapshot.Access == "write" && result.Archive == nil {
		return problem("invalid_result", "A successful writer must provide its immutable output archive.")
	}
	if result.Status == "succeeded" && result.ExitCode != nil && *result.ExitCode != 0 {
		return problem("invalid_result", "A successful execution cannot have a failing exit code.")
	}
	if result.Status == "succeeded" && result.TestExitCode != nil && *result.TestExitCode != 0 {
		return problem("invalid_result", "A successful execution cannot have a failing check exit code.")
	}
	var profile projectenv.Profile
	if len(run.Environment) > 0 && json.Unmarshal(run.Environment, &profile) != nil {
		return problem("invalid_result", "The frozen environment cannot be decoded.")
	}
	expected := map[string]string{}
	if run.AgentSnapshot.Access == "write" && (run.TaskID == "" || result.Intent == nil || result.Intent.Kind == "finish") {
		for _, check := range profile.Checks {
			expected[check.Name] = projectenv.CheckCommand(check)
		}
	}
	seen := map[string]bool{}
	for _, check := range result.Checks {
		command, ok := expected[check.Name]
		start, se := time.Parse(time.RFC3339Nano, check.StartedAt)
		end, ee := time.Parse(time.RFC3339Nano, check.FinishedAt)
		if !ok || seen[check.Name] || check.Command != command || len(check.Output) > 64<<10 || se != nil || ee != nil || end.Before(start) || (check.Status != "passed" && check.Status != "failed" && check.Status != "interrupted") {
			return problem("invalid_result", fmt.Sprintf("The check record %q does not match the frozen check plan.", check.Name))
		}
		seen[check.Name] = true
		if check.Status == "passed" && (check.ExitCode == nil || *check.ExitCode != 0) {
			return problem("invalid_result", "A passing check requires a zero exit code.")
		}
		if result.Status == "succeeded" && check.Status != "passed" {
			return problem("invalid_result", "Successful publication requires every configured check to pass.")
		}
	}
	if result.Status == "succeeded" && len(seen) != len(expected) {
		return problem("invalid_result", "Configured checks are missing from the final result.")
	}
	return nil
}
func validResultText(value string, limit int, empty bool) bool {
	return utf8.ValidString(value) && len(value) <= limit && (empty || strings.TrimSpace(value) != "") && !strings.ContainsRune(value, 0)
}
func bounded(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	const marker = "\n[Camp output truncated at its configured limit]\n"
	end := limit - len(marker)
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end] + marker
}
