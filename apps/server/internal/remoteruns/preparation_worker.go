package remoteruns

import (
	"context"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type preparationJob struct {
	run     storage.Run
	attempt storage.Attempt
	cancel  context.CancelFunc
	result  *preparationOutcome // scheduler-owned, after the builder returns
}

type preparationOutcome struct {
	job   *preparationJob
	input PreparedInput
	err   error
}

func (m *Manager) currentPreparation() *preparationJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.preparing
}

func (m *Manager) stopPreparation(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.preparing != nil && m.preparing.attempt.ID == id {
		m.preparing.cancel()
	}
}

func (m *Manager) stopTaskPreparation(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.preparing != nil && m.preparing.run.TaskID == id {
		m.preparing.cancel()
	}
}

// Admission and result activation stay on the scheduler. There is one worker,
// no in-memory backlog and no Attempt/lease reserved for waiting queued Runs.
func (m *Manager) startPreparation(ctx context.Context, run storage.Run, a storage.Attempt) error {
	if m.currentPreparation() != nil {
		return problem("run_busy", "Input preparation is occupied.")
	}
	var err error
	a, err = m.store.FreezeAttemptPreparation(ctx, a.ID)
	if err != nil {
		return err
	}
	lease, err := time.Parse(time.RFC3339Nano, a.LeaseUntil)
	if err != nil {
		return err
	}
	deadline, err := time.Parse(time.RFC3339Nano, a.Deadline)
	if err != nil {
		return err
	}
	if deadline.Before(lease) {
		lease = deadline
	}
	life, cancel := context.WithDeadline(m.ctx, lease)
	job := &preparationJob{run: run, attempt: a, cancel: cancel}
	m.mu.Lock()
	if m.closed || m.ctx.Err() != nil {
		m.mu.Unlock()
		cancel()
		return context.Canceled
	}
	m.preparing = job
	m.preparationWG.Add(1)
	m.mu.Unlock()
	build := m.build
	if build == nil {
		build = m.buildInput
	}
	go func() {
		defer m.preparationWG.Done()
		defer cancel()
		input, err := build(life, run, a)
		// The builder (including its cleanup defers) has exited before publishing
		// this outcome. The bounded channel cannot block with a second result.
		m.prepared <- preparationOutcome{job: job, input: input, err: err}
		m.Wake()
	}()
	return nil
}

func (m *Manager) releasePreparation(job *preparationJob) {
	job.cancel()
	m.mu.Lock()
	if m.preparing == job {
		m.preparing = nil
	}
	m.mu.Unlock()
}

// A failed activation keeps its exact prepared command for retry. Unknown SQL
// outcomes are read back before any terminal decision; a committed command is
// never replaced or rebuilt with a new capability.
func (m *Manager) advancePreparation(ctx context.Context) error {
	job := m.currentPreparation()
	if job == nil {
		return nil
	}
	a, err := m.store.GetAttempt(ctx, job.attempt.ID)
	if err != nil {
		return err
	}
	if a.Phase == "finished" || a.CancelRequested || expired(a.LeaseUntil) || expired(a.Deadline) {
		job.cancel()
	}
	if job.result == nil {
		select {
		case result := <-m.prepared:
			if result.job != job {
				return problem("attempt_conflict", "The preparation result has a different owner.")
			}
			job.result = &result
		default:
			return nil
		}
	}
	if a.Phase == "finished" {
		m.releasePreparation(job)
		return nil
	}
	result := job.result
	if result.err == nil {
		activate := m.activate
		if activate == nil {
			activate = m.activateInput
		}
		_, err = activate(ctx, job.attempt, &result.input)
		if err == nil {
			m.releasePreparation(job)
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		current, readErr := m.store.GetAttempt(ctx, a.ID)
		if readErr != nil {
			return readErr
		}
		if len(current.Command) > 0 {
			// The durable command owns execution, including a lost reply. Never
			// interrupt it merely because this builder did not observe success.
			m.releasePreparation(job)
			if string(current.Command) == string(result.input.command) &&
				current.ModelTokenHash == result.input.modelTokenHash &&
				current.InputDigest == result.input.spec.Project.SHA256 && current.InputPath == result.input.inputPath {
				return nil
			}
			return problem("request_conflict", "The Attempt already retained another prepared command.")
		}
		if current.Phase == "finished" {
			m.releasePreparation(job)
			return nil
		}
		if transient(err) {
			return err
		}
	} else {
		err = result.err
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	if a.CancelRequested {
		_, err = m.store.CancelRemoteRun(ctx, a.RunID, "Cancellation requested during input preparation.")
	} else {
		_, err = m.store.InterruptAttempt(ctx, a.ID, "Execution input could not be prepared: "+m.redact(err.Error()))
	}
	if err != nil {
		return err
	}
	m.releasePreparation(job)
	return nil
}
