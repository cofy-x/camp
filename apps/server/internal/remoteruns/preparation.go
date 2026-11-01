package remoteruns

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"

	"github.com/cofy-x/camp/apps/server/internal/resources"
	"github.com/cofy-x/camp/apps/server/internal/runinput"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

// PreparedInput contains private immutable files and a frozen execution spec.
// The builder cannot activate capabilities, dispatch work or publish a result.
type PreparedInput struct {
	spec           *p.ExecutionSpec
	inputPath      string
	conversation   storage.Conversation
	command        json.RawMessage
	modelTokenHash string
}

func (m *Manager) operationDir(id string) string {
	return filepath.Join(m.config.DataDir, "remote", id)
}

func (m *Manager) buildInput(ctx context.Context, run storage.Run, a storage.Attempt) (PreparedInput, error) {
	dir := m.operationDir(a.ID)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return PreparedInput{}, err
	}
	if _, err := m.models.Resolve(run.RuntimeKey); err != nil {
		return PreparedInput{}, err
	}
	if a.Preparation == nil {
		return PreparedInput{}, problem("stale_attempt", "The Attempt has no frozen preparation context.")
	}
	conv := *a.Preparation
	prompt, err := runinput.Conversation(ctx, m.store, run, conv)
	var refs string
	if err == nil {
		refs, err = runinput.PrepareReferences(ctx, m.store, m.files, run, dir)
	}
	defer func() {
		if refs != "" {
			_ = os.RemoveAll(refs)
		}
	}()
	if err != nil {
		return PreparedInput{}, err
	}
	var taskSpec *p.Collaboration
	if run.TaskID != "" {
		task, e := m.store.GetTaskState(ctx, run.TaskID)
		if e != nil {
			return PreparedInput{}, e
		}
		if run.TaskInputProtocol == 1 {
			task.Inputs, e = m.store.TaskBatchInputs(ctx, task.ID, run.TaskInputAfter, run.TaskInputRevision)
		} else {
			task.Inputs, e = m.store.TaskInputs(ctx, task.ID, 0, run.TaskInputRevision)
		}
		if e != nil {
			return PreparedInput{}, e
		}
		prompt = runinput.TaskBrief(task, run) + "\nRoom context:\n" + prompt
		if run.PreviousDraftCommit != "" {
			if refs == "" {
				refs, e = os.MkdirTemp(dir, ".references-")
				if e != nil {
					return PreparedInput{}, e
				}
			}
			if e = m.taskDraftEvidence(ctx, run, refs); e != nil {
				return PreparedInput{}, e
			}
			prompt += "\nThe previous task draft is read-only evidence named previous-task-draft under the reference root announced by this turn's Runner. Reconcile it with the active workspace; do not replace the shared tree wholesale.\n"
		}
		taskSpec = &p.Collaboration{ID: task.ID, Goal: task.Goal, AgentID: run.AgentID, Participants: []p.Participant{}}
		if run.TaskInputProtocol == 1 {
			taskSpec.Inputs = &p.TaskInputScope{Version: 1, After: run.TaskInputAfter, Through: run.TaskInputRevision}
		}
		for _, participant := range task.Participants {
			taskSpec.Participants = append(taskSpec.Participants, p.Participant{ID: participant.Agent.ID, Name: participant.Agent.Name, Access: participant.Agent.Access})
		}
	}
	project, err := writeArtifact(filepath.Join(dir, "project.tar"), func(w io.Writer) error { return m.files.ExportArchive(ctx, run.ResourceID, run.BaselineCommit, w) })
	if err != nil {
		return PreparedInput{}, err
	}
	project.URL = "/api/daemon/operations/" + a.ID + "/input?part=project"
	var references *p.Artifact
	if refs != "" {
		v, e := writeArtifact(filepath.Join(dir, "references.tar"), func(w io.Writer) error { return resources.WriteArchive(ctx, refs, w) })
		if e != nil {
			return PreparedInput{}, e
		}
		v.URL = "/api/daemon/operations/" + a.ID + "/input?part=references"
		references = &v
	}

	spec := &p.ExecutionSpec{Task: taskSpec, RunID: run.ID, AttemptID: a.ID, ResourceID: run.ResourceID, BaselineCommit: run.BaselineCommit, Fence: a.WriterFence, AgentName: run.AgentSnapshot.Name, Instructions: run.AgentSnapshot.Instructions, Access: run.AgentSnapshot.Access, Prompt: prompt, Request: run.Prompt, Environment: run.Environment, Model: run.Model, MaxTokens: run.MaxTokens, MaxSteps: run.MaxSteps, TimeoutSeconds: run.TimeoutSeconds, ConversationID: conv.ID, SessionID: conv.SessionID, Generation: conv.Generation, Resume: conv.Resumable, Project: project, References: references}
	return PreparedInput{spec: spec, inputPath: filepath.Join(dir, "project.tar"), conversation: conv}, nil
}

func (m *Manager) activateInput(ctx context.Context, a storage.Attempt, in *PreparedInput) (storage.Attempt, error) {
	// Retain this exact payload across a failed or uncertain activation reply.
	if len(in.command) == 0 {
		spec := *in.spec
		spec.ModelURL = "/api/daemon/model/" + a.ID
		spec.ModelToken = secret()
		command := p.Command{ID: a.CommandID, Kind: "run.start", OperationID: a.ID, Deadline: a.Deadline, Spec: &spec}
		data, err := json.Marshal(command)
		if err != nil {
			return storage.Attempt{}, err
		}
		in.command = data
		in.modelTokenHash = digest([]byte(spec.ModelToken))
	}
	return m.store.ActivateAttempt(ctx, a.ID, storage.PreparedAttempt{Conversation: in.conversation, InputDigest: in.spec.Project.SHA256, InputPath: in.inputPath, Command: in.command, ModelTokenHash: in.modelTokenHash})
}

// prepare is the synchronous source-probe/focused-fixture adapter. Production
// scheduling uses the bounded worker and activates its result on the scheduler.
func (m *Manager) prepare(ctx context.Context, run storage.Run, a storage.Attempt) error {
	current, err := m.store.GetAttempt(ctx, a.ID)
	if err != nil {
		return err
	}
	if len(current.Command) > 0 {
		return nil
	}
	current, err = m.store.FreezeAttemptPreparation(ctx, a.ID)
	if err != nil {
		return err
	}
	input, err := m.buildInput(ctx, run, current)
	if err != nil {
		return err
	}
	_, err = m.activateInput(ctx, current, &input)
	return err
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
