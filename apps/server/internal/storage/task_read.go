package storage

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/hex"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func taskReadAttempt(ctx context.Context, q *sqlgen.Queries, id, runner string) (Attempt, Run, error) {
	a, err := getAttempt(ctx, q, id)
	if err != nil {
		return a, Run{}, err
	}
	if runner == "" {
		runner = a.RunnerID
	}
	if err = validateAttempt(ctx, q, a, runner, false); err != nil {
		return a, Run{}, err
	}
	if a.Phase != "executing" || remoteExpired(a.Deadline) || len(a.Command) == 0 {
		return a, Run{}, problem("attempt_expired", "Task history is available only during authorized execution.")
	}
	r, err := getRun(ctx, q, a.RunID)
	if err != nil {
		return a, r, err
	}
	if r.TaskID == "" || r.TaskInputProtocol != 1 || r.TaskInputAfter < 0 || r.TaskInputRevision < r.TaskInputAfter {
		return a, r, problem("invalid_task", "This Attempt has no bounded task input capability.")
	}
	return a, r, nil
}

// TaskReadCapability returns a stable private credential only to the owning
// machine. It is never included in commands, journals or tool environments.
func (s *Store) TaskReadCapability(ctx context.Context, id, runner string) (token string, run Run, err error) {
	if runner == "" {
		return "", run, problem("runner_mismatch", "An owning Runner identity is required.")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		_, r, e := taskReadAttempt(ctx, q, id, runner)
		if e != nil {
			return e
		}
		token, e = q.GetAttemptTaskReadToken(ctx, id)
		if e != nil {
			return e
		}
		run = r
		if token == "" {
			var key [32]byte
			if _, e = rand.Read(key[:]); e != nil {
				return e
			}
			token = hex.EncodeToString(key[:])
			return q.SetAttemptTaskReadToken(ctx, sqlgen.SetAttemptTaskReadTokenParams{ID: id, TaskReadToken: token})
		}
		return nil
	})
	return
}

// ReadAttemptTaskInputs binds the credential, current authority and frozen range
// in one read transaction. Reading never charges or advances task coverage.
func (s *Store) ReadAttemptTaskInputs(ctx context.Context, id, token string, after int64, limit int) (page TaskInputPage, err error) {
	if limit < 1 || limit > TaskInputPageLimit {
		return page, problem("invalid_page", "Read from 1 to 20 whole inputs.")
	}
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		_, r, e := taskReadAttempt(ctx, q, id, "")
		if e != nil {
			return e
		}
		stored, e := q.GetAttemptTaskReadToken(ctx, id)
		if e != nil {
			return e
		}
		if len(token) != 64 || len(stored) != 64 || subtle.ConstantTimeCompare([]byte(token), []byte(stored)) != 1 {
			return problem("invalid_capability", "Task history capability is invalid.")
		}
		t, e := getTask(ctx, q, r.TaskID)
		if e != nil {
			return e
		}
		page, e = taskInputsPage(ctx, q, t, after, &r.TaskInputRevision, limit)
		return e
	})
	return
}
