package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

// RecordRunnerConnectionIssue records an authenticated incompatible connection
// without granting a connection epoch or executing any machine instruction.
func (s *Store) RecordRunnerConnectionIssue(ctx context.Context, id, reason string) error {
	if !validText(reason, 8192, false) {
		return problem("invalid_runner", "A connection issue requires a reason.")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		runner, err := q.GetRunner(ctx, id)
		if err != nil {
			return err
		}
		capabilities := map[string]any{}
		if err = json.Unmarshal([]byte(runner.Capabilities), &capabilities); err != nil {
			return err
		}
		capabilities["ready"] = false
		capabilities["reason"] = reason
		raw, err := json.Marshal(capabilities)
		if err != nil {
			return err
		}
		if err = q.SetRunnerConnectionIssue(ctx, sqlgen.SetRunnerConnectionIssueParams{ID: id, Capabilities: string(raw), UpdatedAt: now()}); err != nil {
			return err
		}
		return event(ctx, q, "runner.updated", "", id)
	})
}
