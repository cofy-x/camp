package daemon

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

// CleanupJournalEvidence contains no command bodies, credentials or sessions.
// Inspection never initializes, migrates, checkpoints or prunes the journal.
type CleanupJournalEvidence struct {
	Schema      int    `json:"schema"`
	Fingerprint string `json:"fingerprint"`
	Pending     int64  `json:"pending"`
	Intents     int64  `json:"intents"`
}

func InspectCleanupJournal(ctx context.Context, directory string) (CleanupJournalEvidence, error) {
	var evidence CleanupJournalEvidence
	name := filepath.Join(directory, "journal.sqlite")
	for _, file := range []string{name, name + "-wal", name + "-shm"} {
		info, err := os.Lstat(file)
		if os.IsNotExist(err) && file != name {
			continue
		}
		if err != nil {
			return evidence, fmt.Errorf("An existing stopped Runner journal is required for cleanup: %w", err)
		}
		if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || !cleanupJournalOwner(info) {
			return evidence, errors.New("Runner journal files must be private owned regular files")
		}
	}
	u := url.URL{Scheme: "file", Path: name, RawQuery: "mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(1000)"}
	db, err := sql.Open("sqlite", u.String())
	if err != nil {
		return evidence, err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return evidence, err
	}
	defer tx.Rollback()
	var count, maxRow, invalid int64
	var newest string
	err = tx.QueryRowContext(ctx, `SELECT count(*),COALESCE(max(rowid),0),COALESCE(max(created_at),''),COALESCE(sum(state<>'terminal'),0),COALESCE(sum(state NOT IN ('accepted','running','uploading','terminal')),0) FROM commands`).Scan(&count, &maxRow, &newest, &evidence.Pending, &invalid)
	if err != nil {
		return evidence, fmt.Errorf("Runner journal needs reconciliation before cleanup: %w", err)
	}
	if invalid != 0 {
		return evidence, errors.New("Runner journal has unknown operation states")
	}
	if err = tx.QueryRowContext(ctx, "SELECT count(*) FROM coordination_intents").Scan(&evidence.Intents); err != nil {
		return evidence, err
	}
	encoded, _ := json.Marshal([]any{count, maxRow, newest, evidence.Pending, evidence.Intents})
	digest := sha256.Sum256(encoded)
	evidence.Schema = 1
	evidence.Fingerprint = hex.EncodeToString(digest[:])
	return evidence, tx.Commit()
}
