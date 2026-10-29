package storage

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

type ResourceLease struct {
	ResourceID string `json:"resourceId"`
	Owner      string `json:"owner"`
	Fence      int64  `json:"fence"`
	ExpiresAt  string `json:"expiresAt"`
}
type resourceLeaseKey struct{}

func WithResourceLease(ctx context.Context, lease ResourceLease) context.Context {
	return context.WithValue(ctx, resourceLeaseKey{}, lease)
}

func resourceLeaseRow(r sqlgen.ResourceWriterLease) ResourceLease {
	return ResourceLease{ResourceID: r.ResourceID, Owner: r.Owner, Fence: r.Fence, ExpiresAt: r.ExpiresAt}
}
func acquireResourceLease(ctx context.Context, q *sqlgen.Queries, resource, owner string, expires time.Time) (ResourceLease, error) {
	if !validText(owner, 240, false) || !expires.After(time.Now()) {
		return ResourceLease{}, problem("invalid_lease", "A writer identity and future expiry are required.")
	}
	if err := requireWorkingResource(ctx, q, resource); err != nil {
		return ResourceLease{}, err
	}
	old, err := q.GetResourceLease(ctx, resource)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return ResourceLease{}, err
	}
	if err == nil && old.Owner != "" && !remoteExpired(old.ExpiresAt) {
		if old.Owner == owner {
			return resourceLeaseRow(old), nil
		}
		return ResourceLease{}, problem("resource_busy", "The shared files have an active writer.")
	}
	lease := ResourceLease{ResourceID: resource, Owner: owner, Fence: old.Fence + 1, ExpiresAt: remoteStamp(expires)}
	err = q.SetResourceLease(ctx, sqlgen.SetResourceLeaseParams{ResourceID: resource, Owner: owner, Fence: lease.Fence, ExpiresAt: lease.ExpiresAt})
	return lease, err
}
func (s *Store) AcquireResourceLease(ctx context.Context, resource, owner string, expires time.Time) (out ResourceLease, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		var err error
		out, err = acquireResourceLease(ctx, sqlgen.New(tx), resource, owner, expires)
		return err
	})
	return
}
func (s *Store) GetResourceLease(ctx context.Context, resource string) (ResourceLease, error) {
	r, err := s.queries().GetResourceLease(ctx, resource)
	return resourceLeaseRow(r), missing(err, "The writer lease")
}
func (s *Store) RenewResourceLease(ctx context.Context, lease ResourceLease, expires time.Time) error {
	if !expires.After(time.Now()) {
		return problem("invalid_lease", "A future expiry is required.")
	}
	n, err := s.queries().RenewResourceLease(ctx, sqlgen.RenewResourceLeaseParams{ResourceID: lease.ResourceID, Owner: lease.Owner, Fence: lease.Fence, ExpiresAt: remoteStamp(expires), Now: remoteStamp(time.Now())})
	if err == nil && n != 1 {
		return problem("stale_lease", "The writer lease is no longer current.")
	}
	return err
}
func (s *Store) ReleaseResourceLease(ctx context.Context, lease ResourceLease) error {
	_, err := s.queries().ReleaseResourceLease(ctx, sqlgen.ReleaseResourceLeaseParams{ResourceID: lease.ResourceID, Owner: lease.Owner, Fence: lease.Fence})
	return err
}
func validateResourceLease(ctx context.Context, q *sqlgen.Queries, lease ResourceLease) error {
	current, err := q.GetResourceLease(ctx, lease.ResourceID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err != nil || current.Owner != lease.Owner || current.Fence != lease.Fence || remoteExpired(current.ExpiresAt) {
		return problem("stale_lease", "The writer lease is no longer current.")
	}
	return nil
}
func validatePublicationLease(ctx context.Context, q *sqlgen.Queries, resource string) error {
	if lease, present := ctx.Value(resourceLeaseKey{}).(ResourceLease); present {
		if lease.ResourceID != resource {
			return problem("stale_lease", "The writer lease belongs to different files.")
		}
		return validateResourceLease(ctx, q, lease)
	}
	// Historical local callers have no durable lease. They may write only when
	// no remote or explicit human writer owns the resource.
	current, err := q.GetResourceLease(ctx, resource)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if current.Owner != "" && !remoteExpired(current.ExpiresAt) {
		return problem("resource_busy", "The shared files have an active writer.")
	}
	return nil
}
