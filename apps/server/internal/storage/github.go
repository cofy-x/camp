package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cofy-x/camp/apps/server/internal/githubhooks"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func githubSource(row sqlgen.GithubSource) GitHubSource {
	return GitHubSource{ID: row.ID, RoomID: row.RoomID, Repository: row.Repository, RepositoryID: row.RepositoryID, Enabled: row.Enabled != 0, Revision: row.Revision, Mode: "observe", SecretRef: row.SecretRef, CreatedBy: row.CreatedBy, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
}

// GitHubSourceCredential is for the trusted ingress adapter. SecretRef is never
// serialized, and ingress rechecks this exact credential revision in its write.
func (s *Store) GitHubSourceCredential(ctx context.Context, id string) (GitHubSource, error) {
	row, err := s.queries().GetGitHubSource(ctx, id)
	return githubSource(row), missing(err, "The GitHub source")
}

func (s *Store) ListGitHubSources(ctx context.Context, room string) ([]GitHubSource, error) {
	if _, err := s.GetRoom(ctx, room); err != nil {
		return nil, err
	}
	rows, err := s.queries().ListGitHubSources(ctx, room)
	out := make([]GitHubSource, 0, len(rows))
	for _, row := range rows {
		out = append(out, githubSource(row))
	}
	return out, err
}

func githubActor(ctx context.Context, q *sqlgen.Queries, admin bool) error {
	if _, ok := UserFromContext(ctx); !ok {
		return problem("unauthenticated", "Sign in to Camp.")
	}
	return requireActor(ctx, q, admin)
}

func githubReceipt(ctx context.Context, q *sqlgen.Queries, request string, input any) (GitHubReceipt, bool, string, error) {
	if !validText(request, 128, false) {
		return GitHubReceipt{}, false, "", problem("invalid_source", "A stable request ID is required.")
	}
	raw, err := json.Marshal(input)
	if err != nil {
		return GitHubReceipt{}, false, "", err
	}
	hash := remoteHash(raw)
	old, err := q.GetGitHubRequest(ctx, sqlgen.GetGitHubRequestParams{UserID: actorID(ctx), RequestID: request})
	if errors.Is(err, sql.ErrNoRows) {
		return GitHubReceipt{}, false, hash, nil
	}
	if err != nil {
		return GitHubReceipt{}, false, hash, err
	}
	if old.RequestHash != hash {
		return GitHubReceipt{}, false, hash, problem("request_conflict", "This request ID belongs to a different GitHub operation.")
	}
	var result GitHubReceipt
	err = json.Unmarshal([]byte(old.Result), &result)
	return result, true, hash, err
}

func saveGitHubReceipt(ctx context.Context, q *sqlgen.Queries, request, hash string, result GitHubReceipt) error {
	raw, err := json.Marshal(result)
	if err != nil {
		return err
	}
	return q.SaveGitHubRequest(ctx, sqlgen.SaveGitHubRequestParams{UserID: actorID(ctx), RequestID: request, RequestHash: hash, Result: string(raw)})
}

func (s *Store) GitHubRequest(ctx context.Context, request string) (GitHubReceipt, error) {
	if err := githubActor(ctx, s.queries(), false); err != nil {
		return GitHubReceipt{}, err
	}
	row, err := s.queries().GetGitHubRequest(ctx, sqlgen.GetGitHubRequestParams{UserID: actorID(ctx), RequestID: request})
	if err != nil {
		return GitHubReceipt{}, missing(err, "The GitHub operation receipt")
	}
	var result GitHubReceipt
	err = json.Unmarshal([]byte(row.Result), &result)
	return result, err
}

// Resolve a configuration replay before writing another immutable private key.
// The write path repeats receipt and authority checks in its transaction.
func (s *Store) FindGitHubSourceCreation(ctx context.Context, room string, in CreateGitHubSourceInput) (GitHubSource, bool, error) {
	in.Repository = strings.ToLower(strings.TrimSpace(in.Repository))
	if err := githubActor(ctx, s.queries(), true); err != nil {
		return GitHubSource{}, false, err
	}
	old, found, _, err := githubReceipt(ctx, s.queries(), in.RequestID, []any{"source.create", room, in})
	if old.Source != nil {
		return *old.Source, found, err
	}
	return GitHubSource{}, found, err
}

func (s *Store) FindGitHubSourceUpdate(ctx context.Context, id string, in UpdateGitHubSourceInput) (GitHubSource, bool, error) {
	if err := githubActor(ctx, s.queries(), true); err != nil {
		return GitHubSource{}, false, err
	}
	old, found, _, err := githubReceipt(ctx, s.queries(), in.RequestID, []any{"source.update", id, in})
	if old.Source != nil {
		return *old.Source, found, err
	}
	return GitHubSource{}, found, err
}

func (s *Store) CreateGitHubSource(ctx context.Context, room string, in CreateGitHubSourceInput, secretRef string) (out GitHubSource, err error) {
	in.Repository = strings.ToLower(strings.TrimSpace(in.Repository))
	if !githubhooks.RepositoryPattern.MatchString(in.Repository) || strings.HasSuffix(in.Repository, "/.") || strings.HasSuffix(in.Repository, "/..") {
		return out, problem("invalid_source", "Use the GitHub repository name owner/repository.")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := githubActor(ctx, q, true); e != nil {
			return e
		}
		old, replay, hash, e := githubReceipt(ctx, q, in.RequestID, []any{"source.create", room, in})
		if e != nil {
			return e
		}
		if replay {
			out = *old.Source
			return nil
		}
		if _, e = q.GetRoom(ctx, room); e != nil {
			return missing(e, "The room")
		}
		rows, e := q.ListGitHubSources(ctx, room)
		if e != nil {
			return e
		}
		if len(rows) >= 32 {
			return problem("invalid_source", "A Room supports at most 32 GitHub sources.")
		}
		for _, row := range rows {
			if row.Repository == in.Repository && row.Enabled != 0 {
				return problem("source_conflict", "Disable this repository's active source before creating its replacement.")
			}
		}
		if len(secretRef) != 48 {
			return problem("invalid_source", "Save a private webhook credential before creating the source.")
		}
		stamp := now()
		id := newID()
		if e = q.CreateGitHubSource(ctx, sqlgen.CreateGitHubSourceParams{ID: id, RoomID: room, Repository: in.Repository, SecretRef: secretRef, CreatedBy: actorID(ctx), CreatedAt: stamp, UpdatedAt: stamp}); e != nil {
			return e
		}
		row, e := q.GetGitHubSource(ctx, id)
		if e != nil {
			return e
		}
		out = githubSource(row)
		if e = event(ctx, q, "github.source.updated", room, id); e != nil {
			return e
		}
		return saveGitHubReceipt(ctx, q, in.RequestID, hash, GitHubReceipt{Kind: "source.create", Source: &out})
	})
	return out, err
}

func (s *Store) UpdateGitHubSource(ctx context.Context, id string, in UpdateGitHubSourceInput, secretRef string) (out GitHubSource, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := githubActor(ctx, q, true); e != nil {
			return e
		}
		old, replay, hash, e := githubReceipt(ctx, q, in.RequestID, []any{"source.update", id, in})
		if e != nil {
			return e
		}
		if replay {
			out = *old.Source
			return nil
		}
		row, e := q.GetGitHubSource(ctx, id)
		if e != nil {
			return missing(e, "The GitHub source")
		}
		if row.Revision != in.Revision {
			return problem("revision_conflict", "The source changed. Refresh its current settings before saving.")
		}
		if in.Enabled == nil && in.Secret == "" {
			return problem("invalid_source", "Choose a source setting to change.")
		}
		if in.Enabled != nil {
			row.Enabled = 0
			if *in.Enabled {
				others, e := q.ListGitHubSources(ctx, row.RoomID)
				if e != nil {
					return e
				}
				for _, other := range others {
					if other.ID != id && other.Repository == row.Repository && other.Enabled != 0 {
						return problem("source_conflict", "Another source for this repository is enabled. Disable it before enabling this source.")
					}
				}
				row.Enabled = 1
			}
		}
		if in.Secret != "" {
			if len(secretRef) != 48 {
				return problem("invalid_source", "Save a private webhook credential before rotating it.")
			}
			row.SecretRef = secretRef
		}
		if e = q.UpdateGitHubSource(ctx, sqlgen.UpdateGitHubSourceParams{ID: id, Enabled: row.Enabled, SecretRef: row.SecretRef, UpdatedAt: now()}); e != nil {
			return e
		}
		row, e = q.GetGitHubSource(ctx, id)
		if e != nil {
			return e
		}
		out = githubSource(row)
		if e = event(ctx, q, "github.source.updated", out.RoomID, id); e != nil {
			return e
		}
		return saveGitHubReceipt(ctx, q, in.RequestID, hash, GitHubReceipt{Kind: "source.update", Source: &out})
	})
	return out, err
}

func githubEvent(ctx context.Context, q *sqlgen.Queries, id string) (GitHubEvent, error) {
	row, err := q.GetGitHubEvent(ctx, id)
	if err != nil {
		return GitHubEvent{}, missing(err, "The GitHub event")
	}
	var observation githubhooks.Observation
	if err = json.Unmarshal([]byte(row.Value), &observation); err != nil {
		return GitHubEvent{}, err
	}
	return GitHubEvent{Observation: observation, Seq: row.Seq, ID: row.ID, SourceID: row.SourceID, RoomID: row.RoomID, SourceRevision: row.SourceRevision, Fingerprint: row.Fingerprint, Status: row.Status, ReceivedAt: row.ReceivedAt, DecidedBy: row.DecidedBy, DecidedAt: row.DecidedAt, DecisionReason: row.DecisionReason, TaskID: row.TaskID}, nil
}

func (s *Store) GetGitHubEvent(ctx context.Context, id string) (GitHubEvent, error) {
	return githubEvent(ctx, s.queries(), id)
}
func (s *Store) GitHubPayload(ctx context.Context, id string) ([]byte, error) {
	raw, err := s.queries().GetGitHubPayload(ctx, id)
	return raw, missing(err, "The GitHub event")
}

func (s *Store) ReceiveGitHubDelivery(ctx context.Context, id, credential, delivery, kind string, payload []byte) (out GitHubEvent, duplicate bool, err error) {
	if !githubhooks.DeliveryPattern.MatchString(delivery) {
		return out, false, problem("invalid_source", "Use one GitHub delivery GUID.")
	}
	// Parse and hash up to a MiB of immutable signed bytes before acquiring the
	// database connection. The transaction below binds the prepared observation
	// to the current source, credential and pinned repository identity.
	observation, e := githubhooks.ParseDelivery(kind, payload)
	if e != nil {
		return out, false, problem("invalid_source", e.Error())
	}
	fingerprint := githubhooks.PayloadFingerprint(payload)
	raw, e := json.Marshal(observation)
	if e != nil {
		return out, false, e
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		source, e := q.GetGitHubSource(ctx, id)
		if e != nil {
			return missing(e, "The GitHub source")
		}
		if source.Enabled == 0 || source.SecretRef != credential {
			return problem("source_unavailable", "This source is disabled or its credential changed.")
		}
		if !strings.EqualFold(source.Repository, observation.Repository) {
			return problem("invalid_source", "Use a valid JSON delivery from the configured repository.")
		}
		if source.RepositoryID != 0 && source.RepositoryID != observation.RepositoryID {
			return problem("source_conflict", "The repository identity changed. Configure a new source explicitly.")
		}
		previous, e := q.GetGitHubDelivery(ctx, sqlgen.GetGitHubDeliveryParams{SourceID: id, DeliveryID: strings.ToLower(delivery)})
		if e == nil {
			if previous.Fingerprint != fingerprint {
				return problem("request_conflict", "This delivery GUID belongs to different content.")
			}
			duplicate = true
			out, e = githubEvent(ctx, q, previous.EventID)
			if e == nil && out.Kind != kind {
				return problem("request_conflict", "These payload bytes were recorded under a different event type.")
			}
			return e
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		eventID, e := q.GetGitHubFingerprint(ctx, sqlgen.GetGitHubFingerprintParams{SourceID: id, Fingerprint: fingerprint})
		if errors.Is(e, sql.ErrNoRows) {
			eventID = newID()
			disposition := "pending"
			if observation.IgnoreReason != "" {
				disposition = "ignored"
			}
			if e = q.InsertGitHubEvent(ctx, sqlgen.InsertGitHubEventParams{ID: eventID, SourceID: id, RoomID: source.RoomID, SourceRevision: source.Revision, Fingerprint: fingerprint, Disposition: disposition, Value: string(raw), Payload: payload, ReceivedAt: now()}); e != nil {
				return e
			}
			if source.RepositoryID == 0 {
				if e = q.PinGitHubRepository(ctx, sqlgen.PinGitHubRepositoryParams{RepositoryID: observation.RepositoryID, ID: id}); e != nil {
					return e
				}
			}
			if e = event(ctx, q, "github.event.received", source.RoomID, eventID); e != nil {
				return e
			}
			observed, e := githubEvent(ctx, q, eventID)
			if e != nil {
				return e
			}
			if e = recordAutomationGitHubEvent(ctx, tx, observed); e != nil {
				return e
			}
		} else if e != nil {
			return e
		} else {
			duplicate = true
		}
		if e = q.InsertGitHubDelivery(ctx, sqlgen.InsertGitHubDeliveryParams{SourceID: id, DeliveryID: strings.ToLower(delivery), Fingerprint: fingerprint, EventID: eventID}); e != nil {
			return e
		}
		out, e = githubEvent(ctx, q, eventID)
		if e == nil && out.Kind != kind {
			return problem("request_conflict", "These payload bytes were recorded under a different event type.")
		}
		return e
	})
	return out, duplicate, err
}

func (s *Store) GitHubEventsPage(ctx context.Context, room, state string, after int64, through *int64, limit int) (page GitHubEventPage, err error) {
	page.Items = []GitHubEvent{}
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if _, e := q.GetRoom(ctx, room); e != nil {
			return missing(e, "The room")
		}
		frontier, e := q.GitHubEventFrontier(ctx, room)
		if e != nil {
			return e
		}
		page.Through = frontier
		if through != nil {
			page.Through = *through
		}
		if state == "" {
			state = "pending"
		}
		if state != "pending" && state != "all" && state != "accepted" && state != "dismissed" && state != "ignored" {
			return problem("invalid_page", "Choose a valid event state.")
		}
		if after < 0 || page.Through < after || page.Through > frontier || limit < 0 || limit > 20 {
			return problem("invalid_page", "Choose a valid event range and at most 20 events.")
		}
		if limit == 0 {
			limit = 20
		}
		ids, e := q.GitHubEventsPage(ctx, sqlgen.GitHubEventsPageParams{RoomID: room, State: state, AfterSeq: after, ThroughSeq: page.Through, PageLimit: int64(limit + 1)})
		if e != nil {
			return e
		}
		for i, id := range ids {
			if i == limit {
				break
			}
			item, e := githubEvent(ctx, q, id)
			if e != nil {
				return e
			}
			item.Body = ""
			previous := page
			page.Items = append(page.Items, item)
			page.HasMore = i+1 < len(ids)
			page.NextAfter = nil
			if page.HasMore {
				page.NextAfter = &item.Seq
			}
			encoded, e := json.Marshal(page)
			if e != nil {
				return e
			}
			if len(encoded)+1 > 64<<10 {
				page = previous
				if len(page.Items) == 0 {
					return problem("invalid_page", "The event cannot fit in a bounded page.")
				}
				break
			}
		}
		return nil
	})
	return page, err
}

func (s *Store) DismissGitHubEvent(ctx context.Context, id string, in DismissGitHubEventInput) (out GitHubEvent, err error) {
	in.Reason = strings.TrimSpace(in.Reason)
	if !validText(in.Reason, 1024, false) {
		return out, problem("invalid_source", "Provide a dismissal reason of at most 1024 bytes.")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := githubActor(ctx, q, false); e != nil {
			return e
		}
		old, replay, hash, e := githubReceipt(ctx, q, in.RequestID, []any{"event.dismiss", id, in})
		if e != nil {
			return e
		}
		if replay {
			out = *old.Event
			return nil
		}
		out, e = githubEvent(ctx, q, id)
		if e != nil {
			return e
		}
		if out.Status != "pending" {
			return problem("event_decided", "This event already has a decision. Refresh its recorded result.")
		}
		if e = q.DecideGitHubEvent(ctx, sqlgen.DecideGitHubEventParams{EventID: id, Outcome: "dismissed", ActorID: actorID(ctx), Reason: in.Reason, CreatedAt: now()}); e != nil {
			return e
		}
		if e = markAutomationManualDecision(ctx, q, id, ""); e != nil {
			return e
		}
		out, e = githubEvent(ctx, q, id)
		if e != nil {
			return e
		}
		if e = event(ctx, q, "github.event.decided", out.RoomID, id); e != nil {
			return e
		}
		return saveGitHubReceipt(ctx, q, in.RequestID, hash, GitHubReceipt{Kind: "event.dismiss", Event: &out})
	})
	return out, err
}

func pendingGitHubEvent(ctx context.Context, q *sqlgen.Queries, room, id string) (GitHubEvent, error) {
	e, err := githubEvent(ctx, q, id)
	if err != nil {
		return e, err
	}
	if e.RoomID != room {
		return e, problem("invalid_source", "Choose a GitHub event from this Room.")
	}
	if e.Status != "pending" {
		return e, problem("event_decided", "This event already has a decision. Open its recorded result.")
	}
	source, err := q.GetGitHubSource(ctx, e.SourceID)
	if err != nil {
		return e, err
	}
	if source.Enabled == 0 {
		return e, problem("source_unavailable", "Enable this source before confirming its events.")
	}
	return e, nil
}

func acceptGitHubEvent(ctx context.Context, q *sqlgen.Queries, t *CollaborationTask, e GitHubEvent) error {
	// Chunk the complete normalized original through the existing bounded input
	// log. The confirming member supplies this evidence; the GitHub actor is not
	// promoted to a Camp principal, and event text never changes the task grant.
	remaining := e.Observation.Context()
	for part := 1; len(remaining) > 0; part++ {
		chunk := truncateUTF8(remaining, 4096)
		remaining = remaining[len(chunk):]
		content := fmt.Sprintf("Untrusted GitHub context %s, part %d:\n\n%s\n\n[End of GitHub context part %d]", e.ID, part, chunk, part)
		if _, err := addTaskInput(ctx, q, t, content, true); err != nil {
			return err
		}
	}
	if err := q.DecideGitHubEvent(ctx, sqlgen.DecideGitHubEventParams{EventID: e.ID, Outcome: "accepted", ActorID: actorID(ctx), TaskID: nullableParent(t.ID), Reason: "explicit_task_start", CreatedAt: now()}); err != nil {
		return err
	}
	return event(ctx, q, "github.event.decided", t.RoomID, e.ID)
}
