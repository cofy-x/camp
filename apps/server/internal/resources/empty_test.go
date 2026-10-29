package resources

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAutomaticEmptyResourceNeedsNoGit(t *testing.T) {
	originalPath := os.Getenv("PATH")
	t.Setenv("PATH", t.TempDir())
	ctx := context.Background()
	manager := New(filepath.Join(t.TempDir(), "resources"))
	result, err := manager.Create(ctx, testProjectID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Commit != EmptyRevision {
		t.Fatal(result)
	}
	body := []byte(fmt.Sprintf("commit %d\x00%s", len(emptyCommitBody), emptyCommitBody))
	digest := sha1.Sum(body)
	if hex.EncodeToString(digest[:]) != EmptyRevision {
		t.Fatal("The empty commit body and identifier disagree")
	}
	files, err := manager.ListFiles(ctx, testProjectID, result.Commit)
	if err != nil || len(files) != 0 {
		t.Fatalf("files %v %v", files, err)
	}
	run := t.TempDir()
	if err = manager.Prepare(ctx, testProjectID, result.Commit, run); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(run, "project"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("prepare %v %v", entries, err)
	}
	if _, err = manager.ReadFile(ctx, testProjectID, result.Commit, "missing.md"); !os.IsNotExist(err) {
		t.Fatal(err)
	}
	repository, _ := manager.repository(testProjectID)
	if _, err = os.Stat(filepath.Join(repository, "objects")); !os.IsNotExist(err) {
		t.Fatal("Create eagerly initialized Git", err)
	}
	if _, err = manager.ListFiles(ctx, strings.Repeat("e", 32), EmptyRevision); err == nil {
		t.Fatal("An unowned empty revision was accepted")
	}
	// A later execution can install the native dependency without replacing data.
	t.Setenv("PATH", originalPath)
	writeFile(t, filepath.Join(run, "project"), "report.md", "# First result\n")
	commit, changes, err := manager.Capture(ctx, testProjectID, result.Commit, run)
	if err != nil || len(changes) != 1 {
		t.Fatalf("capture %s %v %v", commit, changes, err)
	}
	if err = checkCommit(ctx, repository, EmptyRevision); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err = checkCommit(cancelled, repository, commit); !errors.Is(err, context.Canceled) {
		t.Fatalf("A cancelled read was reported as a missing revision: %v", err)
	}
	patch, err := manager.ExportPatch(ctx, testProjectID, EmptyRevision, commit)
	if err != nil || !strings.Contains(string(patch), "+# First result") {
		t.Fatalf("patch %s %v", patch, err)
	}
	content, err := manager.ReadFile(ctx, testProjectID, commit, "report.md")
	if err != nil || string(content) != "# First result\n" {
		t.Fatalf("result %s %v", content, err)
	}
	// The original empty revision remains available without native tools.
	t.Setenv("PATH", t.TempDir())
	if files, err = manager.ListFiles(ctx, testProjectID, EmptyRevision); err != nil || len(files) != 0 {
		t.Fatalf("empty after capture %v %v", files, err)
	}
}

func TestEmptyMaterializationRecoversPartialGitInitialization(t *testing.T) {
	ctx := context.Background()
	manager := New(filepath.Join(t.TempDir(), "resources"))
	if _, err := manager.Create(ctx, testProjectID); err != nil {
		t.Fatal(err)
	}
	repository, _ := manager.repository(testProjectID)
	if _, err := git(ctx, repository, nil, "init", "--bare", "--template=", "--object-format=sha1", repository); err != nil {
		t.Fatal(err)
	}
	if _, err := git(ctx, repository, nil, "mktree"); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := materializeEmptyResource(ctx, repository); err != nil {
			t.Fatal(err)
		}
	}
	body, err := git(ctx, repository, nil, "cat-file", "commit", EmptyRevision)
	if err != nil || string(body) != emptyCommitBody {
		t.Fatalf("commit %s %v", body, err)
	}
}
