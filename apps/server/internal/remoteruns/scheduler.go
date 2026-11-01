package remoteruns

import (
	"context"
	"encoding/json"
	"time"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
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
	// Store and Server ownership outlive every private file builder.
	defer m.preparationWG.Wait()
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
	// A recoverable activation failure retains its command for retry, while
	// unrelated cancellation, expiry and committed dispatch still reconcile.
	preparationErr := m.advancePreparation(ctx)
	if ctx.Err() != nil {
		return ctx.Err()
	}
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
		if len(a.Command) == 0 && !a.CancelRequested && m.currentPreparation() == nil {
			run, e := m.store.GetRun(ctx, a.RunID)
			if e == nil {
				e = m.startPreparation(ctx, run, a)
			}
			if e != nil {
				if ctx.Err() != nil {
					return ctx.Err()
				}
				if _, err = m.store.InterruptAttempt(ctx, a.ID, "Execution input could not be prepared: "+m.redact(e.Error())); err != nil {
					return err
				}
			}
		}
	}
	queued, err := m.store.ListQueuedRuns(ctx)
	if err != nil {
		return err
	}
	for _, run := range queued {
		if count >= m.config.Concurrency || m.currentPreparation() != nil {
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
			if errorCode(err) == "runner_upgrade_required" || errorCode(err) == "run_busy" || errorCode(err) == "resource_busy" || errorCode(err) == "run_finished" || errorCode(err) == "agent_unavailable" {
				continue
			}
			return err
		}
		if err = m.startPreparation(ctx, claimed, a); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if _, err = m.store.InterruptAttempt(ctx, a.ID, "Execution input could not be prepared: "+m.redact(err.Error())); err != nil {
				return err
			}
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
	return preparationErr
}

func (m *Manager) Cancel(ctx context.Context, id string) (storage.Run, error) {
	run, err := m.store.CancelRemoteRun(ctx, id, "Cancellation requested.")
	if err == nil {
		if run.AttemptID != "" {
			m.stopProxy(run.AttemptID)
			m.stopPreparation(run.AttemptID)
		}
		m.Wake()
	}
	return run, err
}
