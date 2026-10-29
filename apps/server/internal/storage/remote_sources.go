package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

type SourceImportInput struct {
	RequestID              string `json:"requestId"`
	RunnerID               string `json:"runnerId"`
	SourceID               string `json:"sourceId"`
	ExpectedSourceRevision string `json:"expectedSourceRevision"`
	RoomName               string `json:"roomName"`
}
type SourceImport struct {
	ID                string `json:"id"`
	RunnerID          string `json:"runnerId"`
	RequestedByUserID string `json:"requestedByUserId"`
	SourceID          string `json:"sourceId"`
	SourceRevision    string `json:"sourceRevision"`
	SourceAlias       string `json:"sourceAlias"`
	Commit            string `json:"commit"`
	RoomName          string `json:"roomName"`
	ResourceID        string `json:"resourceId"`
	RoomID            string `json:"roomId"`
	CommandID         string `json:"commandId"`
	Status            string `json:"status"`
	ExpiresAt         string `json:"expiresAt"`
	CompletionID      string `json:"completionId"`
	ResultDigest      string `json:"resultDigest"`
	ArtifactDigest    string `json:"artifactDigest"`
	ArtifactPath      string `json:"-"`
	Error             string `json:"error"`
	CreatedAt         string `json:"createdAt"`
	UpdatedAt         string `json:"updatedAt"`
}

func sourceImportRow(r sqlgen.SourceImport) SourceImport {
	return SourceImport{ID: r.ID, RunnerID: r.RunnerID, RequestedByUserID: r.RequestedByUserID, SourceID: r.Source, SourceRevision: r.SourceRevision, SourceAlias: r.SourceAlias, Commit: r.CommitID, RoomName: r.RoomName, ResourceID: r.ResourceKey, RoomID: r.RoomID.String, CommandID: r.CommandID, Status: r.Status, ExpiresAt: r.ExpiresAt, CompletionID: r.CompletionID, ResultDigest: r.ResultHash, ArtifactDigest: r.ArtifactDigest, ArtifactPath: r.ArtifactPath, Error: r.Error, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}
func sourceImportActor(ctx context.Context, q *sqlgen.Queries, id, runnerID string) (context.Context, error) {
	row, err := q.GetUser(ctx, id)
	if err != nil {
		return ctx, missing(err, "The importing member")
	}
	actor := userRow(row)
	if !actor.Enabled {
		return ctx, problem("forbidden", "The importing member is no longer authorized.")
	}
	actorCtx := WithUser(ctx, actor)
	runner, err := q.GetRunner(ctx, runnerID)
	if err != nil {
		return ctx, missing(err, "The runner")
	}
	if err = requireObjectOwner(actorCtx, q, runner.OwnerUserID.String); err != nil {
		return ctx, err
	}
	return actorCtx, nil
}
func (s *Store) CreateSourceImport(ctx context.Context, in SourceImportInput) (out SourceImport, err error) {
	in.RoomName = strings.TrimSpace(in.RoomName)
	if !validText(in.RequestID, 128, false) || !handlePattern.MatchString(in.SourceID) || !validText(in.ExpectedSourceRevision, 128, false) || !validText(in.RoomName, 120, false) || strings.ContainsFunc(in.RoomName, unicode.IsControl) {
		return out, problem("invalid_import", "Provide an approved source, revision, room name, and request identity.")
	}
	payload, _ := json.Marshal(in)
	hash := remoteHash(payload)
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		actor, ok := UserFromContext(ctx)
		if !ok {
			return problem("unauthenticated", "Sign in before importing a runner source.")
		}
		if e := requireActor(ctx, q, false); e != nil {
			return e
		}
		old, e := q.GetSourceImportRequest(ctx, sqlgen.GetSourceImportRequestParams{RequestedByUserID: actor.ID, RequestID: in.RequestID})
		if e == nil {
			if old.RequestHash != hash {
				return problem("request_conflict", "This request identity belongs to a different import.")
			}
			out = sourceImportRow(old)
			return nil
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		runner, e := availableRunner(ctx, q, in.RunnerID)
		if e != nil {
			return e
		}
		if e = requireObjectOwner(ctx, q, runner.OwnerUserID.String); e != nil {
			return e
		}
		var sources []RunnerSource
		if e = json.Unmarshal([]byte(runner.Sources), &sources); e != nil {
			return e
		}
		found := false
		sourceAlias := ""
		for _, source := range sources {
			if source.ID == in.SourceID {
				if source.Revision != in.ExpectedSourceRevision {
					return problem("revision_conflict", "The approved source changed. Refresh before importing.")
				}
				if !source.Available {
					return problem("source_unavailable", "The approved source is unavailable.")
				}
				found = true
				sourceAlias = source.Alias
				break
			}
		}
		if !found {
			return problem("source_unavailable", "This source is not approved on the selected runner.")
		}
		rooms, e := q.ListRooms(ctx)
		if e != nil {
			return e
		}
		for _, room := range rooms {
			if strings.EqualFold(room.Name, in.RoomName) {
				return problem("name_conflict", "A room already uses this name. Choose a different name for the imported snapshot.")
			}
		}
		id, stamp := newID(), now()
		if e = q.CreateSourceImport(ctx, sqlgen.CreateSourceImportParams{ID: id, RunnerID: in.RunnerID, RequestedByUserID: actor.ID, RequestID: in.RequestID, RequestHash: hash, Source: in.SourceID, SourceRevision: in.ExpectedSourceRevision, SourceAlias: sourceAlias, RoomName: in.RoomName, ResourceKey: newID(), CommandID: newID(), ExpiresAt: "", CreatedAt: stamp, UpdatedAt: stamp}); e != nil {
			return e
		}
		row, e := q.GetSourceImport(ctx, id)
		if e != nil {
			return e
		}
		out = sourceImportRow(row)
		return event(ctx, q, "source_import.updated", "", id)
	})
	return
}
func (s *Store) GetSourceImport(ctx context.Context, id string) (SourceImport, error) {
	r, e := s.queries().GetSourceImport(ctx, id)
	if e != nil {
		return SourceImport{}, missing(e, "The source import")
	}
	if _, authenticated := UserFromContext(ctx); authenticated {
		runner, err := s.queries().GetRunner(ctx, r.RunnerID)
		if err != nil {
			return SourceImport{}, err
		}
		if err = requireObjectOwner(ctx, s.queries(), runner.OwnerUserID.String); err != nil {
			return SourceImport{}, err
		}
	}
	return sourceImportRow(r), nil
}
func (s *Store) ListSourceImports(ctx context.Context) ([]SourceImport, error) {
	actor, authenticated, err := currentActor(ctx, s.queries())
	if err != nil {
		return nil, err
	}
	rows, e := s.queries().ListSourceImports(ctx)
	out := make([]SourceImport, 0, len(rows))
	for _, r := range rows {
		if authenticated && actor.Role != "admin" {
			runner, err := s.queries().GetRunner(ctx, r.RunnerID)
			if err != nil {
				return nil, err
			}
			if runner.OwnerUserID.String != actor.ID {
				continue
			}
		}
		out = append(out, sourceImportRow(r))
	}
	return out, e
}
func (s *Store) ListActiveSourceImports(ctx context.Context) ([]SourceImport, error) {
	rows, e := s.queries().ListActiveSourceImports(ctx)
	out := make([]SourceImport, 0, len(rows))
	for _, r := range rows {
		out = append(out, sourceImportRow(r))
	}
	return out, e
}
func (s *Store) ClaimSourceImport(ctx context.Context, id, runnerID string, leaseFor time.Duration) (out SourceImport, err error) {
	if !validLeaseDuration(leaseFor) {
		return out, problem("invalid_lease", "The source capture deadline is invalid.")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		row, e := q.GetSourceImport(ctx, id)
		if e != nil {
			return missing(e, "The source import")
		}
		if row.RunnerID != runnerID {
			return problem("runner_mismatch", "This import belongs to another runner.")
		}
		runner, e := availableRunner(ctx, q, runnerID)
		if e != nil {
			return e
		}
		if _, e = sourceImportActor(ctx, q, row.RequestedByUserID, runnerID); e != nil {
			return e
		}
		if row.Status == "capturing" && !remoteExpired(row.ExpiresAt) {
			out = sourceImportRow(row)
			return nil
		}
		if row.Status != "queued" {
			return problem("import_finished", "This import is no longer queued.")
		}
		active, e := q.CountRunnerOperations(ctx, runnerID)
		if e != nil {
			return e
		}
		if active >= runner.Slots {
			return problem("run_busy", "The runner has no available execution slots.")
		}
		row.Status = "capturing"
		row.ExpiresAt = remoteStamp(time.Now().Add(leaseFor))
		row.UpdatedAt = now()
		if e = saveSourceImport(ctx, q, row); e != nil {
			return e
		}
		out = sourceImportRow(row)
		return event(ctx, q, "source_import.updated", "", id)
	})
	return
}
func saveSourceImport(ctx context.Context, q *sqlgen.Queries, r sqlgen.SourceImport) error {
	return q.SaveSourceImport(ctx, sqlgen.SaveSourceImportParams{ID: r.ID, Status: r.Status, ExpiresAt: r.ExpiresAt, RoomID: r.RoomID, ResourceID: r.ResourceID, ArtifactDigest: r.ArtifactDigest, ArtifactPath: r.ArtifactPath, Error: r.Error, UpdatedAt: r.UpdatedAt})
}
func (s *Store) SaveSourceImportCommand(ctx context.Context, id string, command json.RawMessage) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		r, e := q.GetSourceImport(ctx, id)
		if e != nil {
			return missing(e, "The source import")
		}
		if r.Status != "capturing" || remoteExpired(r.ExpiresAt) {
			return problem("import_expired", "The source import is no longer authorized.")
		}
		return createRunnerCommand(ctx, q, r.CommandID, r.RunnerID, id, "source.capture", command)
	})
}
func (s *Store) CancelSourceImport(ctx context.Context, id string) (out SourceImport, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		r, e := q.GetSourceImport(ctx, id)
		if e != nil {
			return missing(e, "The source import")
		}
		runner, err := q.GetRunner(ctx, r.RunnerID)
		if err != nil {
			return err
		}
		if err = requireObjectOwner(ctx, q, runner.OwnerUserID.String); err != nil {
			return err
		}
		out, e = cancelSourceImport(ctx, q, r, "Source import cancelled. Original files were not modified.")
		return e
	})
	return
}

func cancelSourceImport(ctx context.Context, q *sqlgen.Queries, r sqlgen.SourceImport, reason string) (SourceImport, error) {
	if runTerminal(r.Status) || r.Status == "cancelling" {
		return sourceImportRow(r), nil
	}
	_, commandErr := q.GetRunnerCommand(ctx, sqlgen.GetRunnerCommandParams{OperationID: r.ID, Kind: "source.capture"})
	if commandErr != nil && !errors.Is(commandErr, sql.ErrNoRows) {
		return SourceImport{}, commandErr
	}
	if r.Status == "queued" || errors.Is(commandErr, sql.ErrNoRows) {
		r.Status = "cancelled"
		if err := q.AcknowledgeOperationCommands(ctx, r.ID); err != nil {
			return SourceImport{}, err
		}
	} else {
		r.Status = "cancelling"
		commandID := "cancel-" + r.ID
		payload, _ := json.Marshal(map[string]string{"id": commandID, "kind": "source.cancel", "operationId": r.ID, "deadline": r.ExpiresAt})
		if err := createRunnerCommand(ctx, q, commandID, r.RunnerID, r.ID, "source.cancel", payload); err != nil {
			return SourceImport{}, err
		}
	}
	r.Error, r.UpdatedAt = reason, now()
	if err := saveSourceImport(ctx, q, r); err != nil {
		return SourceImport{}, err
	}
	return sourceImportRow(r), event(ctx, q, "source_import.updated", "", r.ID)
}

// Revocation and cancellation share one durable transition. Restoring access
// cannot revive an operation that already lost its capture authority.
func revokeSourceImports(ctx context.Context, q *sqlgen.Queries, reason string, matches func(SourceImport) bool) error {
	rows, err := q.ListActiveSourceImports(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		if matches(sourceImportRow(row)) {
			if _, err := cancelSourceImport(ctx, q, row, reason); err != nil {
				return err
			}
		}
	}
	return nil
}
func (s *Store) FinishSourceImport(ctx context.Context, id, runnerID, completionID, hash string, resource Resource) (out SourceImport, err error) {
	if !validText(completionID, 128, false) || len(hash) != 64 {
		return out, problem("invalid_result", "A completion identity and digest are required.")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		r, e := q.GetSourceImport(ctx, id)
		if e != nil {
			return missing(e, "The source import")
		}
		if r.RunnerID != runnerID {
			return problem("runner_mismatch", "This import belongs to another runner.")
		}
		if r.CompletionID != "" {
			if r.CompletionID != completionID || r.ResultHash != hash {
				return problem("request_conflict", "A different import result was already committed.")
			}
			out = sourceImportRow(r)
			return nil
		}
		if r.Status != "capturing" || remoteExpired(r.ExpiresAt) {
			return problem("import_expired", "The source import is no longer authorized.")
		}
		if _, e = availableRunner(ctx, q, runnerID); e != nil {
			return e
		}
		actorCtx, e := sourceImportActor(ctx, q, r.RequestedByUserID, runnerID)
		if e != nil {
			return e
		}
		if resource.ID != r.ResourceKey || !commitValid(resource.CurrentCommit) {
			return problem("invalid_resource", "The import result does not match the reserved resource.")
		}
		resource.Name = r.RoomName
		resource.SourcePath = ""
		resource.Environment = json.RawMessage(`{}`)
		resource.CreatedAt = now()
		resource.UpdatedAt = resource.CreatedAt
		if e = createResource(actorCtx, q, resource); e != nil {
			return e
		}
		room := Room{ID: newID(), Name: r.RoomName, Description: "Imported from an approved runner source.", ResourceID: resource.ID, CreatedAt: now(), UpdatedAt: now()}
		assistant, agentErr := q.GetAgentByHandle(ctx, "assistant")
		if agentErr != nil && !errors.Is(agentErr, sql.ErrNoRows) {
			return agentErr
		}
		if agentErr == nil && assistant.Enabled && !assistant.OwnerUserID.Valid {
			room.DefaultAgentID = assistant.ID
		}
		if e = insertRoom(actorCtx, q, &room); e != nil {
			return e
		}
		if _, e = q.SetRoomRunner(ctx, sqlgen.SetRoomRunnerParams{ID: room.ID, RunnerID: nullableParent(runnerID), RunnerRevision: 0, UpdatedAt: now()}); e != nil {
			return e
		}
		if e = q.FinishSourceImport(ctx, sqlgen.FinishSourceImportParams{ID: id, Status: "succeeded", RoomID: nullableParent(room.ID), ResourceID: nullableParent(resource.ID), CommitID: resource.CurrentCommit, ArtifactDigest: r.ArtifactDigest, CompletionID: completionID, ResultHash: hash, UpdatedAt: now()}); e != nil {
			return e
		}
		if e = q.AcknowledgeOperationCommands(ctx, id); e != nil {
			return e
		}
		r, e = q.GetSourceImport(ctx, id)
		if e != nil {
			return e
		}
		out = sourceImportRow(r)
		return event(ctx, q, "source_import.updated", room.ID, id)
	})
	return
}
func (s *Store) SaveSourceImportArtifact(ctx context.Context, id, runnerID, digest, path string) error {
	if len(digest) != 64 || !validText(path, 4096, false) {
		return problem("invalid_artifact", "The source archive digest is invalid.")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		r, e := q.GetSourceImport(ctx, id)
		if e != nil {
			return missing(e, "The source import")
		}
		if r.RunnerID != runnerID {
			return problem("runner_mismatch", "This import belongs to another runner.")
		}
		if r.Status != "capturing" || remoteExpired(r.ExpiresAt) {
			return problem("import_expired", "The source import is no longer authorized.")
		}
		if r.ArtifactDigest != "" && (r.ArtifactDigest != digest || r.ArtifactPath != path) {
			return problem("request_conflict", "A different source archive was already received.")
		}
		r.ArtifactDigest = digest
		r.ArtifactPath = path
		r.UpdatedAt = now()
		return saveSourceImport(ctx, q, r)
	})
}
func (s *Store) FailSourceImport(ctx context.Context, id, runnerID, completionID, hash, status, reason string) (out SourceImport, err error) {
	return s.finishSourceFailure(ctx, id, runnerID, completionID, hash, status, reason, true)
}

// CompleteSourceImportFailure records a daemon's outcome and cleanup authority
// together. Server-side expiry uses FailSourceImport without claiming cleanup.
func (s *Store) CompleteSourceImportFailure(ctx context.Context, id, runnerID, completionID, hash, status, reason string, cleanupConfirmed bool) (out SourceImport, err error) {
	if !cleanupConfirmed {
		status, reason = "interrupted", "Runner could not confirm source capture cleanup. Its execution capability is suspended."
	}
	return s.finishSourceFailure(ctx, id, runnerID, completionID, hash, status, reason, cleanupConfirmed)
}

func (s *Store) finishSourceFailure(ctx context.Context, id, runnerID, completionID, hash, status, reason string, cleanupConfirmed bool) (out SourceImport, err error) {
	if status != "failed" && status != "cancelled" && status != "interrupted" || !validText(reason, MaxRunErrorSize, true) || completionID != "" && (!validText(completionID, 128, false) || len(hash) != 64) {
		return out, problem("invalid_result", "The source import outcome is invalid.")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		r, e := q.GetSourceImport(ctx, id)
		if e != nil {
			return missing(e, "The source import")
		}
		if r.RunnerID != runnerID {
			return problem("runner_mismatch", "This import belongs to another runner.")
		}
		if r.CompletionID != "" {
			if r.CompletionID != completionID || r.ResultHash != hash {
				return problem("request_conflict", "A different source result was already recorded.")
			}
			out = sourceImportRow(r)
			return nil
		}
		if runTerminal(r.Status) {
			out = sourceImportRow(r)
			return nil
		}
		if r.Status == "cancelling" && status != "interrupted" {
			status = "cancelled"
			reason = "Source import cancelled. Original files were not modified."
		}
		if e = q.FinishSourceImport(ctx, sqlgen.FinishSourceImportParams{ID: id, Status: status, ArtifactDigest: r.ArtifactDigest, CompletionID: completionID, ResultHash: hash, Error: reason, UpdatedAt: now()}); e != nil {
			return e
		}
		if e = q.AcknowledgeOperationCommands(ctx, id); e != nil {
			return e
		}
		r, e = q.GetSourceImport(ctx, id)
		if e != nil {
			return e
		}
		out = sourceImportRow(r)
		if e = event(ctx, q, "source_import.updated", "", id); e != nil {
			return e
		}
		if !cleanupConfirmed {
			return suspendRunner(ctx, q, runnerID)
		}
		return nil
	})
	return
}

func (s *Store) RecordSourceImportEvent(ctx context.Context, runnerID, id string, seq int64, kind, text, hash string) (replayed bool, err error) {
	if seq < 1 || len(hash) != 64 || !validText(kind, 48, false) || !validText(text, MaxRunEventSize, true) {
		return false, problem("invalid_event", "The source event is invalid.")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		r, e := q.GetSourceImport(ctx, id)
		if e != nil {
			return missing(e, "The source import")
		}
		if r.RunnerID != runnerID {
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
		if r.Status != "capturing" && r.Status != "uploading" && r.Status != "cancelling" || remoteExpired(r.ExpiresAt) {
			return problem("import_expired", "The source capture authorization expired.")
		}
		next, e := q.NextRunnerEventSequence(ctx, id)
		if e != nil {
			return e
		}
		if seq != next {
			return problem("event_sequence_gap", "Replay earlier events before advancing the event sequence.")
		}
		if e = q.CreateRunnerEventReceipt(ctx, sqlgen.CreateRunnerEventReceiptParams{OperationID: id, Sequence: seq, PayloadHash: hash, Kind: kind, Text: text, CreatedAt: now()}); e != nil {
			return e
		}
		return event(ctx, q, "source_import.updated", "", id)
	})
	return
}
