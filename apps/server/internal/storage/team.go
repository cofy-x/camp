package storage

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

type User struct {
	ID        string `json:"id"`
	Username  string `json:"username"`
	Name      string `json:"name"`
	Avatar    string `json:"avatar"`
	Role      string `json:"role"`
	Enabled   bool   `json:"enabled"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}
type userContextKey struct{}

func WithUser(ctx context.Context, user User) context.Context {
	return context.WithValue(ctx, userContextKey{}, user)
}
func UserFromContext(ctx context.Context) (User, bool) {
	u, ok := ctx.Value(userContextKey{}).(User)
	return u, ok
}
func actorID(ctx context.Context) string { u, _ := UserFromContext(ctx); return u.ID }
func userRow(u sqlgen.User) User {
	return User{ID: u.ID, Username: u.Username, Name: u.Name, Avatar: u.Avatar, Role: u.Role, Enabled: u.Enabled, CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt}
}
func (s *Store) GetUser(ctx context.Context, id string) (User, error) {
	u, e := s.queries().GetUser(ctx, id)
	return userRow(u), missing(e, "The member")
}
func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, e := s.queries().ListUsers(ctx)
	out := make([]User, 0, len(rows))
	for _, u := range rows {
		out = append(out, userRow(u))
	}
	return out, e
}
func (s *Store) LoginUser(ctx context.Context, username string) (User, string, error) {
	u, e := s.queries().GetUserByUsername(ctx, strings.ToLower(strings.TrimSpace(username)))
	return userRow(u), u.PasswordHash, e
}
func (s *Store) SetupRequired(ctx context.Context) (bool, error) {
	n, e := s.queries().CountUsers(ctx)
	return n == 0, e
}
func (s *Store) EnsureSetupToken(ctx context.Context, hash string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		n, e := q.CountUsers(ctx)
		if e != nil {
			return e
		}
		if n != 0 {
			return nil
		}
		old, e := q.GetSetupToken(ctx)
		if errors.Is(e, sql.ErrNoRows) {
			return q.SetSetupToken(ctx, hash)
		}
		if e != nil {
			return e
		}
		if subtle.ConstantTimeCompare([]byte(old), []byte(hash)) != 1 {
			return problem("setup_token_mismatch", "The setup token file does not match this database.")
		}
		return nil
	})
}

var usernamePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{2,31}$`)

func validateNewUser(username, name, passwordHash string) (string, string, error) {
	username = strings.ToLower(strings.TrimSpace(username))
	name = strings.TrimSpace(name)
	if !usernamePattern.MatchString(username) || !validText(name, 80, false) || len(passwordHash) < 32 {
		return "", "", problem("invalid_user", "Use a username of 3–32 lowercase letters, numbers, hyphens or underscores, and a display name of at most 80 bytes.")
	}
	return username, name, nil
}
func createUser(ctx context.Context, q *sqlgen.Queries, username, name, passwordHash, role string) (User, error) {
	username, name, e := validateNewUser(username, name, passwordHash)
	if e != nil {
		return User{}, e
	}
	if _, e = q.GetUserByUsername(ctx, username); e == nil {
		return User{}, problem("username_conflict", "This username is already in use.")
	} else if !errors.Is(e, sql.ErrNoRows) {
		return User{}, e
	}
	if _, e = q.GetAgentByHandle(ctx, username); e == nil {
		return User{}, problem("username_conflict", "This handle belongs to an agent.")
	} else if !errors.Is(e, sql.ErrNoRows) {
		return User{}, e
	}
	stamp := now()
	avatar, e := chooseMemberAvatar(ctx, q)
	if e != nil {
		return User{}, e
	}
	u := User{ID: newID(), Username: username, Name: name, Avatar: avatar, Role: role, Enabled: true, CreatedAt: stamp, UpdatedAt: stamp}
	e = q.CreateUser(ctx, sqlgen.CreateUserParams{ID: u.ID, Username: u.Username, Name: u.Name, Avatar: u.Avatar, Role: u.Role, Enabled: true, PasswordHash: passwordHash, CreatedAt: stamp, UpdatedAt: stamp})
	if e == nil {
		e = event(ctx, q, "team.changed", "", u.ID)
	}
	return u, e
}
func (s *Store) SetupAdmin(ctx context.Context, tokenHash, username, name, passwordHash, sessionHash, expires string, teamNames ...string) (User, error) {
	var user User
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		n, e := q.CountUsers(ctx)
		if e != nil {
			return e
		}
		if n != 0 {
			return problem("setup_complete", "Camp has already been set up.")
		}
		stored, e := q.GetSetupToken(ctx)
		if e != nil || subtle.ConstantTimeCompare([]byte(stored), []byte(tokenHash)) != 1 {
			return problem("invalid_credentials", "The setup token is invalid.")
		}
		user, e = createUser(ctx, q, username, name, passwordHash, "admin")
		if e != nil {
			return e
		}
		if e = q.CreateSession(ctx, sqlgen.CreateSessionParams{TokenHash: sessionHash, UserID: user.ID, CreatedAt: now(), ExpiresAt: expires}); e != nil {
			return e
		}
		name := "Camp"
		if len(teamNames) > 0 && strings.TrimSpace(teamNames[0]) != "" {
			name = strings.TrimSpace(teamNames[0])
		}
		if !validText(name, 120, false) {
			return problem("invalid_team", "Use a team name of at most 120 bytes.")
		}
		if e = q.SaveTeamName(ctx, sqlgen.SaveTeamNameParams{Name: name, UpdatedAt: now()}); e != nil {
			return e
		}
		return q.DeleteSetupToken(ctx)
	})
	return user, err
}
func (s *Store) CreateSession(ctx context.Context, hash, userID, expires string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		u, e := q.GetUser(ctx, userID)
		if e != nil || !u.Enabled {
			return problem("invalid_credentials", "The username or password is incorrect.")
		}
		if e = q.DeleteExpiredSessions(ctx, now()); e != nil {
			return e
		}
		return q.CreateSession(ctx, sqlgen.CreateSessionParams{TokenHash: hash, UserID: userID, CreatedAt: now(), ExpiresAt: expires})
	})
}
func (s *Store) SessionUser(ctx context.Context, hash string) (User, error) {
	session, e := s.queries().GetSession(ctx, hash)
	if errors.Is(e, sql.ErrNoRows) {
		return User{}, problem("unauthenticated", "Sign in to Camp.")
	}
	if e != nil {
		return User{}, e
	}
	expires, e := time.Parse(time.RFC3339Nano, session.ExpiresAt)
	if e != nil || !expires.After(time.Now()) {
		return User{}, problem("unauthenticated", "Sign in to Camp.")
	}
	u, e := s.queries().GetUser(ctx, session.UserID)
	if errors.Is(e, sql.ErrNoRows) || e == nil && !u.Enabled {
		return User{}, problem("unauthenticated", "Sign in to Camp.")
	}
	if e != nil {
		return User{}, e
	}
	return userRow(u), nil
}
func (s *Store) DeleteSession(ctx context.Context, hash string) error {
	return s.queries().DeleteSession(ctx, hash)
}

type Invite struct {
	ID              string `json:"id"`
	CreatedByUserID string `json:"createdByUserId"`
	CreatedAt       string `json:"createdAt"`
	ExpiresAt       string `json:"expiresAt"`
	ConsumedAt      string `json:"consumedAt"`
	RevokedAt       string `json:"revokedAt"`
}

func inviteRow(i sqlgen.Invite) Invite {
	return Invite{i.ID, i.CreatedByUserID, i.CreatedAt, i.ExpiresAt, i.ConsumedAt, i.RevokedAt}
}
func (s *Store) CreateInvite(ctx context.Context, tokenHash string, expires time.Time) (Invite, error) {
	var out Invite
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		u, e := requireCurrentAdmin(ctx, q)
		if e != nil {
			return e
		}
		stamp := now()
		out = Invite{ID: newID(), CreatedByUserID: u.ID, CreatedAt: stamp, ExpiresAt: expires.UTC().Format(time.RFC3339Nano)}
		if e = q.CreateInvite(ctx, sqlgen.CreateInviteParams{ID: out.ID, TokenHash: tokenHash, CreatedByUserID: u.ID, CreatedAt: stamp, ExpiresAt: out.ExpiresAt}); e != nil {
			return e
		}
		return event(ctx, q, "team.changed", "", "default")
	})
	return out, err
}
func requireCurrentAdmin(ctx context.Context, q *sqlgen.Queries) (User, error) {
	u, ok := UserFromContext(ctx)
	if !ok {
		return User{}, problem("forbidden", "An administrator is required.")
	}
	current, e := q.GetUser(ctx, u.ID)
	if e != nil && !errors.Is(e, sql.ErrNoRows) {
		return User{}, e
	}
	if e != nil || !current.Enabled || current.Role != "admin" {
		return User{}, problem("forbidden", "An administrator is required.")
	}
	return userRow(current), nil
}

// Public requests carry an actor, while internal seeding and supervised work do
// not. Revalidate public authority inside the mutation transaction so a slow
// request body cannot outlive an account disable or administrator demotion.
func requireActor(ctx context.Context, q *sqlgen.Queries, admin bool) error {
	u, present := UserFromContext(ctx)
	if !present {
		return nil
	}
	current, err := q.GetUser(ctx, u.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err != nil || !current.Enabled {
		return problem("unauthenticated", "This account is no longer enabled.")
	}
	if admin && current.Role != "admin" {
		return problem("forbidden", "An administrator is required.")
	}
	return nil
}
func (s *Store) ListInvites(ctx context.Context) ([]Invite, error) {
	rows, e := s.queries().ListInvites(ctx)
	out := make([]Invite, 0, len(rows))
	for _, r := range rows {
		out = append(out, inviteRow(r))
	}
	return out, e
}
func (s *Store) RevokeInvite(ctx context.Context, id string) error {
	return s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if _, e := requireCurrentAdmin(ctx, q); e != nil {
			return e
		}
		changed, e := q.RevokeInvite(ctx, sqlgen.RevokeInviteParams{RevokedAt: now(), ID: id})
		if e != nil || changed == 0 {
			return e
		}
		return event(ctx, q, "team.changed", "", "default")
	})
}
func (s *Store) AcceptInvite(ctx context.Context, tokenHash, username, name, passwordHash, sessionHash, expires string) (User, error) {
	var user User
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		i, e := q.GetInviteByToken(ctx, tokenHash)
		if e != nil {
			return problem("invalid_invite", "This invitation is invalid or has expired.")
		}
		expiresAt, parseErr := time.Parse(time.RFC3339Nano, i.ExpiresAt)
		if parseErr != nil || !expiresAt.After(time.Now()) {
			return problem("invalid_invite", "This invitation is invalid or has expired.")
		}
		count, e := q.ConsumeInvite(ctx, sqlgen.ConsumeInviteParams{Now: now(), ID: i.ID})
		if e != nil {
			return e
		}
		if count != 1 {
			return problem("invalid_invite", "This invitation is invalid or has expired.")
		}
		user, e = createUser(ctx, q, username, name, passwordHash, "member")
		if e != nil {
			return e
		}
		return q.CreateSession(ctx, sqlgen.CreateSessionParams{TokenHash: sessionHash, UserID: user.ID, CreatedAt: now(), ExpiresAt: expires})
	})
	return user, err
}
func (s *Store) UpdateUserAccess(ctx context.Context, id string, role *string, enabled *bool) (User, error) {
	var user User
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if _, e := requireCurrentAdmin(ctx, q); e != nil {
			return e
		}
		u, e := q.GetUser(ctx, id)
		if e != nil {
			return missing(e, "The member")
		}
		oldAdmin := u.Role == "admin" && u.Enabled
		if role != nil {
			u.Role = *role
		}
		if enabled != nil {
			u.Enabled = *enabled
		}
		if u.Role != "admin" && u.Role != "member" {
			return problem("invalid_user", "Choose admin or member.")
		}
		if oldAdmin && (!u.Enabled || u.Role != "admin") {
			n, e := q.CountActiveAdmins(ctx)
			if e != nil {
				return e
			}
			if n <= 1 {
				return problem("last_admin", "Keep at least one enabled administrator.")
			}
		}
		u.UpdatedAt = now()
		if e = q.UpdateUserAccess(ctx, sqlgen.UpdateUserAccessParams{ID: id, Role: u.Role, Enabled: u.Enabled, UpdatedAt: u.UpdatedAt}); e != nil {
			return e
		}
		if !u.Enabled {
			if e = q.DeleteUserSessions(ctx, id); e != nil {
				return e
			}
			if e = revokeOwnedExecution(ctx, q, id); e != nil {
				return e
			}
		}
		if !u.Enabled || u.Role != "admin" {
			if e = revokeSourceImports(ctx, q, "The importing member is no longer authorized.", func(item SourceImport) bool {
				if item.RequestedByUserID != id {
					return false
				}
				runner, err := q.GetRunner(ctx, item.RunnerID)
				return !u.Enabled || err != nil || runner.OwnerUserID.String != id
			}); e != nil {
				return e
			}
		}
		user = userRow(u)
		return event(ctx, q, "team.changed", "", id)
	})
	return user, err
}
func (s *Store) ListUserActiveRuns(ctx context.Context, userID string) ([]Run, error) {
	rows, e := s.queries().ListUserActiveRuns(ctx, userID)
	if e != nil {
		return nil, e
	}
	return runsFromSnapshots(rows)
}

type Team struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	UpdatedAt string `json:"updatedAt"`
}

func (s *Store) GetTeam(ctx context.Context) (Team, error) {
	row, e := s.queries().GetTeamProfile(ctx)
	return Team{ID: "default", Name: row.Name, UpdatedAt: row.UpdatedAt}, e
}
func (s *Store) RenameTeam(ctx context.Context, name string) (Team, error) {
	name = strings.TrimSpace(name)
	if !validText(name, 120, false) {
		return Team{}, problem("invalid_team", "Use a team name of at most 120 bytes.")
	}
	result := Team{ID: "default", Name: name, UpdatedAt: now()}
	e := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if _, e := requireCurrentAdmin(ctx, q); e != nil {
			return e
		}
		if e := q.SaveTeamName(ctx, sqlgen.SaveTeamNameParams{Name: name, UpdatedAt: result.UpdatedAt}); e != nil {
			return e
		}
		return event(ctx, q, "team.changed", "", "default")
	})
	return result, e
}

// RecordManagementEvent accepts administrative metadata only. Callers must never
// pass credentials, request bodies, environment values or model output in detail.
func (s *Store) RecordManagementEvent(ctx context.Context, action, objectID, detail string) error {
	if !validText(action, 80, false) || !validText(objectID, 128, false) || !validText(detail, 2048, true) {
		return problem("invalid_event", "The management event is invalid.")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		u, e := requireCurrentAdmin(ctx, q)
		if e != nil {
			return e
		}
		if e = q.CreateManagementEvent(ctx, sqlgen.CreateManagementEventParams{UserID: u.ID, Action: action, ObjectID: objectID, Detail: detail, CreatedAt: now()}); e != nil {
			return e
		}
		return event(ctx, q, "management.updated", "", objectID)
	})
}
