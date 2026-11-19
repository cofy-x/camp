package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"

	"github.com/cofy-x/camp/apps/server/internal/githubhooks"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

type GitHubRecoveryReceipt struct {
	DeliveryID string `json:"deliveryId"`
	EventID    string `json:"eventId,omitempty"`
	PreparedAt string `json:"preparedAt,omitempty"`
}

type PrepareGitHubRecoveryInput struct {
	Revision   int64  `json:"revision"`
	DeliveryID string `json:"deliveryId"`
}

// GitHubRecoveryReceipts reads the exact provider GUIDs in one snapshot. It
// reports committed Camp receipts independently of the provider's attempt list.
func (s *Store) GitHubRecoveryReceipts(ctx context.Context, id string, deliveries []string) (out []GitHubRecoveryReceipt, err error) {
	if len(deliveries) == 0 || len(deliveries) > 100 {
		return nil, problem("invalid_source", "Choose between 1 and 100 GitHub delivery GUIDs.")
	}
	ids := make([]string, len(deliveries))
	for i, delivery := range deliveries {
		if !githubhooks.DeliveryPattern.MatchString(delivery) {
			return nil, problem("invalid_source", "Use a GitHub delivery GUID.")
		}
		ids[i] = strings.ToLower(delivery)
	}
	raw, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := githubActor(ctx, q, true); e != nil {
			return e
		}
		if _, e := q.GetGitHubSource(ctx, id); e != nil {
			return missing(e, "The GitHub source")
		}
		rows, e := q.GitHubRecoveryDeliveries(ctx, sqlgen.GitHubRecoveryDeliveriesParams{SourceID: id, DeliveryIds: string(raw)})
		if e != nil {
			return e
		}
		out = make([]GitHubRecoveryReceipt, 0, len(rows))
		for _, row := range rows {
			out = append(out, GitHubRecoveryReceipt(row))
		}
		return nil
	})
	return
}

// PrepareGitHubRecovery fences this GUID before an operator asks GitHub to
// redeliver it. A repeat never grants another dispatch attempt. Registered
// historical deliveries stay observation-only across source revision changes.
func (s *Store) PrepareGitHubRecovery(ctx context.Context, id string, in PrepareGitHubRecoveryInput) (prepared bool, err error) {
	if !githubhooks.DeliveryPattern.MatchString(in.DeliveryID) {
		return false, problem("invalid_source", "Use a GitHub delivery GUID.")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := githubActor(ctx, q, true); e != nil {
			return e
		}
		source, e := q.GetGitHubSource(ctx, id)
		if e != nil {
			return missing(e, "The GitHub source")
		}
		if source.Revision != in.Revision {
			return problem("revision_conflict", "The source changed. Refresh its current settings before preparing recovery.")
		}
		if source.Enabled == 0 || source.RepositoryID == 0 {
			return problem("source_unavailable", "Verify the enabled source before preparing recovery.")
		}
		count, e := q.PrepareGitHubRecovery(ctx, sqlgen.PrepareGitHubRecoveryParams{
			SourceID: id, DeliveryID: strings.ToLower(in.DeliveryID), PreparedBy: actorID(ctx), PreparedAt: now(),
		})
		prepared = count == 1
		return e
	})
	return
}
