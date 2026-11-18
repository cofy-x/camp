package storage

import (
	"context"
	"database/sql"
	"errors"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

type WorkExecutionIssue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// CheckWorkExecution observes the proposed participants without creating a grant.
// Admission repeats the route, permission and capability checks in its write transaction.
func (s *Store) CheckWorkExecution(ctx context.Context, roomID string, ids []string, automatic bool) (issue *WorkExecutionIssue, err error) {
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := requireActor(ctx, q, false); e != nil {
			return e
		}
		room, e := q.GetRoom(ctx, roomID)
		if e != nil {
			return missing(e, "The room")
		}
		participants, e := taskParticipants(ctx, q, room, ids)
		if e == nil {
			e = workCompletionRunnersCompatible(ctx, q, participants, automatic)
		}
		var blocked *Error
		if errors.As(e, &blocked) && blocked.Code != "invalid_task" {
			issue = &WorkExecutionIssue{Code: blocked.Code, Message: blocked.Message}
			return nil
		}
		return e
	})
	return
}

func workCompletionRunnersCompatible(ctx context.Context, q *sqlgen.Queries, participants []TaskParticipant, automatic bool) error {
	if !automatic {
		return nil
	}
	for _, participant := range participants {
		runner, err := q.GetRunner(ctx, participant.RunnerID)
		if err != nil {
			return err
		}
		if err = workCompletionRunnerCompatible(runner.Capabilities, true); err != nil {
			return taskRunnerError(participant.Agent.Name, err)
		}
	}
	return nil
}
