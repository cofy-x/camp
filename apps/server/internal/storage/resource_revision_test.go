package storage

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestResourceConfigurationSavesCompareTheEditingRevision(t *testing.T) {
	s, room, _ := roomFixture(t)
	baseline, err := s.GetResource(ctx, room.ResourceID)
	if err != nil || baseline.Revision != 1 {
		t.Fatal(baseline, err)
	}
	before, err := s.ListWorkspaceEvents(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		resource Resource
		err      error
	}
	results := make(chan result, 2)
	start := make(chan struct{})
	for _, name := range []string{"First editor", "Second editor"} {
		go func() {
			value := baseline
			value.Name = name
			value.Environment = json.RawMessage(`{"kind":"command","checks":[]}`)
			<-start
			saved, err := s.UpdateResource(ctx, value)
			results <- result{saved, err}
		}()
	}
	close(start)
	var winner Resource
	conflicts := 0
	for range 2 {
		r := <-results
		if r.err == nil {
			if winner.ID != "" {
				t.Fatal("both stale editors committed")
			}
			winner = r.resource
		} else {
			var conflict *Error
			if !errors.As(r.err, &conflict) || conflict.Code != "resource_conflict" {
				t.Fatal(r.err)
			}
			conflicts++
		}
	}
	current, err := s.GetResource(ctx, baseline.ID)
	if err != nil || conflicts != 1 || winner.Revision != 2 || current.Name != winner.Name || current.Revision != 2 || current.CurrentCommit != baseline.CurrentCommit {
		t.Fatal(winner, current, conflicts, err)
	}
	after, err := s.ListWorkspaceEvents(ctx, 0)
	if err != nil || len(after) != len(before)+1 {
		t.Fatal("rejected save emitted an event", before, after, err)
	}
	baseline.Revision = 0
	if _, err := s.UpdateResource(ctx, baseline); err == nil {
		t.Fatal("accepted a configuration without its revision")
	}
}

func TestResourceRevisionMigrationPreservesExistingConfiguration(t *testing.T) {
	s := unmigratedStore(t)
	if err := s.applyMigrations(ctx, migrationSourcesThrough(t, 12)); err != nil {
		t.Fatal(err)
	}
	commit := strings.Repeat("a", 40)
	environment := `{"kind":"command","checks":[]}`
	if _, err := s.db.Exec(`INSERT INTO resources(id,name,source_path,current_commit,environment,created_at,updated_at) VALUES('legacy','Legacy','/private/source',?,?,'created','updated')`, commit, environment); err != nil {
		t.Fatal(err)
	}
	if err := s.applyMigrations(ctx, migrationSources(t)); err != nil {
		t.Fatal(err)
	}
	value, err := s.GetResource(ctx, "legacy")
	if err != nil || value.Revision != 1 || value.Name != "Legacy" || value.SourcePath != "/private/source" || value.CurrentCommit != commit || string(value.Environment) != environment || value.CreatedAt != "created" || value.UpdatedAt != "updated" {
		t.Fatal(value, err)
	}
	// File publication has its own commit precondition and must not invalidate a
	// configuration editor or be overwritten by its older Resource snapshot.
	newCommit := strings.Repeat("b", 40)
	if _, err := s.db.Exec(`UPDATE resources SET current_commit=? WHERE id='legacy'`, newCommit); err != nil {
		t.Fatal(err)
	}
	value.Name = "Updated configuration"
	saved, err := s.UpdateResource(ctx, value)
	if err != nil || saved.Revision != 2 || saved.CurrentCommit != newCommit || saved.CreatedAt != "created" {
		t.Fatal(saved, err)
	}
}
