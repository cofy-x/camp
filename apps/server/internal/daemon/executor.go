package daemon

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/nativeexecutor"
	"github.com/cofy-x/camp/apps/server/internal/projectenv"
	"github.com/cofy-x/camp/apps/server/internal/resources"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

func (d *Daemon) execute(ctx context.Context, op operation) {
	id := op.Command.OperationID
	directory := filepath.Join(d.config.DataDir, "operations", id)
	result := p.Result{CompletionID: randomID(), Status: "failed", Checks: []p.CheckResult{}, CleanupConfirmed: true}
	var archive string
	err := os.MkdirAll(directory, 0700)
	emit := func(kind, text string) error {
		if op.Command.Spec != nil && op.Command.Spec.ModelToken != "" {
			text = strings.ReplaceAll(text, op.Command.Spec.ModelToken, "[redacted]")
		}
		text = strings.ToValidUTF8(text, "�")
		if len(text) > 60<<10 {
			text = strings.ToValidUTF8(text[:60<<10], "�") + "\n[Event truncated]"
		}
		writeCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		err := d.journal.appendEvent(writeCtx, id, kind, text)
		if err != nil {
			d.setFailure(err)
		}
		return err
	}
	if err == nil && op.Command.Kind == "source.capture" {
		result, archive, err = d.capture(ctx, op.Command, directory, emit)
	} else if err == nil && op.Command.Spec != nil {
		spec := op.Command.Spec
		if spec.Resume && d.config.Executor == "native" {
			var saved string
			resumeErr := d.journal.db.QueryRowContext(ctx, "SELECT value FROM metadata WHERE key=?", "session:"+sessionKey(spec)).Scan(&saved)
			info, statErr := os.Stat(filepath.Join(d.config.DataDir, "sessions", sourceRevision(sessionKey(spec))))
			if resumeErr != nil || saved != spec.SessionID || statErr != nil || !info.IsDir() {
				result.Status = "interrupted"
				result.Error = "The private native session is unavailable. Send a new request to rebuild context; this request was not replayed."
				err = errors.New(result.Error)
			}
		}
		if err == nil {
			_, err = d.journal.db.ExecContext(ctx, "DELETE FROM metadata WHERE key=?", "session:"+sessionKey(spec))
		}
		if err == nil {
			err = d.download(ctx, spec.Project, filepath.Join(directory, "project"))
		}
		references := ""
		if err == nil && spec.References != nil {
			references = filepath.Join(directory, "references")
			err = d.download(ctx, *spec.References, references)
		}
		if err == nil {
			err = emit("status", "running")
		}
		if err == nil {
			if d.config.Executor == "fixture" {
				result, archive, err = d.fixture(ctx, *spec, directory, emit)
				if err == nil && result.Intent != nil {
					if err = d.journal.saveIntent(ctx, id, *result.Intent); err != nil {
						d.setFailure(err)
					}
				}
			} else if d.native == nil {
				err = errors.New("Native execution is unavailable")
			} else {
				modelURL, urlErr := endpoint(d.config.ServerURL, spec.ModelURL)
				if urlErr != nil {
					err = urlErr
				} else {
					persistIntent := func(ctx context.Context, intent p.CoordinationIntent) error {
						if err := d.journal.saveIntent(ctx, id, intent); err != nil {
							d.setFailure(err)
							return err
						}
						return nil
					}
					request := nativeexecutor.Request{Task: spec.Task, PersistIntent: persistIntent, AttemptID: id, RunID: spec.RunID, ResourceID: spec.ResourceID, BaselineCommit: spec.BaselineCommit, Directory: directory, ReferencesDir: references, Persona: "You are " + spec.AgentName + " in Camp. " + spec.Instructions, Access: spec.Access, Prompt: spec.Prompt, Model: spec.Model, ModelURL: modelURL, ModelToken: spec.ModelToken, MaxTokens: spec.MaxTokens, MaxSteps: spec.MaxSteps, Timeout: time.Duration(spec.TimeoutSeconds) * time.Second, Environment: spec.Environment, SessionID: spec.SessionID, SessionKey: spec.ConversationID + ":" + strconv.FormatInt(spec.Generation, 10), Resume: spec.Resume}
					native := d.native.Execute(ctx, request, emit)
					result = p.Result{Intent: native.Intent, CompletionID: randomID(), Status: native.Status, Error: native.Error, Summary: native.Assistant, ExitCode: native.ExitCode, TestExitCode: native.TestExitCode, Checks: []p.CheckResult{}, CleanupConfirmed: native.CleanupConfirmed, Resumable: native.SessionResumable}
					for _, check := range native.Checks {
						result.Checks = append(result.Checks, p.CheckResult{Name: check.Name, Command: check.Command, Status: check.Status, ExitCode: check.ExitCode, Output: check.Output, StartedAt: check.StartedAt, FinishedAt: check.FinishedAt})
					}
					archive = native.ArchivePath
					if archive != "" {
						result.Archive = &p.Artifact{SHA256: native.ArchiveSHA256, Bytes: native.ArchiveBytes}
					}
				}
			}
		}
	}
	if err != nil {
		if result.Status != "interrupted" {
			result.Status = "failed"
		}
		result.Error = err.Error()
	}
	if ctx.Err() != nil {
		result.Status = "interrupted"
		result.Error = "Execution interrupted; the original request will not be replayed."
		result.Resumable = false
	}
	d.mu.Lock()
	active := d.active[id]
	cancelled := active != nil && active.cancelled
	d.mu.Unlock()
	if cancelled && result.CleanupConfirmed {
		result.Status = "cancelled"
		result.Error = "Cancelled. Execution resources were released."
		result.Resumable = false
	}
	if !result.CleanupConfirmed {
		d.setFailure(errors.New("Sandbox cleanup could not be confirmed. Restart this Runner to reconcile resources."))
	}
	if d.native != nil {
		if unavailable := d.native.Ready(); unavailable != nil {
			d.setFailure(unavailable)
		}
	}
	if result.CompletionID == "" {
		result.CompletionID = randomID()
	}
	if result.Checks == nil {
		result.Checks = []p.CheckResult{}
	}
	if result.Archive != nil {
		if verifyErr := verifyArchive(archive, *result.Archive, true); verifyErr != nil {
			d.setFailure(errors.New("The result archive could not be durably verified. Restart this Runner after checking its storage."))
			result.Archive = nil
			archive = ""
			result.Status = "failed"
			result.Error = "The result archive could not be durably verified."
			result.Resumable = false
		}
	}
	if op.Command.Spec != nil && op.Command.Spec.ModelToken != "" {
		token := op.Command.Spec.ModelToken
		result.Error = strings.ReplaceAll(result.Error, token, "[redacted]")
		result.Summary = strings.ReplaceAll(result.Summary, token, "[redacted]")
		for i := range result.Checks {
			result.Checks[i].Output = strings.ReplaceAll(result.Checks[i].Output, token, "[redacted]")
		}
	}
	writeCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	finishErr := d.journal.finish(writeCtx, id, result, archive)
	stop()
	if finishErr != nil {
		d.setFailure(finishErr)
	}
	d.mu.Lock()
	delete(d.active, id)
	d.mu.Unlock()
	pruneCtx, stop := context.WithTimeout(context.Background(), 5*time.Second)
	d.pruneAcknowledged(pruneCtx, id)
	stop()
}

func (d *Daemon) capture(ctx context.Context, command p.Command, directory string, emit nativeexecutor.Emit) (p.Result, string, error) {
	result := p.Result{CompletionID: randomID(), Status: "succeeded", Checks: []p.CheckResult{}, CleanupConfirmed: true}
	if command.Source == nil {
		return result, "", errors.New("Source capture is missing its frozen source")
	}
	source, ok := d.config.Sources[command.Source.SourceID]
	if !ok || sourceRevision(source) != command.Source.Revision {
		return result, "", errors.New("The selected source changed or is no longer registered")
	}
	available := false
	for _, item := range d.sources() {
		if item.ID == command.Source.SourceID {
			available = item.Available
			break
		}
	}
	if !available {
		return result, "", errors.New("The selected source is unavailable or overlaps private Runner data")
	}
	if err := emit("source", "Capturing the authorized source directory.\n"); err != nil {
		return result, "", err
	}
	if err := resources.CaptureStableSource(ctx, source, filepath.Join(directory, "project")); err != nil {
		return result, "", err
	}
	archive, artifact, err := pack(ctx, directory)
	if err == nil {
		result.Archive = &artifact
	}
	result.Summary = "Source captured without changing the original directory."
	return result, archive, err
}

// Fixture is a deterministic protocol driver: it never evaluates the prompt as
// code or invokes shell commands. Scenarios are explicit fixed markers.
func (d *Daemon) fixture(ctx context.Context, spec p.ExecutionSpec, directory string, emit nativeexecutor.Emit) (p.Result, string, error) {
	// Scenario controls belong to this request. Quoted evidence or an earlier
	// failed/cancelled request must not inject a fault into the next operation.
	scenario := spec.Request
	if scenario == "" {
		scenario = spec.Prompt
	}
	result := p.Result{CompletionID: randomID(), Status: "succeeded", Checks: []p.CheckResult{}, CleanupConfirmed: true, Resumable: true}
	if err := emit("fixture", "[Camp fixture] Execution accepted on "+d.config.Name+".\n"); err != nil {
		return result, "", err
	}
	if strings.Contains(scenario, "[fixture:wait]") {
		if err := emit("fixture", "[Camp fixture] Waiting at the cancellation barrier.\n"); err != nil {
			return result, "", err
		}
		<-ctx.Done()
		return result, "", ctx.Err()
	}
	if strings.Contains(scenario, "[fixture:fail]") {
		return result, "", errors.New("Requested fixture execution failure")
	}
	if spec.Access == "write" {
		content := "Generated by Camp fixture Runner " + d.config.Name + ".\n\n" + spec.Prompt + "\n"
		if err := os.WriteFile(filepath.Join(directory, "project", "result.txt"), []byte(content), 0644); err != nil {
			return result, "", err
		}
	}
	if spec.Task != nil {
		intent, err := fixtureTaskIntent(spec, scenario)
		if err != nil {
			return result, "", err
		}
		result.Intent = intent
	}
	result.Summary = "The fixture result is ready for human review."
	if spec.Access == "write" && (result.Intent == nil || result.Intent.Kind == "finish") && len(spec.Environment) > 0 && string(spec.Environment) != "null" && string(spec.Environment) != "{}" {
		var profile projectenv.Profile
		if err := json.Unmarshal(spec.Environment, &profile); err != nil {
			return result, "", err
		}
		if err := projectenv.Validate(profile); err != nil {
			return result, "", err
		}
		if len(profile.Checks) > 0 {
			if err := emit("status", "checking"); err != nil {
				return result, "", err
			}
		}
		for _, check := range profile.Checks {
			code := 0
			status := "passed"
			if strings.Contains(scenario, "[fixture:check-fail]") {
				code = 1
				status = "failed"
				result.Status = "failed"
				result.Error = "A configured fixture check failed."
			}
			stamp := time.Now().UTC().Format(time.RFC3339Nano)
			item := p.CheckResult{Name: check.Name, Command: projectenv.CheckCommand(check), Status: status, ExitCode: &code, Output: "Deterministic fixture check; no command was executed.", StartedAt: stamp, FinishedAt: stamp}
			if strings.Contains(scenario, "[fixture:corrupt-check]") {
				item.Name = "unconfigured-check"
			}
			result.Checks = append(result.Checks, item)
			if err := emit("check", check.Name+": "+status+"\n"); err != nil {
				return result, "", err
			}
		}
		if len(result.Checks) > 0 {
			code := 0
			if result.Status != "succeeded" {
				code = 1
			}
			result.TestExitCode = &code
		}
	}
	if err := emit("assistant", result.Summary+"\n"); err != nil {
		return result, "", err
	}
	if spec.Access != "write" {
		return result, "", nil
	}
	archive, artifact, err := pack(ctx, directory)
	if err == nil {
		result.Archive = &artifact
	}
	return result, archive, err
}

func pack(ctx context.Context, directory string) (string, p.Artifact, error) {
	name := filepath.Join(directory, "result.tar")
	file, err := os.OpenFile(name, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0600)
	if err != nil {
		return "", p.Artifact{}, err
	}
	hash := sha256.New()
	err = resources.WriteArchive(ctx, filepath.Join(directory, "project"), io.MultiWriter(file, hash))
	info, statErr := file.Stat()
	err = errors.Join(err, statErr, file.Sync(), file.Close())
	if err != nil {
		return "", p.Artifact{}, err
	}
	return name, p.Artifact{SHA256: hex.EncodeToString(hash.Sum(nil)), Bytes: info.Size()}, nil
}

func verifyArchive(name string, artifact p.Artifact, sync bool) error {
	file, err := os.Open(name)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != artifact.Bytes || artifact.Bytes < 0 || artifact.Bytes > p.MaxArchiveBytes {
		return errors.New("Archive size is invalid")
	}
	hash := sha256.New()
	if _, err = io.Copy(hash, file); err != nil {
		return err
	}
	if hex.EncodeToString(hash.Sum(nil)) != artifact.SHA256 {
		return errors.New("Archive checksum is invalid")
	}
	if sync {
		if err = file.Sync(); err != nil {
			return err
		}
		directory, err := os.Open(filepath.Dir(name))
		if err != nil {
			return err
		}
		return errors.Join(directory.Sync(), directory.Close())
	}
	return nil
}

func sourceRevision(source string) string {
	sum := sha256.Sum256([]byte(source))
	return hex.EncodeToString(sum[:])
}
