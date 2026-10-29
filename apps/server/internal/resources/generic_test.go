package resources

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
)

func TestResourcesDoNotRequireLanguageOrManifest(t *testing.T) {
	for _, fixture := range []struct{ name, path, body string }{
		{"empty", "", ""}, {"documents", "dist/brief.md", "# Brief\n"},
		{"go", "main.go", "package main\n"}, {"python", "app.py", "print('hello')\n"},
		{"unconfigured-node", "package.json", `{"dependencies":{"example":"1.0.0"}}`},
		{"invalid-manifest", "package.json", "{"},
	} {
		t.Run(fixture.name, func(t *testing.T) {
			ctx := context.Background()
			manager := New(filepath.Join(t.TempDir(), "resources"))
			source := t.TempDir()
			if fixture.path != "" {
				writeFile(t, source, fixture.path, fixture.body)
			}
			result, err := manager.Import(ctx, testProjectID, source)
			if err != nil {
				t.Fatal(err)
			}
			run := t.TempDir()
			if err := manager.Prepare(ctx, testProjectID, result.Commit, run); err != nil {
				t.Fatal(err)
			}
			writeFile(t, filepath.Join(run, "project"), "coverage/report.md", "A deliverable, not a Node cache.")
			commit, files, err := manager.Capture(ctx, testProjectID, result.Commit, run)
			if err != nil || len(files) != 1 || files[0].Path != "coverage/report.md" {
				t.Fatalf("capture: %+v %v", files, err)
			}
			body, err := manager.ReadFile(ctx, testProjectID, commit, "coverage/report.md")
			if err != nil || !strings.Contains(string(body), "deliverable") {
				t.Fatalf("read: %s %v", body, err)
			}
			if _, err := manager.ReadFile(ctx, testProjectID, commit, "../escape"); err == nil {
				t.Fatal("Path traversal accepted")
			}
		})
	}
}

func TestCreateEmptyResourceAndOptionalExclusions(t *testing.T) {
	ctx := context.Background()
	manager := New(filepath.Join(t.TempDir(), "resources"))
	result, err := manager.Create(ctx, testProjectID)
	if err != nil {
		t.Fatal(err)
	}
	files, err := manager.ListFiles(ctx, testProjectID, result.Commit)
	if err != nil || len(files) != 0 {
		t.Fatalf("empty resource: %v %v", files, err)
	}
	source := t.TempDir()
	writeFile(t, source, "node_modules/reference.md", "valid generic directory")
	writeFile(t, source, "dist/report.md", "keep this")
	id := strings.Repeat("c", 32)
	result, err = manager.ImportWithPolicy(ctx, id, source, Policy{ExcludeNames: []string{"node_modules"}})
	if err != nil {
		t.Fatal(err)
	}
	files, err = manager.ListFiles(ctx, id, result.Commit)
	if err != nil || len(files) != 1 || files[0] != "dist/report.md" {
		t.Fatalf("optional policy: %v %v", files, err)
	}
}
