package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func repositoryValue(row sqlgen.GithubRepository) (out orchestration.GitHubRepository, err error) {
	err = json.Unmarshal([]byte(row.Value), &out)
	out.ID, out.Revision, out.Enabled = row.ID, row.Revision, row.Enabled != 0
	return
}

func (s *Store) GitHubRepositories(ctx context.Context) (out []orchestration.GitHubRepository, err error) {
	out = []orchestration.GitHubRepository{}
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := githubActor(ctx, q, false); e != nil {
			return e
		}
		rows, e := q.GitHubRepositories(ctx, actorID(ctx))
		if e != nil {
			return e
		}
		for _, r := range rows {
			v, e := repositoryValue(r)
			if e != nil {
				return e
			}
			out = append(out, v)
		}
		return nil
	})
	return
}

// Configuration hashes include the submitted credential, but no receipt or
// returned value contains token text. Verification and private-file I/O happen
// outside this transaction; only this commit activates a connection.
func (s *Store) SaveGitHubRepository(ctx context.Context, input orchestration.GitHubRepositoryInput, verified orchestration.GitHubRepository, credentialRef string) (out orchestration.GitHubRepository, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := githubActor(ctx, q, false); e != nil {
			return e
		}
		if !validText(input.RequestID, 128, false) {
			return problem("invalid_repository", "A stable request ID is required.")
		}
		raw, _ := json.Marshal(input)
		hash := remoteHash(raw)
		old, e := q.GitHubRepositoryRequest(ctx, sqlgen.GitHubRepositoryRequestParams{OwnerUserID: actorID(ctx), RequestID: input.RequestID})
		if e == nil {
			if old.Cancelled != 0 {
				return problem("request_cancelled", "This repository operation was cancelled.")
			}
			if old.RequestHash != hash {
				return problem("request_conflict", "This request belongs to another connection change.")
			}
			return json.Unmarshal([]byte(old.Result), &out)
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if verified.RepositoryID < 1 || verified.ActorID < 1 || len(credentialRef) != 48 {
			return problem("invalid_repository", "Verify the repository and its private credential first.")
		}
		out = verified
		out.Enabled = input.Enabled
		enabled := int64(0)
		if out.Enabled {
			enabled = 1
		}
		if input.ID == "" {
			rows, e := q.GitHubRepositories(ctx, actorID(ctx))
			if e != nil {
				return e
			}
			if len(rows) >= 32 {
				return problem("repository_limit", "Keep at most 32 repository connections.")
			}
			for _, row := range rows {
				if row.RepositoryID == out.RepositoryID {
					return problem("repository_exists", "Update the existing repository connection.")
				}
			}
			out.ID, out.Revision = newID(), 1
			raw, _ = json.Marshal(out)
			if e = q.InsertGitHubRepository(ctx, sqlgen.InsertGitHubRepositoryParams{ID: out.ID, OwnerUserID: actorID(ctx), RepositoryID: out.RepositoryID, Enabled: enabled, CredentialRef: credentialRef, Value: string(raw)}); e != nil {
				return e
			}
		} else {
			row, e := q.GitHubRepository(ctx, input.ID)
			if e != nil {
				return missing(e, "The repository connection")
			}
			if row.OwnerUserID != actorID(ctx) {
				return problem("forbidden", "Use your own repository connection.")
			}
			if row.Revision != input.Revision {
				return problem("revision_conflict", "The repository connection changed. Reload its current settings.")
			}
			if row.RepositoryID != out.RepositoryID {
				return problem("repository_identity_changed", "Create a new connection for a different repository identity.")
			}
			out.ID, out.Revision = row.ID, row.Revision+1
			raw, _ = json.Marshal(out)
			n, e := q.SaveGitHubRepository(ctx, sqlgen.SaveGitHubRepositoryParams{ID: out.ID, Revision: row.Revision, Enabled: enabled, CredentialRef: credentialRef, Value: string(raw)})
			if e != nil {
				return e
			}
			if n != 1 {
				return problem("revision_conflict", "The repository connection changed.")
			}
		}
		raw, _ = json.Marshal(out)
		if e = q.InsertGitHubRepositoryRequest(ctx, sqlgen.InsertGitHubRepositoryRequestParams{OwnerUserID: actorID(ctx), RequestID: input.RequestID, RequestHash: hash, Result: string(raw)}); e != nil {
			return e
		}
		return event(ctx, q, "github_repository.updated", "", out.ID)
	})
	return
}

func (s *Store) GitHubRepositoryRequest(ctx context.Context, id string) (out orchestration.GitHubRepository, err error) {
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := githubActor(ctx, q, false); e != nil {
			return e
		}
		r, e := q.GitHubRepositoryRequest(ctx, sqlgen.GitHubRepositoryRequestParams{OwnerUserID: actorID(ctx), RequestID: id})
		if e != nil {
			return missing(e, "The saved repository operation")
		}
		if r.Cancelled != 0 {
			return problem("request_cancelled", "This repository operation was cancelled.")
		}
		return json.Unmarshal([]byte(r.Result), &out)
	})
	return
}

// Private configuration resolution is only for the trusted Server adapter.
func (s *Store) GitHubRepositoryCredential(ctx context.Context, id string) (out orchestration.GitHubRepository, ref string, err error) {
	row, e := s.queries().GitHubRepository(ctx, id)
	if e != nil {
		return out, "", missing(e, "The repository connection")
	}
	if row.OwnerUserID != actorID(ctx) {
		return out, "", problem("forbidden", "Use your own repository connection.")
	}
	out, err = repositoryValue(row)
	return out, row.CredentialRef, err
}

func (s *Store) FindGitHubRepositoryRequest(ctx context.Context, input orchestration.GitHubRepositoryInput) (out orchestration.GitHubRepository, found bool, err error) {
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := githubActor(ctx, q, false); e != nil {
			return e
		}
		row, e := q.GitHubRepositoryRequest(ctx, sqlgen.GitHubRepositoryRequestParams{OwnerUserID: actorID(ctx), RequestID: input.RequestID})
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		if row.Cancelled != 0 {
			return problem("request_cancelled", "This repository operation was cancelled.")
		}
		raw, _ := json.Marshal(input)
		if row.RequestHash != remoteHash(raw) {
			return problem("request_conflict", "This request belongs to another repository change.")
		}
		found = true
		return json.Unmarshal([]byte(row.Result), &out)
	})
	return
}

// A cancellation tombstone fences a late original configuration write.
func (s *Store) CancelGitHubRepositoryRequest(ctx context.Context, id string) (out *orchestration.GitHubRepository, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := githubActor(ctx, q, false); e != nil {
			return e
		}
		if !validText(id, 128, false) {
			return problem("invalid_repository", "A stable request ID is required.")
		}
		row, e := q.GitHubRepositoryRequest(ctx, sqlgen.GitHubRepositoryRequestParams{OwnerUserID: actorID(ctx), RequestID: id})
		if e == nil {
			if row.Cancelled != 0 {
				return nil
			}
			out = &orchestration.GitHubRepository{}
			return json.Unmarshal([]byte(row.Result), out)
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		return q.CancelGitHubRepositoryRequest(ctx, sqlgen.CancelGitHubRepositoryRequestParams{OwnerUserID: actorID(ctx), RequestID: id})
	})
	return
}
