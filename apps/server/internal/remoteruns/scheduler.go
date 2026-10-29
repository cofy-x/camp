package remoteruns

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/resources"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/runs"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func (m *Manager) recover(ctx context.Context) error {
	previous, err := m.store.ListActiveAttempts(ctx)
	if err != nil {
		return err
	}
	{
		for _, attempt := range previous {
			if attempt.ModelInFlight {
				if _, err = m.store.InterruptAttempt(ctx, attempt.ID, "The server restarted during a model stream. The stream cannot resume and the request was not retried."); err != nil {
					return err
				}
			}
		}
	}
	// Historical local processes cannot survive an upgrade. Keep their true origin.
	old, err := m.store.ListUnfinishedRuns(ctx)
	if err != nil {
		return err
	}
	{
		for _, run := range old {
			if run.RunnerID == "" {
				run.Status = "interrupted"
				run.Error = "The local execution ended before the remote-runner upgrade. Submit a new request after choosing a runner."
				if _, err = m.store.FinishRun(ctx, run, "", false); err != nil {
					return err
				}
			}
		}
	}
	return nil
}
func (m *Manager) schedule() {
	defer close(m.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for {
		if err := m.tick(m.ctx); err != nil && m.ctx.Err() == nil {
			m.logger.Error("Remote scheduler could not reconcile durable state", "code", errorCode(err))
		}
		select {
		case <-m.ctx.Done():
			return
		case <-m.wake:
		case <-ticker.C:
		}
	}
}
func (m *Manager) tick(ctx context.Context) error {
	tasks, e := m.store.ListOpenTasks(ctx)
	if e != nil {
		return e
	}
	for _, task := range tasks {
		if e = m.store.ReconcileTask(ctx, task.ID); e != nil {
			return e
		}
	}
	if time.Since(m.lastGC) > time.Minute {
		if err := m.collectTerminalScratch(ctx); err != nil {
			return err
		}
		m.lastGC = time.Now()
	}
	active, err := m.store.ListActiveAttempts(ctx)
	if err != nil {
		return err
	}
	count := 0
	for _, a := range active {
		if a.CancelRequested {
			m.stopProxy(a.ID)
		}
		if !a.CancelRequested {
			if _, checkErr := m.store.ValidateAttempt(ctx, a.ID, a.RunnerID, false); errorCode(checkErr) == "run_cancelled" {
				if _, err = m.store.CancelRemoteRun(ctx, a.RunID, "Execution authority changed before completion."); err != nil {
					return err
				}
				m.stopProxy(a.ID)
				a.CancelRequested = true
			}
		}
		runner, e := m.store.GetRunner(ctx, a.RunnerID)
		if expired(a.LeaseUntil) || e != nil || runner.Revoked || !runner.Enabled {
			reason := "Execution authorization expired before a final receipt; the request was not replayed."
			if e == nil && (runner.Revoked || !runner.Enabled) {
				reason = "The assigned runner was disabled or revoked before completion."
			}
			m.stopProxy(a.ID)
			if _, err = m.store.InterruptAttempt(ctx, a.ID, reason); err != nil {
				return err
			}
			continue
		}
		count++
		if len(a.Command) == 0 {
			run, e := m.store.GetRun(ctx, a.RunID)
			if e == nil {
				e = m.prepare(ctx, run, a)
			}
			if e != nil {
				_, _ = m.store.InterruptAttempt(ctx, a.ID, "Execution input could not be prepared: "+m.redact(e.Error()))
				continue
			}
		}
	}
	queued, err := m.store.ListQueuedRuns(ctx)
	if err != nil {
		return err
	}
	for _, run := range queued {
		if count >= m.config.Concurrency {
			break
		}
		if run.RunnerID == "" || !m.IsOnline(run.RunnerID) {
			continue
		}
		runner, err := m.store.GetRunner(ctx, run.RunnerID)
		if err != nil {
			return err
		}
		var caps p.Capabilities
		_ = json.Unmarshal(runner.Capabilities, &caps)
		if !runner.Enabled || runner.Revoked || !caps.Ready || (caps.Executor == "fixture" && !m.config.AllowFixture) {
			continue
		}
		claimed, a, err := m.store.ClaimRemoteRun(ctx, run.ID, run.RunnerID, m.config.LeaseDuration)
		if err != nil {
			if errorCode(err) == "run_busy" || errorCode(err) == "resource_busy" || errorCode(err) == "run_finished" || errorCode(err) == "agent_unavailable" {
				continue
			}
			return err
		}
		if err = m.prepare(ctx, claimed, a); err != nil {
			_, _ = m.store.InterruptAttempt(ctx, a.ID, "Execution input could not be prepared: "+m.redact(err.Error()))
			continue
		}
		count++
	}
	if err = m.scheduleSources(ctx); err != nil {
		return err
	}
	m.mu.Lock()
	peers := make([]*peer, 0, len(m.peers))
	for _, peer := range m.peers {
		peers = append(peers, peer)
	}
	m.mu.Unlock()
	for _, peer := range peers {
		if err = m.dispatch(ctx, peer); err != nil {
			peer.close()
		}
	}
	return nil
}

func (m *Manager) operationDir(id string) string {
	return filepath.Join(m.config.DataDir, "remote", id)
}
func (m *Manager) prepare(ctx context.Context, run storage.Run, a storage.Attempt) error {
	dir := m.operationDir(a.ID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	if _, err := m.models.Resolve(run.RuntimeKey); err != nil {
		return err
	}
	conv, err := m.store.GetConversation(ctx, run)
	if err != nil {
		return err
	}
	prompt, refs, err := runs.PrepareRemoteInput(ctx, m.store, m.files, run, conv, dir)
	defer func() {
		if refs != "" {
			_ = os.RemoveAll(refs)
		}
	}()
	if err != nil {
		return err
	}
	var taskSpec *p.Collaboration
	if run.TaskID != "" {
		task, e := m.store.GetTask(ctx, run.TaskID)
		if e != nil {
			return e
		}
		prompt = task.TaskBrief(run) + "\nRoom context:\n" + prompt
		if run.PreviousDraftCommit != "" {
			if refs == "" {
				refs, e = os.MkdirTemp(dir, ".references-")
				if e != nil {
					return e
				}
			}
			if e = m.taskDraftEvidence(ctx, run, refs); e != nil {
				return e
			}
			prompt += "\nPrevious task draft is read-only evidence at /references/previous-task-draft. Reconcile it with current /workspace; do not replace the shared tree wholesale.\n"
		}
		taskSpec = &p.Collaboration{ID: task.ID, Goal: task.Goal, AgentID: run.AgentID, Participants: []p.Participant{}}
		for _, participant := range task.Participants {
			taskSpec.Participants = append(taskSpec.Participants, p.Participant{ID: participant.Agent.ID, Name: participant.Agent.Name, Access: participant.Agent.Access})
		}
	}
	project, err := writeArtifact(filepath.Join(dir, "project.tar"), func(w io.Writer) error { return m.files.ExportArchive(ctx, run.ResourceID, run.BaselineCommit, w) })
	if err != nil {
		return err
	}
	project.URL = "/api/daemon/operations/" + a.ID + "/input?part=project"
	var references *p.Artifact
	if refs != "" {
		v, e := writeArtifact(filepath.Join(dir, "references.tar"), func(w io.Writer) error { return resources.WriteArchive(ctx, refs, w) })
		if e != nil {
			return e
		}
		v.URL = "/api/daemon/operations/" + a.ID + "/input?part=references"
		references = &v
	}
	if err = m.store.SaveAttemptInput(ctx, a.ID, project.SHA256, filepath.Join(dir, "project.tar")); err != nil {
		return err
	}
	run.SessionID, run.SessionGeneration, run.SessionMode = conv.SessionID, conv.Generation, "new"
	if conv.Resumable {
		run.SessionMode = "resume"
	} else if conv.Generation > 1 {
		if err = m.store.AddRunNotice(ctx, run.ID, "Rebuilding context on the assigned runner from visible messages. Previous requests are not replayed."); err != nil {
			return err
		}
	}
	if _, err = m.store.UpdateRun(ctx, run); err != nil {
		return err
	}
	token := secret()
	command := p.Command{ID: a.CommandID, Kind: "run.start", OperationID: a.ID, Deadline: a.Deadline, Spec: &p.ExecutionSpec{Task: taskSpec, RunID: run.ID, AttemptID: a.ID, ResourceID: run.ResourceID, BaselineCommit: run.BaselineCommit, Fence: a.WriterFence, AgentName: run.AgentSnapshot.Name, Instructions: run.AgentSnapshot.Instructions, Access: run.AgentSnapshot.Access, Prompt: prompt, Request: run.Prompt, Environment: run.Environment, Model: run.Model, MaxTokens: run.MaxTokens, MaxSteps: run.MaxSteps, TimeoutSeconds: run.TimeoutSeconds, ConversationID: conv.ID, SessionID: conv.SessionID, Generation: conv.Generation, Resume: conv.Resumable, ModelURL: "/api/daemon/model/" + a.ID, ModelToken: token, Project: project, References: references}}
	data, err := json.Marshal(command)
	if err != nil {
		return err
	}
	return m.store.SaveAttemptCommand(ctx, a.ID, data, digest([]byte(token)))
}
func writeArtifact(path string, write func(io.Writer) error) (p.Artifact, error) {
	f, err := os.CreateTemp(filepath.Dir(path), ".artifact-")
	if err != nil {
		return p.Artifact{}, err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if err = write(f); err != nil {
		return p.Artifact{}, err
	}
	if err = f.Sync(); err != nil {
		return p.Artifact{}, err
	}
	if err = f.Close(); err != nil {
		return p.Artifact{}, err
	}
	if err = os.Rename(f.Name(), path); err != nil {
		return p.Artifact{}, err
	}
	dir, err := os.Open(filepath.Dir(path))
	if err != nil {
		return p.Artifact{}, err
	}
	err = dir.Sync()
	dir.Close()
	if err != nil {
		return p.Artifact{}, err
	}
	return artifactManifest(path)
}
func (m *Manager) Cancel(ctx context.Context, id string) (storage.Run, error) {
	run, err := m.store.CancelRemoteRun(ctx, id, "Cancellation requested.")
	if err == nil {
		if run.AttemptID != "" {
			m.stopProxy(run.AttemptID)
		}
		m.Wake()
	}
	return run, err
}
