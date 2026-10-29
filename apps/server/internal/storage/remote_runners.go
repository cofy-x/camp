package storage

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

// RunnerSource exposes only the administrator-approved alias, never host paths.
type RunnerSource struct {
	ID        string `json:"id"`
	Alias     string `json:"alias"`
	Revision  string `json:"revision"`
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
}

type Runner struct {
	OwnerUserID          string          `json:"ownerUserId"`
	TeamShared           bool            `json:"teamShared"`
	ID                   string          `json:"id"`
	Name                 string          `json:"name"`
	Enabled              bool            `json:"enabled"`
	Revoked              bool            `json:"revoked"`
	StorageID            string          `json:"storageId"`
	Runtime              string          `json:"runtime"`
	Slots                int             `json:"slots"`
	Capabilities         json.RawMessage `json:"capabilities"`
	Sources              []RunnerSource  `json:"sources"`
	ConnectionGeneration int64           `json:"connectionGeneration"`
	ConnectedAt          string          `json:"connectedAt"`
	LastSeenAt           string          `json:"lastSeenAt"`
	CreatedAt            string          `json:"createdAt"`
	UpdatedAt            string          `json:"updatedAt"`
}

type RunnerEnrollment struct {
	OwnerUserID string `json:"ownerUserId"`
	ID          string `json:"id"`
	Name        string `json:"name"`
	ExpiresAt   string `json:"expiresAt"`
	Revoked     bool   `json:"revoked"`
	RunnerID    string `json:"runnerId"`
	CreatedAt   string `json:"createdAt"`
}

type JoinRunnerInput struct {
	Token          string `json:"token"`
	RegistrationID string `json:"registrationId"`
	Credential     string `json:"credential"`
	Name           string `json:"name"`
	StorageID      string `json:"storageId"`
}

type RunnerConnection struct {
	StorageID    string          `json:"storageId"`
	Runtime      string          `json:"runtime"`
	Slots        int             `json:"slots"`
	Capabilities json.RawMessage `json:"capabilities"`
	Sources      []RunnerSource  `json:"sources"`
}

func remoteStamp(t time.Time) string { return t.UTC().Format("2006-01-02T15:04:05.000000000Z") }
func remoteHash(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
func remoteSecret() string {
	var secret [32]byte
	_, _ = rand.Read(secret[:])
	return base64.RawURLEncoding.EncodeToString(secret[:])
}
func remoteExpired(value string) bool {
	t, err := time.Parse(time.RFC3339Nano, value)
	return err != nil || !time.Now().Before(t)
}
func boolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
func runnerRow(row sqlgen.Runner) (Runner, error) {
	r := Runner{OwnerUserID: row.OwnerUserID.String, TeamShared: row.TeamShared != 0, ID: row.ID, Name: row.Name, Enabled: row.Enabled != 0, Revoked: row.Revoked != 0, StorageID: row.StorageID, Runtime: row.Runtime, Slots: int(row.Slots), Capabilities: json.RawMessage(row.Capabilities), Sources: []RunnerSource{}, ConnectionGeneration: row.ConnectionGeneration, ConnectedAt: row.ConnectedAt, LastSeenAt: row.LastSeenAt, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	err := json.Unmarshal([]byte(row.Sources), &r.Sources)
	return r, err
}
func enrollmentRow(r sqlgen.RunnerEnrollment) RunnerEnrollment {
	return RunnerEnrollment{OwnerUserID: r.OwnerUserID.String, ID: r.ID, Name: r.Name, ExpiresAt: r.ExpiresAt, Revoked: r.Revoked != 0, RunnerID: r.RunnerID.String, CreatedAt: r.CreatedAt}
}
func availableRunner(ctx context.Context, q *sqlgen.Queries, id string) (sqlgen.Runner, error) {
	r, err := q.GetRunner(ctx, id)
	if err != nil {
		return r, missing(err, "The runner")
	}
	if err := enabledOwner(ctx, q, r.OwnerUserID.String); err != nil {
		return r, err
	}
	if r.Enabled == 0 || r.Revoked != 0 {
		return r, problem("runner_unavailable", "The runner is disabled or revoked.")
	}
	return r, nil
}

// A cleanup failure must suspend execution in the same transaction as its final
// receipt. A lost response or server restart must not leave the runner schedulable.
func suspendRunner(ctx context.Context, q *sqlgen.Queries, id string) error {
	runner, err := q.GetRunner(ctx, id)
	if err != nil {
		return err
	}
	if err = q.UpdateRunner(ctx, sqlgen.UpdateRunnerParams{ID: id, Name: runner.Name, Enabled: 0, Revoked: runner.Revoked, UpdatedAt: now()}); err != nil {
		return err
	}
	const reason = "The assigned runner was suspended because execution cleanup could not be confirmed."
	if err = revokeRuns(ctx, q, reason, func(run Run) bool { return run.RunnerID == id }); err != nil {
		return err
	}
	if err = revokeSourceImports(ctx, q, reason, func(item SourceImport) bool { return item.RunnerID == id }); err != nil {
		return err
	}
	return event(ctx, q, "runner.updated", "", id)
}
func (s *Store) GetRunner(ctx context.Context, id string) (Runner, error) {
	r, err := s.queries().GetRunner(ctx, id)
	if err != nil {
		return Runner{}, missing(err, "The runner")
	}
	return runnerRow(r)
}
func (s *Store) ListRunners(ctx context.Context) ([]Runner, error) {
	rows, err := s.queries().ListRunners(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Runner, 0, len(rows))
	for _, row := range rows {
		r, err := runnerRow(row)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}
func (s *Store) CreateRunnerEnrollment(ctx context.Context, name string, expires time.Time) (out RunnerEnrollment, token string, err error) {
	name = strings.TrimSpace(name)
	if !validText(name, 120, false) || !expires.After(time.Now()) || expires.After(time.Now().Add(24*time.Hour)) {
		return out, "", problem("invalid_runner", "Provide a runner name and an enrollment lifetime of at most 24 hours.")
	}
	token = remoteSecret()
	out = RunnerEnrollment{OwnerUserID: actorID(ctx), ID: newID(), Name: name, ExpiresAt: remoteStamp(expires), CreatedAt: now()}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if err := requireActor(ctx, q, false); err != nil {
			return err
		}
		if err := q.CreateRunnerEnrollment(ctx, sqlgen.CreateRunnerEnrollmentParams{ID: out.ID, Name: name, TokenHash: remoteHash([]byte(token)), ExpiresAt: out.ExpiresAt, CreatedAt: out.CreatedAt}); err != nil {
			return err
		}
		return q.SetEnrollmentOwner(ctx, sqlgen.SetEnrollmentOwnerParams{ID: out.ID, OwnerUserID: nullableParent(out.OwnerUserID)})
	})
	return
}
func (s *Store) ListRunnerEnrollments(ctx context.Context) ([]RunnerEnrollment, error) {
	actor, authenticated, err := currentActor(ctx, s.queries())
	if err != nil {
		return nil, err
	}
	rows, err := s.queries().ListRunnerEnrollments(ctx)
	out := make([]RunnerEnrollment, 0, len(rows))
	for _, r := range rows {
		if authenticated && actor.Role != "admin" && actor.ID != r.OwnerUserID.String {
			continue
		}
		out = append(out, enrollmentRow(r))
	}
	return out, err
}
func (s *Store) RevokeRunnerEnrollment(ctx context.Context, id string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		enrollment, err := q.GetEnrollmentByID(ctx, id)
		if err != nil {
			return missing(err, "The enrollment")
		}
		if err = requireObjectOwner(ctx, q, enrollment.OwnerUserID.String); err != nil {
			return err
		}
		n, err := q.RevokeRunnerEnrollment(ctx, id)
		if err == nil && n == 0 {
			return problem("not_found", "The enrollment does not exist.")
		}
		return err
	})
}

// JoinRunner takes the daemon's pre-generated credential so replaying enrollment
// after a lost response needs no recoverable plaintext secret on the server.
func (s *Store) JoinRunner(ctx context.Context, in JoinRunnerInput) (out Runner, err error) {
	if !validText(in.RegistrationID, 128, false) || !validText(in.Credential, 256, false) || len(in.Credential) < 32 || !validText(in.StorageID, 128, false) || !validText(in.Token, 256, false) {
		return out, problem("invalid_runner", "The registration identity, credential, and storage identity are required.")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		enrollment, err := q.GetRunnerEnrollment(ctx, remoteHash([]byte(in.Token)))
		if errors.Is(err, sql.ErrNoRows) {
			return problem("unauthenticated", "The enrollment token is invalid.")
		}
		if err != nil {
			return err
		}
		if err = enabledOwner(ctx, q, enrollment.OwnerUserID.String); err != nil {
			return err
		}
		if enrollment.Revoked != 0 {
			return problem("unauthenticated", "The enrollment token was revoked.")
		}
		if enrollment.RunnerID.Valid {
			row, err := availableRunner(ctx, q, enrollment.RunnerID.String)
			if err != nil {
				return err
			}
			if enrollment.RegistrationID != in.RegistrationID || row.CredentialHash != remoteHash([]byte(in.Credential)) || row.StorageID != in.StorageID {
				return problem("request_conflict", "This enrollment was already used by another registration.")
			}
			out, err = runnerRow(row)
			return err
		}
		if remoteExpired(enrollment.ExpiresAt) {
			return problem("unauthenticated", "The enrollment token has expired.")
		}
		name := strings.TrimSpace(in.Name)
		if name == "" {
			name = enrollment.Name
		}
		if !validText(name, 120, false) {
			return problem("invalid_runner", "The runner name is invalid.")
		}
		id, stamp := newID(), now()
		if err := q.CreateRunner(ctx, sqlgen.CreateRunnerParams{ID: id, Name: name, CredentialHash: remoteHash([]byte(in.Credential)), StorageID: in.StorageID, CreatedAt: stamp, UpdatedAt: stamp}); err != nil {
			return err
		}
		if err := q.SetRunnerOwnership(ctx, sqlgen.SetRunnerOwnershipParams{ID: id, OwnerUserID: enrollment.OwnerUserID, TeamShared: boolInt(!enrollment.OwnerUserID.Valid)}); err != nil {
			return err
		}
		if enrollment.OwnerUserID.Valid {
			if err := q.InitializePersonalRunnerDefault(ctx, sqlgen.InitializePersonalRunnerDefaultParams{UserID: enrollment.OwnerUserID.String, RunnerID: nullableParent(id)}); err != nil {
				return err
			}
		}
		if err := q.ConsumeRunnerEnrollment(ctx, sqlgen.ConsumeRunnerEnrollmentParams{ID: enrollment.ID, RegistrationID: in.RegistrationID, RunnerID: nullableParent(id)}); err != nil {
			return err
		}
		row, err := q.GetRunner(ctx, id)
		if err != nil {
			return err
		}
		out, err = runnerRow(row)
		if err != nil {
			return err
		}
		return event(ctx, q, "runner.updated", "", id)
	})
	return
}

// AuthenticateRunner verifies identity. Disabled identities may retrieve their
// terminal receipts; execution authority is checked separately by each operation.
func (s *Store) AuthenticateRunner(ctx context.Context, token string) (Runner, error) {
	if len(token) < 32 || len(token) > 256 {
		return Runner{}, problem("unauthenticated", "The runner credential is invalid.")
	}
	r, err := s.queries().GetRunnerByCredential(ctx, remoteHash([]byte(token)))
	if errors.Is(err, sql.ErrNoRows) || err == nil && r.Revoked != 0 {
		return Runner{}, problem("unauthenticated", "The runner credential is invalid or revoked.")
	}
	if err != nil {
		return Runner{}, err
	}
	return runnerRow(r)
}

type RunnerUpdate struct {
	TeamShared *bool   `json:"teamShared"`
	Name       *string `json:"name"`
	Enabled    *bool   `json:"enabled"`
	Revoke     bool    `json:"revoke"`
}

// UpdateRunner applies only supplied fields to the current transactional row.
// A delayed rename must never undo an administrator's concurrent disable.
func (s *Store) UpdateRunner(ctx context.Context, id string, input RunnerUpdate) (out Runner, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		row, err := q.GetRunner(ctx, id)
		if err != nil {
			return missing(err, "The runner")
		}
		if err = requireObjectOwner(ctx, q, row.OwnerUserID.String); err != nil {
			return err
		}
		if err = enabledOwner(ctx, q, row.OwnerUserID.String); err != nil {
			return err
		}
		name, enabled := row.Name, row.Enabled != 0
		if input.Name != nil {
			name = strings.TrimSpace(*input.Name)
			if !validText(name, 120, false) {
				return problem("invalid_runner", "The runner name is invalid.")
			}
		}
		if input.Enabled != nil {
			enabled = *input.Enabled
		}
		revoke := input.Revoke || row.Revoked != 0
		if revoke {
			enabled = false
		}
		if err := q.UpdateRunner(ctx, sqlgen.UpdateRunnerParams{ID: id, Name: name, Enabled: boolInt(enabled), Revoked: boolInt(revoke), UpdatedAt: now()}); err != nil {
			return err
		}
		if input.TeamShared != nil {
			if err := q.SetRunnerOwnership(ctx, sqlgen.SetRunnerOwnershipParams{ID: id, OwnerUserID: row.OwnerUserID, TeamShared: boolInt(*input.TeamShared)}); err != nil {
				return err
			}
			if !*input.TeamShared {
				if err := revokeUnauthorizedRunnerRuns(ctx, q, id); err != nil {
					return err
				}
			}
		}
		if !enabled {
			if err := revokeRuns(ctx, q, "The assigned runner was disabled or revoked.", func(run Run) bool { return run.RunnerID == id }); err != nil {
				return err
			}
			if err := revokeSourceImports(ctx, q, "The assigned runner was disabled or revoked.", func(item SourceImport) bool { return item.RunnerID == id }); err != nil {
				return err
			}
		}
		row, err = q.GetRunner(ctx, id)
		if err != nil {
			return err
		}
		out, err = runnerRow(row)
		if err != nil {
			return err
		}
		return event(ctx, q, "runner.updated", "", id)
	})
	return
}
func (s *Store) ConnectRunner(ctx context.Context, id string, in RunnerConnection) (out Runner, err error) {
	if !validText(in.Runtime, 240, false) || in.Slots < 1 || in.Slots > runnerprotocol.MaxSlots || !objectJSON(in.Capabilities) || len(in.Sources) > runnerprotocol.MaxSources {
		return out, problem("invalid_runner", "The runner capability report is invalid.")
	}
	seen := map[string]bool{}
	for _, source := range in.Sources {
		if !runnerprotocol.ValidSourceID(source.ID) || !validText(source.Alias, 120, false) || !validText(source.Revision, 128, false) || !validText(source.Reason, 4096, true) || seen[source.ID] {
			return out, problem("invalid_runner", "Source identities must be unique lowercase identifiers with valid aliases and revisions.")
		}
		seen[source.ID] = true
	}
	if in.Sources == nil {
		in.Sources = []RunnerSource{}
	}
	sources, _ := json.Marshal(in.Sources)
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		row, err := availableRunner(ctx, q, id)
		if err != nil {
			return err
		}
		if row.StorageID != in.StorageID {
			return problem("runner_storage_changed", "The runner storage identity changed. Register this storage as a new runner.")
		}
		stamp := now()
		if err := q.ConnectRunner(ctx, sqlgen.ConnectRunnerParams{ID: id, Runtime: in.Runtime, Slots: int64(in.Slots), Capabilities: string(in.Capabilities), Sources: string(sources), ConnectedAt: stamp, LastSeenAt: stamp, UpdatedAt: stamp}); err != nil {
			return err
		}
		row, err = q.GetRunner(ctx, id)
		if err != nil {
			return err
		}
		out, err = runnerRow(row)
		if err != nil {
			return err
		}
		return event(ctx, q, "runner.updated", "", id)
	})
	return
}
func (s *Store) TouchRunner(ctx context.Context, id string, generation int64) error {
	n, err := s.queries().TouchRunner(ctx, sqlgen.TouchRunnerParams{ID: id, ConnectionGeneration: generation, LastSeenAt: now()})
	if err == nil && n != 1 {
		return problem("stale_connection", "This runner connection is no longer current.")
	}
	return err
}
func (s *Store) SetRoomRunner(ctx context.Context, room, runner string, revision int64) (out Room, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if err := requireActor(ctx, q, true); err != nil {
			return err
		}
		if _, err := q.GetRoom(ctx, room); err != nil {
			return missing(err, "The room")
		}
		if runner != "" {
			if _, err := availableRunner(ctx, q, runner); err != nil {
				return err
			}
		}
		n, err := q.SetRoomRunner(ctx, sqlgen.SetRoomRunnerParams{ID: room, RunnerID: nullableParent(runner), RunnerRevision: revision, UpdatedAt: now()})
		if err != nil {
			return err
		}
		if n != 1 {
			return problem("revision_conflict", "The room runner changed. Refresh before saving.")
		}
		r, err := q.GetRoom(ctx, room)
		if err != nil {
			return err
		}
		out = roomRow(r)
		return event(ctx, q, "room.updated", room, room)
	})
	return
}
