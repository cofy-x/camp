package storage

import (
	"context"
	"database/sql"
	"errors"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

// Publication is an immutable receipt. Restoring files creates another receipt.
type Publication struct {
	TaskID         string `json:"taskId,omitempty"`
	Seq            int64  `json:"seq"`
	ID             string `json:"id"`
	ResourceID     string `json:"resourceId"`
	PreviousCommit string `json:"previousCommit"`
	Commit         string `json:"commit"`
	AuthorUserID   string `json:"authorUserId"`
	AuthorName     string `json:"authorName"`
	RunID          string `json:"runId"`
	Kind           string `json:"kind"`
	Path           string `json:"path"`
	ChecksStatus   string `json:"checksStatus"`
	CreatedAt      string `json:"createdAt"`
}

type LibraryResource struct {
	Resource
	Immutable bool       `json:"immutable"`
	Source    *Reference `json:"source"`
}

func publicationRow(r sqlgen.ResourcePublication) Publication {
	return Publication{TaskID: r.TaskID, Seq: r.Seq, ID: r.ID, ResourceID: r.ResourceID, PreviousCommit: r.PreviousCommit, Commit: r.CommitID, AuthorUserID: r.AuthorUserID.String, RunID: r.RunID.String, Kind: r.Kind, Path: r.Path, ChecksStatus: r.ChecksStatus, CreatedAt: r.CreatedAt}
}

func (s *Store) LibraryResources(ctx context.Context) ([]LibraryResource, error) {
	rows, err := s.queries().ListLibraryResources(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]LibraryResource, 0, len(rows))
	for _, r := range rows {
		item := LibraryResource{Resource: resourceRow(r)}
		u, e := s.queries().GetResourceUpload(ctx, r.ID)
		if e == nil {
			ref := uploadReference(u, r.CurrentCommit)
			item.Source = &ref
			item.Immutable = true
			item.Name = u.Name
		} else if !errors.Is(e, sql.ErrNoRows) {
			return nil, e
		} else if imported, err := s.queries().GetResourceSourceImport(ctx, nullableParent(r.ID)); err == nil {
			item.Source = &Reference{Kind: "source", ResourceID: r.ID, Commit: imported.CommitID, Title: imported.SourceAlias, Origin: "runner", SourceRunnerID: imported.RunnerID, SourceImportID: imported.ID, CreatedAt: imported.CreatedAt}
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, err
		}
		out = append(out, item)
	}
	return out, nil
}

func (s *Store) ListPublications(ctx context.Context, id string, before int64) ([]Publication, error) {
	if _, err := s.GetResource(ctx, id); err != nil {
		return nil, err
	}
	rows, err := s.queries().ListResourcePublications(ctx, sqlgen.ListResourcePublicationsParams{ResourceID: id, BeforeSeq: before})
	out := make([]Publication, 0, len(rows))
	for _, r := range rows {
		out = append(out, Publication{TaskID: r.TaskID, Seq: r.Seq, ID: r.ID, ResourceID: r.ResourceID, PreviousCommit: r.PreviousCommit, Commit: r.CommitID, AuthorUserID: r.AuthorUserID.String, AuthorName: r.AuthorName, RunID: r.RunID.String, Kind: r.Kind, Path: r.Path, ChecksStatus: r.ChecksStatus, CreatedAt: r.CreatedAt})
	}
	return out, err
}

func publicationRequest(ctx context.Context, q *sqlgen.Queries, id, requestID, hash string) (Publication, bool, error) {
	u, ok := UserFromContext(ctx)
	if !ok {
		return Publication{}, false, problem("unauthenticated", "Sign in before saving shared files.")
	}
	r, err := q.GetPublicationByRequest(ctx, sqlgen.GetPublicationByRequestParams{ResourceID: id, AuthorUserID: sql.NullString{String: u.ID, Valid: true}, RequestID: requestID})
	if errors.Is(err, sql.ErrNoRows) {
		return Publication{}, false, nil
	}
	if err != nil {
		return Publication{}, false, err
	}
	if r.RequestHash != hash {
		return Publication{}, false, problem("request_conflict", "This request ID already belongs to a different save.")
	}
	p := publicationRow(r)
	p.AuthorName = u.Name
	return p, true, nil
}

func (s *Store) FindPublication(ctx context.Context, id, requestID, hash string) (Publication, bool, error) {
	return publicationRequest(ctx, s.queries(), id, requestID, hash)
}

func (s *Store) WorkingResource(ctx context.Context, id string) (Resource, error) {
	if err := requireWorkingResource(ctx, s.queries(), id); err != nil {
		return Resource{}, err
	}
	return s.GetResource(ctx, id)
}

type PublishInput struct{ ResourceID, ExpectedCommit, Commit, Kind, Path, RequestID, RequestHash string }

// Publish atomically advances the pointer and records its author and event.
// The caller owns the resource writer lease and has already saved immutable bytes.
func (s *Store) Publish(ctx context.Context, in PublishInput) (Publication, error) {
	if !commitValid(in.ExpectedCommit) || !commitValid(in.Commit) || !validText(in.RequestID, 128, false) || len(in.RequestHash) != 64 || (in.Kind != "edit" && in.Kind != "restore") {
		return Publication{}, problem("invalid_resource", "The publication request is invalid.")
	}
	var saved Publication
	err := s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		u, ok := UserFromContext(ctx)
		if !ok {
			return problem("unauthenticated", "Sign in before saving shared files.")
		}
		actor, err := q.GetUser(ctx, u.ID)
		if err != nil || !actor.Enabled {
			return problem("forbidden", "This account can no longer publish files.")
		}
		if previous, found, err := publicationRequest(ctx, q, in.ResourceID, in.RequestID, in.RequestHash); err != nil {
			return err
		} else if found {
			saved = previous
			return nil
		}
		if err := requireWorkingResource(ctx, q, in.ResourceID); err != nil {
			return err
		}
		if err := validatePublicationLease(ctx, q, in.ResourceID); err != nil {
			return err
		}
		stamp := now()
		n, err := q.PublishResource(ctx, sqlgen.PublishResourceParams{ID: in.ResourceID, CurrentCommit: in.Commit, CurrentCommit_2: in.ExpectedCommit, UpdatedAt: stamp})
		if err != nil {
			return err
		}
		if n != 1 {
			return problem("revision_conflict", "Room files changed. Compare the latest version with your draft before saving.")
		}
		id := newID()
		if err := q.CreateResourcePublication(ctx, sqlgen.CreateResourcePublicationParams{ID: id, ResourceID: in.ResourceID, PreviousCommit: in.ExpectedCommit, CommitID: in.Commit, AuthorUserID: sql.NullString{String: actor.ID, Valid: true}, Kind: in.Kind, Path: in.Path, ChecksStatus: "not_run", RequestID: in.RequestID, RequestHash: in.RequestHash, CreatedAt: stamp}); err != nil {
			return err
		}
		r, err := q.GetResourcePublication(ctx, id)
		if err != nil {
			return err
		}
		saved = publicationRow(r)
		saved.AuthorName = actor.Name
		return event(ctx, q, "resource.updated", "", in.ResourceID)
	})
	return saved, err
}
