package remoteruns

import (
	"context"
	"encoding/hex"
	"os"
	"path/filepath"
	"time"
)

// Terminal receipts live in SQLite and published bytes live in Git. Transfer
// copies are retained briefly for concurrent handlers, then removed independently
// of a daemon's acknowledgement. A lost response still replays the same receipt.
func (m *Manager) collectTerminalScratch(ctx context.Context) error {
	entries, err := os.ReadDir(filepath.Join(m.config.DataDir, "remote"))
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || len(entry.Name()) != 32 {
			continue
		}
		if _, err := hex.DecodeString(entry.Name()); err != nil {
			continue
		}
		id := entry.Name()
		stamp := ""
		unregisteredResource := ""
		attempt, err := m.store.GetAttempt(ctx, id)
		if err == nil {
			if attempt.Phase != "finished" {
				continue
			}
			stamp = attempt.UpdatedAt
		} else if errorCode(err) == "not_found" {
			item, err := m.store.GetSourceImport(ctx, id)
			if errorCode(err) == "not_found" {
				continue
			}
			if err != nil {
				return err
			}
			if !terminal(item.Status) {
				continue
			}
			stamp = item.UpdatedAt
			if item.Status != "succeeded" {
				unregisteredResource = item.ResourceID
			}
		} else {
			return err
		}
		finished, err := time.Parse(time.RFC3339Nano, stamp)
		if err != nil || time.Since(finished) < time.Minute {
			continue
		}
		if unregisteredResource != "" {
			if _, err = m.store.GetResource(ctx, unregisteredResource); errorCode(err) == "not_found" {
				if err = m.files.DiscardImported(unregisteredResource); err != nil {
					return err
				}
			} else if err != nil {
				return err
			}
		}
		if err = os.RemoveAll(m.operationDir(id)); err != nil {
			return err
		}
	}
	return nil
}
