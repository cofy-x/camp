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
	changes, e := manager.Changes(ctx, id, initial, current)
	if e != nil || len(changes) != 1 || changes[0].OldSHA == "" || changes[0].OldMode != "100644" || string(changes[0].Content) != "## Exact result\n\nA checked note.\n" {
		t.Fatal(changes, e)
	}
	workflow, e := manager.WriteFile(ctx, id, current, ".github/workflows/ci.yml", []byte("name: unapproved\n"))
	if e != nil {
		t.Fatal(e)
	}
	if _, e = manager.Changes(ctx, id, current, workflow); e == nil {
		t.Fatal("a source publication changed its own required CI configuration")
	}
	if _, e = manager.Changes(ctx, id, current, current); e == nil {
		t.Fatal("an unchanged result created a delivery")
	}
}
