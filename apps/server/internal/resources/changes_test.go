package resources

import (
	"context"
	"path/filepath"
	"testing"
)

func TestDeliveryChangesPreserveExactBytesAndExcludeWorkflowEdits(t *testing.T) {
	ctx := context.Background()
	manager := New(filepath.Join(t.TempDir(), "resources"))
	id := "0123456789abcdef0123456789abcdef"
	base, e := manager.Create(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	initial, e := manager.WriteFile(ctx, id, base.Commit, "notes.md", []byte("Original\n"))
	if e != nil {
		t.Fatal(e)
	}
	current, e := manager.WriteFile(ctx, id, initial, "notes.md", []byte("## Exact result\n\nA checked note.\n"))
	if e != nil {
		t.Fatal(e)
	}
	changes, e := manager.Changes(ctx, id, initial, current, nil)
	if e != nil || len(changes) != 1 || changes[0].OldSHA == "" || changes[0].OldMode != "100644" || string(changes[0].Content) != "## Exact result\n\nA checked note.\n" {
		t.Fatal(changes, e)
	}
	workflow, e := manager.WriteFile(ctx, id, current, ".github/workflows/ci.yml", []byte("name: unapproved\n"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = manager.Changes(ctx, id, current, workflow, nil); e == nil {
		t.Fatal("a source publication changed its own required CI configuration")
	}
	if _, e = manager.Changes(ctx, id, current, current, nil); e == nil {
		t.Fatal("an unchanged result created a delivery")
	}
}

func TestSourceDeliveryUsesGitIgnoreRulesWithoutDroppingTrackedFilesOrOriginals(t *testing.T) {
	ctx := context.Background()
	manager := New(filepath.Join(t.TempDir(), "resources"))
	id := "1234567890abcdef1234567890abcdef"
	created, e := manager.Create(ctx, id)
	if e != nil {
		t.Fatal(e)
	}
	base, e := manager.WriteFile(ctx, id, created.Commit, ".gitignore", []byte("dist/\ncache/*\n!cache/keep.txt\n*.log\n"))
	if e != nil {
		t.Fatal(e)
	}
	base, e = manager.WriteFile(ctx, id, base, "dist/tracked.js", []byte("old tracked output"))
	if e != nil {
		t.Fatal(e)
	}
	result := base
	for _, file := range []struct{ path, body string }{{"dist/tracked.js", "new tracked output"}, {"dist/new.js", "generated"}, {"cache/keep.txt", "retained"}, {"cache/drop.txt", "generated"}, {"debug.log", "generated"}, {"src/main.js", "source"}, {"packages/app/.gitignore", "out/\n"}, {"packages/app/out/bundle.js", "generated"}} {
		result, e = manager.WriteFile(ctx, id, result, file.path, []byte(file.body))
		if e != nil {
			t.Fatal(e)
		}
	}
	changes, e := manager.Changes(ctx, id, base, result, []string{".gitignore", "dist/tracked.js"})
	if e != nil {
		t.Fatal(e)
	}
	got := map[string]bool{}
	for _, change := range changes {
		got[change.Path] = true
	}
	if len(got) != 4 || !got["dist/tracked.js"] || !got["cache/keep.txt"] || !got["src/main.js"] || !got["packages/app/.gitignore"] {
		t.Fatal("source selection differs from Git semantics", got)
	}
	original, e := manager.ReadFile(ctx, id, result, "dist/new.js")
	if e != nil || string(original) != "generated" {
		t.Fatal("source selection changed the original result", e)
	}
}
