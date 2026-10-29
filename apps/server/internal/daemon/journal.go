package daemon

import (
	"context"
	"crypto/sha256"
	"database/sql"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	_ "modernc.org/sqlite"
)

//go:embed schema.sql
var journalSchema string

type journal struct{ db *sql.DB }
type operation struct {
	Command     p.Command
	State       string
	Result      *p.Result
	ArchivePath string
	Cancelled   bool
}

func openJournal(directory string) (*journal, error) {
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	name := filepath.Join(directory, "journal.sqlite")
	f, err := os.OpenFile(name, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	if err = f.Close(); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", name)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err = db.Exec(journalSchema); err != nil {
		db.Close()
		return nil, err
	}
	if _, err = db.Exec("PRAGMA busy_timeout = 5000"); err != nil {
		db.Close()
		return nil, err
	}
	return &journal{db}, nil
}

func (j *journal) accept(ctx context.Context, command p.Command) (bool, error) {
	encoded, err := json.Marshal(command)
	if err != nil {
		return false, err
	}
	sum := sha256.Sum256(encoded)
	digest := hex.EncodeToString(sum[:])
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var previous string
	err = tx.QueryRowContext(ctx, "SELECT digest FROM commands WHERE id=?", command.ID).Scan(&previous)
	if err == nil {
		if previous != digest {
			return false, errors.New("A command ID was reused with different content")
		}
		return false, tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO commands(id,operation_id,digest,body,state,created_at) VALUES(?,?,?,?,'accepted',?)", command.ID, command.OperationID, digest, encoded, time.Now().UTC().Format(time.RFC3339Nano))
	if err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (j *journal) operations(ctx context.Context) ([]operation, error) {
	rows, err := j.db.QueryContext(ctx, "SELECT body,state,result,archive_path,cancelled FROM commands WHERE state<>'terminal' ORDER BY created_at,id")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []operation{}
	for rows.Next() {
		var op operation
		var body, terminal []byte
		if err = rows.Scan(&body, &op.State, &terminal, &op.ArchivePath, &op.Cancelled); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(body, &op.Command); err != nil {
			return nil, err
		}
		if len(terminal) > 0 {
			op.Result = &p.Result{}
			if err = json.Unmarshal(terminal, op.Result); err != nil {
				return nil, err
			}
		}
		result = append(result, op)
	}
	return result, rows.Err()
}

func (j *journal) begin(ctx context.Context, id string) (bool, error) {
	result, err := j.db.ExecContext(ctx, "UPDATE commands SET state='running' WHERE operation_id=? AND state='accepted' AND cancelled=0", id)
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}
func (j *journal) cancel(ctx context.Context, id string) error {
	_, err := j.db.ExecContext(ctx, "UPDATE commands SET cancelled=1 WHERE operation_id=? AND state<>'terminal'", id)
	return err
}
func (j *journal) finish(ctx context.Context, id string, result p.Result, archive string) error {
	encoded, err := json.Marshal(result)
	if err != nil {
		return err
	}
	_, err = j.db.ExecContext(ctx, "UPDATE commands SET state='uploading',result=?,archive_path=? WHERE operation_id=? AND state IN ('accepted','running')", encoded, archive, id)
	return err
}
func (j *journal) receipt(ctx context.Context, id string, receipt p.Receipt) error {
	encoded, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var state string
	var body, resultBytes []byte
	err = tx.QueryRowContext(ctx, "SELECT state,body,result FROM commands WHERE operation_id=?", id).Scan(&state, &body, &resultBytes)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if state == "terminal" {
		return tx.Commit()
	}
	if receipt.Disposition == "committed" && receipt.Status == "succeeded" && len(resultBytes) > 0 {
		var command p.Command
		var result p.Result
		if err = json.Unmarshal(body, &command); err != nil {
			return err
		}
		if err = json.Unmarshal(resultBytes, &result); err != nil {
			return err
		}
		if command.Spec != nil && result.Resumable {
			if _, err = tx.ExecContext(ctx, "INSERT INTO metadata(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", "session:"+sessionKey(command.Spec), command.Spec.SessionID); err != nil {
				return err
			}
		}
	}
	// Receipt and native session eligibility are one durable fact. A restart
	// cannot discard a completed outbox entry before preserving its session.
	if _, err = tx.ExecContext(ctx, "UPDATE commands SET state='terminal',receipt=?,body=X'',result=NULL,archive_path='' WHERE operation_id=?", encoded, id); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, "DELETE FROM coordination_intents WHERE operation_id=?", id); err != nil {
		return err
	}
	// Server owns retained progress and publication after its final receipt.
	// Keep the identity and digest forever for deduplication, not copied prompts,
	// expired capabilities, manifests and potentially large event payloads.
	if _, err = tx.ExecContext(ctx, "DELETE FROM events WHERE operation_id=?", id); err != nil {
		return err
	}
	return tx.Commit()
}
func (j *journal) appendEvent(ctx context.Context, id, kind, text string) error {
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var count, total int64
	if err = tx.QueryRowContext(ctx, "SELECT COUNT(*),COALESCE(SUM(length(text)),0) FROM events WHERE operation_id=?", id).Scan(&count, &total); err != nil {
		return err
	}
	// Log pressure stops the execution instead of losing lifecycle observations.
	if count >= 100000 || total+int64(len(text)) > 32<<20 {
		return errors.New("The durable event journal reached its limit")
	}
	_, err = tx.ExecContext(ctx, "INSERT INTO events(operation_id,sequence,kind,text) VALUES(?,COALESCE((SELECT MAX(sequence) FROM events WHERE operation_id=?),0)+1,?,?)", id, id, kind, text)
	if err != nil {
		return err
	}
	return tx.Commit()
}
func (j *journal) events(ctx context.Context) ([]p.Event, error) {
	rows, err := j.db.QueryContext(ctx, "SELECT e.operation_id,e.sequence,e.kind,e.text FROM events e JOIN commands c ON c.operation_id=e.operation_id WHERE e.acknowledged=0 AND c.state<>'terminal' ORDER BY c.created_at,e.sequence LIMIT 128")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []p.Event{}
	for rows.Next() {
		var event p.Event
		if err = rows.Scan(&event.OperationID, &event.Sequence, &event.Kind, &event.Text); err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}
func (j *journal) acknowledge(ctx context.Context, id string, sequence int64) error {
	_, err := j.db.ExecContext(ctx, "UPDATE events SET acknowledged=1 WHERE operation_id=? AND sequence=?", id, sequence)
	return err
}
func (j *journal) pendingEvents(ctx context.Context, id string) (bool, error) {
	var count int
	err := j.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM events WHERE operation_id=? AND acknowledged=0", id).Scan(&count)
	return count > 0, err
}

// saveIntent is called before the runtime tool acknowledges a scheduling choice.
// An intent without a fully retained result is interrupted on restart, not replayed.
func (j *journal) saveIntent(ctx context.Context, id string, intent p.CoordinationIntent) error {
	body, err := json.Marshal(intent)
	if err != nil {
		return err
	}
	tx, err := j.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var state string
	var cancelled bool
	if err = tx.QueryRowContext(ctx, "SELECT state,cancelled FROM commands WHERE operation_id=?", id).Scan(&state, &cancelled); err != nil {
		return err
	}
	if state != "running" || cancelled {
		return errors.New("This operation cannot accept a coordination decision")
	}
	var previous []byte
	err = tx.QueryRowContext(ctx, "SELECT body FROM coordination_intents WHERE operation_id=?", id).Scan(&previous)
	if err == nil {
		if string(previous) != string(body) {
			return errors.New("A different coordination decision was already saved")
		}
		return tx.Commit()
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO coordination_intents(operation_id,body) VALUES(?,?)", id, body); err != nil {
		return err
	}
	return tx.Commit()
}
