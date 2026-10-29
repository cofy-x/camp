package runs

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/dsh"
	runner "github.com/cofy-x/camp/apps/server/internal/execution"
	"github.com/cofy-x/camp/apps/server/internal/projectenv"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func (m *Manager) record(e *execution, kind, text string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	text = m.redact(text)
	e.run.Output = boundedOutput(e.run.Output, text)
	if kind == "assistant" {
		e.assistant = boundedOutput(e.assistant, text)
	}
	eventText := text
	if len(eventText) > storage.MaxRunEventSize {
		const marker = "\n[Event content exceeded 64 KiB and was truncated]"
		eventText = truncate(eventText, storage.MaxRunEventSize-len(marker)) + marker
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	saved, err := m.store.UpdateRunWithEvent(ctx, e.run, kind, eventText)
	if err == nil {
		e.run = saved
	} else {
		e.writeErr = err
		e.cancel()
	}
	return err
}

func (m *Manager) capture(ctx context.Context, run storage.Run) ([]storage.RunFile, string, error) {
	if run.AgentSnapshot.Access != "write" || run.BaselineCommit == "" {
		return []storage.RunFile{}, "", nil
	}
	dir := filepath.Join(m.dir, run.ID)
	archive, err := confirmedArchive(dir)
	if err != nil || archive == nil {
		return []storage.RunFile{}, "", err
	}
	defer archive.Close()
	var profile projectenv.Profile
	_ = json.Unmarshal(run.Environment, &profile)
	commit, files, err := m.resources.CaptureArchive(ctx, run.ResourceID, run.BaselineCommit, archive, projectenv.SnapshotPolicy(profile))
	return files, commit, err
}

func (m *Manager) execute(ctx context.Context, e *execution) {
	defer e.cancel()
	m.mu.Lock()
	input := e.run
	m.mu.Unlock()
	dir := filepath.Join(m.dir, input.ID)
	var err error
	e.model, err = m.models.Resolve(input.RuntimeKey)

	if err == nil {
		err = m.resources.Prepare(ctx, input.ResourceID, input.BaselineCommit, dir)
	}
	var referencesDir string
	if err == nil {
		referencesDir, err = m.prepareReferences(ctx, input, dir)
	}
	if referencesDir != "" {
		defer os.RemoveAll(referencesDir)
	}
	if err == nil {
		err = m.prepareEnvironment(ctx, e, dir)
	}
	if err == nil {
		err = m.prepareLayerFS(ctx, e, dir, referencesDir)
	}
	if err == nil {
		e.conversation, err = m.store.GetConversation(ctx, input)
	}
	var prompt, stateDir string
	if err == nil {
		stateDir = filepath.Join(filepath.Dir(m.dir), "sessions", e.conversation.ID, fmt.Sprintf("%d", e.conversation.Generation))
		err = os.MkdirAll(stateDir, 0700)
	}
	if err == nil {
		persona := "You are " + input.AgentSnapshot.Name + " (@" + input.AgentSnapshot.Handle + ") in Camp. " + input.AgentSnapshot.Instructions
		var overlay []byte
		overlay, err = dsh.RemoteOverlay(persona, input.AgentSnapshot.Access == "read")
		if err == nil {
			err = os.WriteFile(filepath.Join(dir, "runtime.patch.yml"), overlay, 0600)
		}
	}
	if err == nil {
		prompt, err = m.conversationPrompt(ctx, input, e.conversation)
	}
	if err == nil {
		err = m.update(e, func(r *storage.Run) {
			if r.Status != "cancelling" {
				r.Status = "running"
			}
			r.SessionID = e.conversation.SessionID
			r.SessionGeneration = e.conversation.Generation
			r.SessionMode = "new"
			if e.conversation.Resumable {
				r.SessionMode = "resume"
			}
		})
	}
	if err == nil {
		contextNote := "Starting a new private conversation context."
		if e.conversation.Resumable {
			contextNote = "Resuming the private conversation context."
		} else if e.conversation.Generation > 1 {
			contextNote = "Rebuilding conversation context from visible messages after an interruption or configuration change. No previous request is replayed."
		}
		if !e.conversation.Resumable && e.conversation.Generation > 1 {
			err = m.store.AddRunNotice(ctx, input.ID, contextNote)
		}
		if err == nil {
			err = m.record(e, "context", "[Camp] "+contextNote+"\n")
		}
	}
	if err == nil {
		config := runner.Config{RuntimeDir: m.config.RuntimeDir, NodePath: m.config.NodePath, APIKey: e.model.APIKey, BaseURL: e.model.BaseURL, ReadOnlyProject: input.AgentSnapshot.Access == "read", StateDir: stateDir, RemoteTools: true, ToolEndpoint: e.tools.Endpoint(), ToolToken: e.tools.Token()}
		cmd, commandErr := runner.Command(config, dir, m.lock, false)
		err = commandErr
		if err == nil {
			watchDone := make(chan struct{})
			go func() {
				select {
				case <-e.tools.Failed():
					e.cancel()
				case <-watchDone:
				}
			}()
			code, modelErr := dsh.Run(ctx, cmd, dsh.Config{Model: input.Model, MaxTokens: input.MaxTokens, MaxSteps: input.MaxSteps, Resume: e.conversation.Resumable}, e.conversation.SessionID, prompt, func(event dsh.Event) error {
				if event.Type == "model" {
					e.modelRequested++
				}
				if e.modelRequested > 0 && (event.Type == "assistant" || event.Type == "tool_call") {
					e.modelResponded = e.modelRequested
				}
				return m.record(e, event.Type, event.Text)
			})
			close(watchDone)
			m.recordModelDiagnostic(input.RuntimeKey, e, modelErr, ctx.Err())
			err = errors.Join(modelErr, m.update(e, func(r *storage.Run) { r.ExitCode = &code }))
		}
	}
	// DSH can no longer issue tools while checks or the immutable export run.
	toolsSettled := true
	if e.tools != nil {
		closeErr := e.tools.Close()
		toolsSettled = closeErr == nil
		err = errors.Join(err, closeErr)
	}
	if err == nil {
		err = ctx.Err()
	}
	if err == nil && input.AgentSnapshot.Access == "write" && e.environment != nil && len(e.environment.Profile.Checks) > 0 {
		err = m.update(e, func(r *storage.Run) {
			if r.Status != "cancelling" {
				r.Status = "checking"
			}
		})
		if err == nil {
			err = m.verifyEnvironment(ctx, e)
		}
	}
	if e.workspace.WorkspaceID != "" {
		if toolsSettled {
			err = errors.Join(err, m.exportLayerFS(e, dir))
		}
	}
	if e.layerProject != "" || e.workspace.WorkspaceID != "" {
		e.cleanupErr = m.closeLayerFS(e)
		err = errors.Join(err, e.cleanupErr)
	}
	captureCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	m.mu.Lock()
	snapshot := e.run
	m.mu.Unlock()
	files, commit, captureErr := m.capture(captureCtx, snapshot)
	cancel()
	m.redactFiles(files)
	m.finish(e, ctx, files, commit, errors.Join(err, captureErr))
}

func (m *Manager) finish(e *execution, ctx context.Context, files []storage.RunFile, commit string, err error) {
	m.mu.Lock()
	e.finishing = true
	err = errors.Join(err, e.writeErr)
	status, message := "succeeded", ""
	if err != nil {
		status, message = "failed", m.redact(err.Error())
	}
	if e.interrupted {
		status, message = "interrupted", "The service stopped before execution completed. Existing output and files were retained."
	} else if e.cancelled && e.cleanupErr == nil {
		status, message = "cancelled", "Cancelled. Execution processes and temporary isolation resources were released."
	} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		status, message = "failed", "Execution exceeded the time budget."
	}
	e.run.Status, e.run.Error, e.run.Files, e.run.ResultCommit = status, truncate(message, storage.MaxRunErrorSize), files, commit
	completion := "Execution processes have exited."
	if e.cleanupErr != nil {
		completion = "Sandbox cleanup could not be confirmed; further execution is suspended."
	}
	// Publication and authorization can still change the terminal status in
	// FinishRun. Only report the observed cleanup fact before that transaction.
	e.run.Output = boundedOutput(e.run.Output, "\n[Camp] "+completion+"\n")
	// No check stays running after cancellation, process failure, or timeout.
	var checks []projectenv.CheckResult
	if json.Unmarshal(e.run.Checks, &checks) == nil {
		for i := range checks {
			if checks[i].Status == "running" {
				checks[i].Status = "interrupted"
				checks[i].FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
			}
		}
		e.run.Checks, _ = json.Marshal(checks)
	}
	saveCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	saved, saveErr := m.store.FinishRun(saveCtx, e.run, truncate(e.assistant, 32<<10), status == "succeeded")
	if saveErr != nil {
		m.logger.Error("Failed to save the final execution state", "id", e.run.ID)
		failed := e.run
		failed.Status = "failed"
		failed.Error = truncate("The final record could not be saved. Current results are retained in memory until the service stops; durable output and files remain available for restart recovery. "+failed.Error, storage.MaxRunErrorSize)
		failed.FinishedAt = time.Now().UTC().Format(time.RFC3339Nano)
		m.unsaved[e.run.ID] = failed
	} else {
		e.run = saved
		m.recordExecutionDiagnostic(saved)
	}
	m.mu.Unlock()
	if saveErr == nil {
		if cleanupErr := m.pruneRunScratch(saved.ID, saved.ResultCommit != "" || saved.AgentSnapshot.Access == "read"); cleanupErr != nil {
			m.logger.Error("Could not remove terminal execution scratch", "id", saved.ID)
		}
	}
	m.mu.Lock()
	delete(m.active, e.run.ID)
	close(e.done)
	m.Wake()
	m.mu.Unlock()
}
