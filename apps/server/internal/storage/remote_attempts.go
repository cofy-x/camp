package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

type Attempt struct {
	ID                   string          `json:"id"`
	RunID                string          `json:"runId"`
	RunnerID             string          `json:"runnerId"`
	CommandID            string          `json:"commandId"`
	Command              json.RawMessage `json:"-"`
	Phase                string          `json:"phase"`
	LeaseUntil           string          `json:"leaseUntil"`
	Deadline             string          `json:"deadline"`
	FinalizationDeadline string          `json:"finalizationDeadline"`
	WriterFence          int64           `json:"writerFence"`
	ModelTokenHash       string          `json:"-"`
	ModelInFlight        bool            `json:"modelInFlight"`
	CancelRequested      bool            `json:"cancelRequested"`
	ResultDigest         string          `json:"resultDigest"`
	CompletionID         string          `json:"completionId"`
	Receipt              json.RawMessage `json:"receipt"`
	InputDigest          string          `json:"inputDigest"`
	InputPath            string          `json:"-"`
	ArtifactDigest       string          `json:"artifactDigest"`
	ArtifactPath         string          `json:"-"`
	CreatedAt            string          `json:"createdAt"`
	UpdatedAt            string          `json:"updatedAt"`
}
type RunnerCommand struct {
	ID           string          `json:"id"`
	RunnerID     string          `json:"runnerId"`
	OperationID  string          `json:"operationId"`
	Kind         string          `json:"kind"`
	Payload      json.RawMessage `json:"payload"`
	Acknowledged bool            `json:"acknowledged"`
}

func attemptRow(ctx context.Context, q *sqlgen.Queries, r sqlgen.RunAttempt) (Attempt, error) {
	out := Attempt{ID: r.ID, RunID: r.RunID, RunnerID: r.RunnerID, CommandID: r.CommandID, Phase: r.Status, LeaseUntil: r.LeaseExpiresAt, Deadline: r.Deadline, FinalizationDeadline: r.UploadExpiresAt, WriterFence: r.Fence, ModelTokenHash: r.CapabilityHash, CancelRequested: r.CancelRequested != 0, ResultDigest: r.ResultHash, CompletionID: r.ResultID, Receipt: json.RawMessage(r.Result), InputDigest: r.InputDigest, InputPath: r.InputPath, ArtifactDigest: r.ArtifactDigest, ArtifactPath: r.ArtifactPath, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
	out.ModelInFlight = r.ModelInFlight != 0
	command, err := q.GetRunnerCommand(ctx, sqlgen.GetRunnerCommandParams{OperationID: r.ID, Kind: "run.start"})
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Attempt{}, err
	}
	if err == nil {
		out.Command = json.RawMessage(command.Payload)
	}
	return out, nil
}
func getAttempt(ctx context.Context, q *sqlgen.Queries, id string) (Attempt, error) {
	r, err := q.GetAttempt(ctx, id)
	if err != nil {
		return Attempt{}, missing(err, "The execution attempt")
	}
	return attemptRow(ctx, q, r)
}
func (s *Store) GetAttempt(ctx context.Context, id string) (Attempt, error) {
	return getAttempt(ctx, s.queries(), id)
}
func (s *Store) GetRunAttempt(ctx context.Context, id string) (Attempt, error) {
	q := s.queries()
	r, err := q.GetRunAttempt(ctx, id)
	if err != nil {
		return Attempt{}, missing(err, "The execution attempt")
	}
	return attemptRow(ctx, q, r)
}
func (s *Store) ListRunnerAttempts(ctx context.Context, id string) ([]Attempt, error) {
	q := s.queries()
	rows, err := q.ListRunnerAttempts(ctx, id)
	if err != nil {
		return nil, err
	}
	return attemptsRows(ctx, q, rows)
}
func (s *Store) ListActiveAttempts(ctx context.Context) ([]Attempt, error) {
	q := s.queries()
	rows, err := q.ListActiveAttempts(ctx)
	if err != nil {
		return nil, err
	}
	return attemptsRows(ctx, q, rows)
}
func attemptsRows(ctx context.Context, q *sqlgen.Queries, rows []sqlgen.RunAttempt) ([]Attempt, error) {
	out := make([]Attempt, 0, len(rows))
	for _, row := range rows {
		a, err := attemptRow(ctx, q, row)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, nil
}
func validLeaseDuration(d time.Duration) bool { return d > 0 && d <= 5*time.Minute }

// ClaimRemoteRun reserves agent ordering, the durable writer fence, the latest
// file baseline, and one stable attempt before a command can be transmitted.
func (s *Store) ClaimRemoteRun(ctx context.Context, runID, runnerID string, leaseFor time.Duration) (out Run, attempt Attempt, err error) {
	if !validLeaseDuration(leaseFor) {
		return out, attempt, problem("invalid_lease", "The execution lease duration is invalid.")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		runner, e := availableRunner(ctx, q, runnerID)
		if e != nil {
			return e
		}
		r, e := getRun(ctx, q, runID)
		if e != nil {
			return e
		}
		if r.RunnerID != runnerID {
			return problem("runner_mismatch", "This task belongs to another runner.")
		}
		active, e := q.CountRunnerOperations(ctx, runnerID)
		if e != nil {
			return e
		}
		if active >= runner.Slots {
			return problem("run_busy", "The runner has no available execution slots.")
		}
		id := newID()
		deadline := time.Now().Add(time.Duration(r.TimeoutSeconds) * time.Second)
		leaseEnd := time.Now().Add(leaseFor)
		if leaseEnd.After(deadline) {
			leaseEnd = deadline
		}
		fence := int64(0)
		if r.TaskID != "" {
			var e error
			fence, e = reserveTaskAttempt(ctx, q, r, id)
			if e != nil {
				return e
			}
		} else if r.AgentSnapshot.Access == "write" {
			lease, e := acquireResourceLease(ctx, q, r.ResourceID, id, leaseEnd)
			if e != nil {
				return e
			}
			fence = lease.Fence
		}
		out, e = claimRun(ctx, q, runID)
		if e != nil {
			return e
		}
		out.AttemptID, out.WriterFence, out.WaitingReason = id, fence, ""
		out.Executor = runner.Runtime
		if e = saveRun(ctx, q, out); e != nil {
			return e
		}
		stamp := now()
		if e = q.CreateAttempt(ctx, sqlgen.CreateAttemptParams{ID: id, RunID: runID, RunnerID: runnerID, Fence: fence, CommandID: newID(), LeaseExpiresAt: remoteStamp(leaseEnd), Deadline: remoteStamp(deadline), CapabilityHash: remoteHash([]byte(remoteSecret())), CreatedAt: stamp, UpdatedAt: stamp}); e != nil {
			return e
		}
		attempt, e = getAttempt(ctx, q, id)
		return e
	})
	return
}
func createRunnerCommand(ctx context.Context, q *sqlgen.Queries, id, runnerID, operationID, kind string, payload json.RawMessage) error {
	if !validText(id, 128, false) || !validText(kind, 48, false) || len(payload) == 0 || len(payload) > 1<<20 || !json.Valid(payload) {
		return problem("invalid_command", "The runner command is invalid.")
	}
	var identity struct {
		ID          string `json:"id"`
		OperationID string `json:"operationId"`
		Kind        string `json:"kind"`
	}
	if json.Unmarshal(payload, &identity) != nil || identity.ID != id || identity.OperationID != operationID || identity.Kind != kind {
		return problem("invalid_command", "The command envelope does not match its durable identity.")
	}
	hash := remoteHash(payload)
	old, err := q.GetRunnerCommand(ctx, sqlgen.GetRunnerCommandParams{OperationID: operationID, Kind: kind})
	if err == nil {
		if old.ID != id || old.RunnerID != runnerID || old.PayloadHash != hash {
			return problem("request_conflict", "This command identity belongs to different content.")
		}
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	return q.CreateRunnerCommand(ctx, sqlgen.CreateRunnerCommandParams{ID: id, RunnerID: runnerID, OperationID: operationID, Kind: kind, Payload: string(payload), PayloadHash: hash, CreatedAt: now()})
}
func (s *Store) SaveAttemptCommand(ctx context.Context, id string, command json.RawMessage, modelTokenHash string) error {
	if len(modelTokenHash) != 64 {
		return problem("invalid_command", "A model capability hash is required.")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		a, err := getAttempt(ctx, q, id)
		if err != nil {
			return err
		}
		if len(a.Command) > 0 {
			if a.ModelTokenHash != modelTokenHash || string(a.Command) != string(command) {
				return problem("request_conflict", "The execution command was already prepared differently.")
			}
			return nil
		}
		if err := validateAttempt(ctx, q, a, a.RunnerID, false); err != nil {
			return err
		}
		if a.Phase != "dispatching" {
			return problem("attempt_conflict", "The execution command cannot be changed after acceptance.")
		}
		if err := createRunnerCommand(ctx, q, a.CommandID, a.RunnerID, a.ID, "run.start", command); err != nil {
			return err
		}
		return q.SetAttemptCapability(ctx, sqlgen.SetAttemptCapabilityParams{ID: id, CapabilityHash: modelTokenHash})
	})
}
func (s *Store) ListRunnerCommands(ctx context.Context, id string) ([]RunnerCommand, error) {
	rows, err := s.queries().ListRunnerCommands(ctx, id)
	out := make([]RunnerCommand, 0, len(rows))
	for _, r := range rows {
		out = append(out, RunnerCommand{ID: r.ID, RunnerID: r.RunnerID, OperationID: r.OperationID, Kind: r.Kind, Payload: json.RawMessage(r.Payload), Acknowledged: r.Acknowledged != 0})
	}
	return out, err
}
func (s *Store) AcknowledgeRunnerCommand(ctx context.Context, runnerID, commandID string) error {
	n, err := s.queries().AcknowledgeRunnerCommand(ctx, sqlgen.AcknowledgeRunnerCommandParams{ID: commandID, RunnerID: runnerID})
	if err == nil && n != 1 {
		return problem("not_found", "The runner command does not exist.")
	}
	return err
}
func validateAttempt(ctx context.Context, q *sqlgen.Queries, a Attempt, runnerID string, allowCancelled bool) error {
	if a.RunnerID != runnerID {
		return problem("runner_mismatch", "This execution belongs to another runner.")
	}
	if _, err := availableRunner(ctx, q, runnerID); err != nil {
		return err
	}
	if a.Phase == "finished" {
		return problem("attempt_finished", "This execution attempt has finished.")
	}
	if remoteExpired(a.LeaseUntil) {
		return problem("attempt_expired", "This execution authorization has expired.")
	}
	if !allowCancelled && a.CancelRequested {
		return problem("run_cancelled", "Cancellation was requested before this operation.")
	}
	r, err := getRun(ctx, q, a.RunID)
	if err != nil {
		return err
	}
	if r.AttemptID != a.ID || r.RunnerID != runnerID || runTerminal(r.Status) {
		return problem("stale_attempt", "This execution attempt is no longer current.")
	}
	if !allowCancelled {
		reason, err := completionCancellation(ctx, q, r)
		if err != nil {
			return err
		}
		if reason != "" {
			return problem("run_cancelled", reason)
		}
	}
	if r.TaskID != "" {
		return validateTaskAttempt(ctx, q, r, a, allowCancelled)
	}
	if r.AgentSnapshot.Access == "write" {
		return validateResourceLease(ctx, q, ResourceLease{ResourceID: r.ResourceID, Owner: a.ID, Fence: a.WriterFence})
	}
	return nil
}
func (s *Store) ValidateAttempt(ctx context.Context, id, runnerID string, allowCancelled bool) (out Attempt, err error) {
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		var err error
		out, err = getAttempt(ctx, q, id)
		if err != nil {
			return err
		}
		return validateAttempt(ctx, q, out, runnerID, allowCancelled)
	})
	return
}

// BeginAttemptModel records uncertainty before an upstream request can escape.
// Restart recovery interrupts these attempts instead of replaying model work.
func (s *Store) BeginAttemptModel(ctx context.Context, id, runnerID string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		a, err := getAttempt(ctx, q, id)
		if err != nil {
			return err
		}
		if err = validateAttempt(ctx, q, a, runnerID, false); err != nil {
			return err
		}
		if a.Phase != "executing" || a.ModelInFlight {
			return problem("model_busy", "This attempt cannot start another model request.")
		}
		if err = chargeTaskModel(ctx, q, a.RunID); err != nil {
			return err
		}
		return q.SetAttemptModelInFlight(ctx, sqlgen.SetAttemptModelInFlightParams{ID: id, ModelInFlight: 1})
	})
}
func (s *Store) EndAttemptModel(ctx context.Context, id string) error {
	return s.queries().SetAttemptModelInFlight(ctx, sqlgen.SetAttemptModelInFlightParams{ID: id, ModelInFlight: 0})
}
func (s *Store) RenewAttempt(ctx context.Context, id, runnerID string, leaseFor time.Duration) (out Attempt, err error) {
	if !validLeaseDuration(leaseFor) {
		return out, problem("invalid_lease", "The execution lease duration is invalid.")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		a, e := getAttempt(ctx, q, id)
		if e != nil {
			return e
		}
		if e = validateAttempt(ctx, q, a, runnerID, true); e != nil {
			return e
		}
		limit := a.Deadline
		if a.Phase == "uploading" {
			limit = a.FinalizationDeadline
		}
		deadline, e := time.Parse(time.RFC3339Nano, limit)
		if e != nil {
			return e
		}
		expiry := time.Now().Add(leaseFor)
		if expiry.After(deadline) {
			expiry = deadline
		}
		if !expiry.After(time.Now()) {
			return problem("attempt_expired", "The execution deadline expired.")
		}
		stamp := remoteStamp(expiry)
		if e = q.RenewAttempt(ctx, sqlgen.RenewAttemptParams{ID: id, LeaseExpiresAt: stamp, UpdatedAt: now()}); e != nil {
			return e
		}
		r, e := getRun(ctx, q, a.RunID)
		if e != nil {
			return e
		}
		if r.TaskID == "" && r.AgentSnapshot.Access == "write" {
			n, e := q.RenewResourceLease(ctx, sqlgen.RenewResourceLeaseParams{ResourceID: r.ResourceID, Owner: id, Fence: a.WriterFence, ExpiresAt: stamp, Now: remoteStamp(time.Now())})
			if e != nil {
				return e
			}
			if n != 1 {
				return problem("stale_lease", "The execution writer lease expired.")
			}
		}
		out, e = getAttempt(ctx, q, id)
		return e
	})
	return
}
func (s *Store) AcceptAttempt(ctx context.Context, id, runnerID string) (out Attempt, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		a, e := getAttempt(ctx, q, id)
		if e != nil {
			return e
		}
		if e = validateAttempt(ctx, q, a, runnerID, true); e != nil {
			return e
		}
		if len(a.Command) == 0 {
			return problem("attempt_conflict", "The execution command is not yet prepared.")
		}
		if a.Phase == "dispatching" {
			if e = q.SetAttemptStatus(ctx, sqlgen.SetAttemptStatusParams{ID: id, Status: "executing", UpdatedAt: now()}); e != nil {
				return e
			}
		}
		if _, e = q.AcknowledgeRunnerCommand(ctx, sqlgen.AcknowledgeRunnerCommandParams{ID: a.CommandID, RunnerID: runnerID}); e != nil {
			return e
		}
		out, e = getAttempt(ctx, q, id)
		return e
	})
	return
}
func (s *Store) BeginFinalizing(ctx context.Context, id, runnerID string) (out Attempt, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		a, e := getAttempt(ctx, q, id)
		if e != nil {
			return e
		}
		if e = validateAttempt(ctx, q, a, runnerID, true); e != nil {
			return e
		}
		if a.Phase != "uploading" {
			if e = q.SetAttemptStatus(ctx, sqlgen.SetAttemptStatusParams{ID: id, Status: "uploading", UploadExpiresAt: remoteStamp(time.Now().Add(5 * time.Minute)), UpdatedAt: now()}); e != nil {
				return e
			}
		}
		out, e = getAttempt(ctx, q, id)
		return e
	})
	return
}
func (s *Store) SaveAttemptInput(ctx context.Context, id, digest, path string) error {
	if len(digest) != 64 || !validText(path, 4096, false) {
		return problem("invalid_artifact", "The input artifact is invalid.")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		a, e := getAttempt(ctx, q, id)
		if e != nil {
			return e
		}
		if e = validateAttempt(ctx, q, a, a.RunnerID, false); e != nil {
			return e
		}
		if a.InputDigest != "" && (a.InputDigest != digest || a.InputPath != path) {
			return problem("request_conflict", "The execution input was already frozen.")
		}
		return q.SaveAttemptInput(ctx, sqlgen.SaveAttemptInputParams{ID: id, InputDigest: digest, InputPath: path, UpdatedAt: now()})
	})
}
func (s *Store) SaveAttemptArtifact(ctx context.Context, id, runnerID, digest, path string) error {
	if len(digest) != 64 || !validText(path, 4096, false) {
		return problem("invalid_artifact", "The result artifact is invalid.")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		a, e := getAttempt(ctx, q, id)
		if e != nil {
			return e
		}
		if e = validateAttempt(ctx, q, a, runnerID, true); e != nil {
			return e
		}
		if a.Phase != "uploading" {
			return problem("attempt_conflict", "Execution must finish before an artifact can be uploaded.")
		}
		if a.ArtifactDigest != "" && (a.ArtifactDigest != digest || a.ArtifactPath != path) {
			return problem("request_conflict", "A different result archive was already accepted.")
		}
		return q.SaveAttemptArtifact(ctx, sqlgen.SaveAttemptArtifactParams{ID: id, ArtifactDigest: digest, ArtifactPath: path, UpdatedAt: now()})
	})
}
func (s *Store) RecordAttemptEvent(ctx context.Context, runnerID, id string, seq int64, kind, text, hash string) (replayed bool, err error) {
	if seq < 1 || len(hash) != 64 {
		return false, problem("invalid_event", "The runner event identity is invalid.")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		a, e := getAttempt(ctx, q, id)
		if e != nil {
			return e
		}
		if a.RunnerID != runnerID {
			return problem("runner_mismatch", "This event belongs to another runner.")
		}
		old, e := q.GetRunnerEventReceipt(ctx, sqlgen.GetRunnerEventReceiptParams{OperationID: id, Sequence: seq})
		if e == nil {
			if old.PayloadHash != hash || old.Kind != kind || old.Text != text {
				return problem("request_conflict", "This event sequence belongs to different content.")
			}
			replayed = true
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if e = validateAttempt(ctx, q, a, runnerID, true); e != nil {
			return e
		}
		next, e := q.NextRunnerEventSequence(ctx, id)
		if e != nil {
			return e
		}
		if seq != next {
			return problem("event_sequence_gap", "Replay earlier events before advancing the event sequence.")
		}
		if _, e = appendRunEvent(ctx, q, a.RunID, kind, text); e != nil {
			return e
		}
		if e = q.CreateRunnerEventReceipt(ctx, sqlgen.CreateRunnerEventReceiptParams{OperationID: id, Sequence: seq, PayloadHash: hash, Kind: kind, Text: text, CreatedAt: now()}); e != nil {
			return e
		}
		r, e := getRun(ctx, q, a.RunID)
		if e != nil {
			return e
		}
		if kind == "status" && (text == "running" || text == "checking") && r.Status != "cancelling" {
			if r.Status == "preparing" || text == "checking" {
				r.Status = text
			}
		}
		if kind != "status" {
			r.Output = remoteOutput(r.Output, text)
		}
		r.UpdatedAt = now()
		if e = saveRun(ctx, q, r); e != nil {
			return e
		}
		return event(ctx, q, "run.updated", r.RoomID, r.ID)
	})
	return
}

func remoteOutput(old, addition string) string {
	text := strings.ToValidUTF8(old+addition, "�")
	if len(text) <= MaxRunOutputSize {
		return text
	}
	const marker = "[Earlier output was truncated; retention is limited to 256 KiB]\n"
	return marker + strings.ToValidUTF8(text[len(text)-(MaxRunOutputSize-len(marker)-3):], "�")
}
func (s *Store) CancelRemoteRun(ctx context.Context, id, reason string) (out Run, err error) {
	if !validText(reason, MaxRunErrorSize, false) {
		return out, problem("invalid_run", "A cancellation reason is required.")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		r, e := getRun(ctx, q, id)
		if e != nil {
			return e
		}
		out = r
		if runTerminal(r.Status) {
			return nil
		}
		if user, ok := UserFromContext(ctx); ok {
			if e = requireActor(ctx, q, false); e != nil {
				return e
			}
			current, e := q.GetUser(ctx, user.ID)
			if e != nil {
				return e
			}
			if current.Role != "admin" && r.RequestedByUserID != user.ID {
				return problem("forbidden", "Only the requester or an administrator can cancel this run.")
			}
		}
		out, e = cancelRemoteRun(ctx, q, r, reason)
		return e
	})
	return
}

func cancelRemoteRun(ctx context.Context, q *sqlgen.Queries, r Run, reason string) (out Run, err error) {
	out = r
	if runTerminal(r.Status) {
		return out, nil
	}
	err = func() error {
		var e error
		if r.Status == "cancelling" && r.CancellationReason != "" {
			return nil
		}
		r.CancellationReason = reason
		r.UpdatedAt = now()
		if r.Status == "queued" {
			if e = saveRun(ctx, q, r); e != nil {
				return e
			}
			r.Status = "cancelled"
			out, e = finishRun(ctx, q, r, "", false)
			return e
		}
		r.Status = "cancelling"
		if e = saveRun(ctx, q, r); e != nil {
			return e
		}
		out = r
		a, e := q.GetRunAttempt(ctx, r.ID)
		if e != nil {
			return e
		}
		if e = q.CancelAttempt(ctx, sqlgen.CancelAttemptParams{ID: a.ID, UpdatedAt: now()}); e != nil {
			return e
		}
		_, commandErr := q.GetRunnerCommand(ctx, sqlgen.GetRunnerCommandParams{OperationID: a.ID, Kind: "run.start"})
		if commandErr != nil && !errors.Is(commandErr, sql.ErrNoRows) {
			return commandErr
		}
		if errors.Is(commandErr, sql.ErrNoRows) {
			// No start command has ever been committed, so the daemon cannot
			// have started this attempt and there is nothing to stop remotely.
			r.Status = "cancelled"
			out, e = finishRun(ctx, q, r, "", false)
			if e != nil {
				return e
			}
			receipt, _ := json.Marshal(map[string]string{"disposition": "committed", "runId": r.ID, "status": "cancelled"})
			if e = q.FinishAttempt(ctx, sqlgen.FinishAttemptParams{ID: a.ID, Result: string(receipt), UpdatedAt: now()}); e != nil {
				return e
			}
			if _, e = q.ReleaseResourceLease(ctx, sqlgen.ReleaseResourceLeaseParams{ResourceID: r.ResourceID, Owner: a.ID, Fence: a.Fence}); e != nil {
				return e
			}
			return q.AcknowledgeOperationCommands(ctx, a.ID)
		}
		commandID := "cancel-" + a.ID
		payload, _ := json.Marshal(map[string]string{"id": commandID, "kind": "run.cancel", "operationId": a.ID, "deadline": a.LeaseExpiresAt})
		if e = createRunnerCommand(ctx, q, commandID, a.RunnerID, a.ID, "run.cancel", payload); e != nil {
			return e
		}
		return event(ctx, q, "run.updated", r.RoomID, r.ID)
	}()
	return
}

// FinishRemoteRun commits the receipt in the same transaction as files, terminal
// run state, completion message, and Inbox. Replays are checked before expiry.
func (s *Store) FinishRemoteRun(ctx context.Context, id, runnerID, completionID, hash string, r Run, summary string, resumable, cleanupConfirmed bool) (out Run, err error) {
	if !validText(completionID, 128, false) || len(hash) != 64 {
		return out, problem("invalid_result", "A stable completion identity and digest are required.")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		a, e := getAttempt(ctx, q, id)
		if e != nil {
			return e
		}
		if a.RunnerID != runnerID {
			return problem("runner_mismatch", "This result belongs to another runner.")
		}
		if a.CompletionID != "" {
			if a.CompletionID != completionID || a.ResultDigest != hash {
				return problem("request_conflict", "This execution already has a different completion receipt.")
			}
			out, e = getRun(ctx, q, a.RunID)
			return e
		}
		if e = validateAttempt(ctx, q, a, runnerID, true); e != nil {
			return e
		}
		if r.ID != a.RunID || r.AttemptID != a.ID {
			return problem("stale_attempt", "This result does not identify the current execution attempt.")
		}
		if !cleanupConfirmed {
			r.Status = "interrupted"
			r.Error = "Runner could not confirm execution cleanup. Its execution capability is suspended."
			resumable = false
		}
		// A caller may have read the attempt before cancellation committed.
		// Interrupted means cleanup was unconfirmed and must remain explicit.
		if a.CancelRequested && r.Status != "interrupted" {
			r.Status = "cancelled"
			resumable = false
		}
		out, e = finishRun(ctx, q, r, summary, resumable)
		if e != nil {
			return e
		}
		receipt, _ := json.Marshal(map[string]any{"disposition": "committed", "runId": out.ID, "status": out.Status, "cleanupConfirmed": cleanupConfirmed})
		if e = q.FinishAttempt(ctx, sqlgen.FinishAttemptParams{ID: id, ResultHash: hash, ResultID: completionID, Result: string(receipt), UpdatedAt: now()}); e != nil {
			return e
		}
		if _, e = q.ReleaseResourceLease(ctx, sqlgen.ReleaseResourceLeaseParams{ResourceID: r.ResourceID, Owner: id, Fence: a.WriterFence}); e != nil {
			return e
		}
		if e = q.AcknowledgeOperationCommands(ctx, id); e != nil {
			return e
		}
		if !cleanupConfirmed {
			return suspendRunner(ctx, q, runnerID)
		}
		return nil
	})
	return
}

// InterruptAttempt is the server's terminal recovery path. It never starts a
// replacement attempt and does not require a live runner or unexpired lease.
func (s *Store) InterruptAttempt(ctx context.Context, id, reason string) (out Run, err error) {
	if !validText(reason, MaxRunErrorSize, false) {
		return out, problem("invalid_run", "A recovery reason is required.")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		a, e := getAttempt(ctx, q, id)
		if e != nil {
			return e
		}
		r, e := getRun(ctx, q, a.RunID)
		if e != nil {
			return e
		}
		if a.Phase == "finished" {
			out = r
			return nil
		}
		r.Status = "interrupted"
		r.Error = reason
		out, e = finishRun(ctx, q, r, "", false)
		if e != nil {
			return e
		}
		receipt, _ := json.Marshal(map[string]string{"disposition": "rejected", "runId": r.ID, "status": "interrupted", "reason": reason})
		if e = q.FinishAttempt(ctx, sqlgen.FinishAttemptParams{ID: id, Result: string(receipt), UpdatedAt: now()}); e != nil {
			return e
		}
		if _, e = q.ReleaseResourceLease(ctx, sqlgen.ReleaseResourceLeaseParams{ResourceID: r.ResourceID, Owner: id, Fence: a.WriterFence}); e != nil {
			return e
		}
		return q.AcknowledgeOperationCommands(ctx, id)
	})
	return
}
