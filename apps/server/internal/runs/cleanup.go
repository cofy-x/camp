package runs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

func (m *Manager) cleanupTerminalScratch(ctx context.Context) error {
	entries, err := os.ReadDir(m.dir)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if !entry.IsDir() || len(entry.Name()) != 32 {
			continue
		}
		run, err := m.store.GetRun(ctx, entry.Name())
		if err != nil {
			continue
		}
		switch run.Status {
		case "succeeded", "failed", "cancelled", "interrupted":
			if err := m.pruneRunScratch(run.ID, run.ResultCommit != "" || run.AgentSnapshot.Access == "read"); err != nil {
				return err
			}
		}
	}
	return nil
}

// Only reserved scratch names are removed, after the inherited execution lock is acquired.
func removeScratch(directory string, nested bool, prefixes ...string) error {
	root, err := os.OpenRoot(directory)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer root.Close()
	file, err := root.Open(".")
	if err != nil {
		return err
	}
	entries, err := file.ReadDir(-1)
	_ = file.Close()
	if err != nil {
		return err
	}
	for _, entry := range entries {
		for _, prefix := range prefixes {
			if strings.HasPrefix(entry.Name(), prefix) {
				if err := root.RemoveAll(entry.Name()); err != nil {
					return err
				}
				break
			}
		}
		if nested && entry.IsDir() && len(entry.Name()) == 32 && !strings.HasPrefix(entry.Name(), ".") {
			if err := removeScratch(filepath.Join(directory, entry.Name()), false, prefixes...); err != nil {
				return err
			}
		}
	}
	return nil
}

func (m *Manager) cleanupScratch() error {
	return errors.Join(removeScratch(m.dir, true, ".verification-", ".inspect-", ".probe-", ".references-"),
		removeScratch(filepath.Join(filepath.Dir(m.dir), "resources"), false, ".import-"),
		removeScratch(filepath.Join(filepath.Dir(m.dir), "environments"), true, ".prepare-"))
}

// Only call after a terminal SQLite commit. The authoritative result and native
// conversation state are separate; an export without a captured Git result is
// retained for diagnosis, and a failed final write keeps all recovery inputs.
func (m *Manager) pruneRunScratch(runID string, captured bool) error {
	root, err := os.OpenRoot(m.dir)
	if err != nil {
		return err
	}
	defer root.Close()
	run, err := root.OpenRoot(runID)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer run.Close()
	names := []string{"project", "runtime.patch.yml", "result.json.pending"}
	if captured {
		names = append(names, "result.tar", "result.json")
	}
	var failures []error
	for _, name := range names {
		failures = append(failures, run.RemoveAll(name))
	}
	return errors.Join(failures...)
}
