package collaboration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/resources"
	"github.com/cofy-x/camp/apps/server/internal/runs"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestDefaultRoomAndFailedCreationDoNotLeaveResources(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	manager, err := runs.New(ctx, store, dir, runs.Config{MaxTokens: 32, MaxSteps: 2, Timeout: time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Shutdown(ctx)
	service, err := New(ctx, store, manager, dir)
	if err != nil {
		t.Fatal(err)
	}
	rooms, err := store.ListRooms(ctx)
	if err != nil || len(rooms) != 1 || rooms[0].Name != "general" || rooms[0].DefaultAgentID == "" {
		t.Fatalf("seed: %+v %v", rooms, err)
	}
	if _, err := service.CreateRoom(ctx, "Invalid\nRoom", ""); err == nil {
		t.Fatal("Invalid room accepted")
	}
	files, err := os.ReadDir(filepath.Join(dir, "resources"))
	if err != nil || len(files) != 1 {
		t.Fatalf("Failed creation left resources: %v %v", files, err)
	}
	result, err := service.Send(ctx, rooms[0].ID, storage.SendMessageInput{RequestID: "note", Content: "A useful note", PostOnly: true})
	if err != nil || len(result.Runs) != 0 {
		t.Fatalf("Post-only unavailable without credentials: %+v %v", result, err)
	}
	if _, err := New(ctx, store, manager, dir); err != nil {
		t.Fatal(err)
	}
	agents, _ := store.ListAgents(ctx)
	if len(agents) != 1 {
		t.Fatal("Restart duplicated default agent")
	}
}

func TestEnvironmentNormalizationAndCommandValidation(t *testing.T) {
	for _, value := range []string{"", "null", "{}", "{ }", "\n{}\n"} {
		got, profile, err := normalizedEnvironment(json.RawMessage(value))
		if err != nil || string(got) != "{}" || profile.Kind != "" {
			t.Fatalf("%q: %s %v", value, got, err)
		}
	}
	for _, value := range []string{`[]`, `{"kind":"command","checks":[{"name":"test","arguments":[]}]}`, `{"kind":"command","checks":[{"name":"test","arguments":["python3"],"directory":"../escape"}]}`} {
		if _, _, err := normalizedEnvironment(json.RawMessage(value)); err == nil {
			t.Fatalf("Invalid environment accepted: %s", value)
		}
	}
	if _, _, err := normalizedEnvironment(json.RawMessage(`{"kind":"command","checks":[{"name":"test","arguments":["python3","-m","unittest"]}]}`)); err != nil {
		t.Fatal(err)
	}
}

func TestPinnedFileReadsRetainReviewedVersionAfterPublication(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	service := &Service{store: store, files: resources.New(filepath.Join(dir, "resources"))}
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "brief.md"), []byte("Version originally reviewed."), 0600); err != nil {
		t.Fatal(err)
	}
	resource, err := service.ImportResource(ctx, "Research", source, nil)
	if err != nil {
		t.Fatal(err)
	}
	room, err := store.SeedRoom(ctx, resource)
	if err != nil {
		t.Fatal(err)
	}
	files, listingCommit, err := service.ResourceFiles(ctx, resource.ID, "")
	if err != nil || len(files) != 1 || listingCommit != resource.CurrentCommit {
		t.Fatalf("listing %#v %s %v", files, listingCommit, err)
	}
	turn, err := store.SendMessage(ctx, room.ID, storage.SendMessageInput{RequestID: "work", Content: "@assistant Revise brief."}, storage.Budget{MaxTokens: 100, MaxSteps: 1, TimeoutSeconds: 10})
	if err != nil {
		t.Fatal(err)
	}
	run, err := store.ClaimRun(ctx, turn.Runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	working := filepath.Join(dir, "working")
	if err := service.files.Prepare(ctx, resource.ID, listingCommit, working); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(working, "project", "brief.md"), []byte("New shared version."), 0600); err != nil {
		t.Fatal(err)
	}
	run.ResultCommit, run.Files, err = service.files.Capture(ctx, resource.ID, listingCommit, working)
	if err != nil {
		t.Fatal(err)
	}
	run.Status = "succeeded"
	if _, err := store.FinishRun(ctx, run, "Revised brief.", false); err != nil {
		t.Fatal(err)
	}
	for commit, want := range map[string]string{listingCommit: "Version originally reviewed.", "": "New shared version."} {
		content, err := service.ResourceFile(ctx, resource.ID, "brief.md", commit)
		if err != nil || string(content) != want {
			t.Fatalf("read %s: %q %v", commit, content, err)
		}
	}
	if err := os.WriteFile(filepath.Join(working, "project", "brief.md"), []byte("Unregistered Git object."), 0600); err != nil {
		t.Fatal(err)
	}
	unregistered, _, err := service.files.Capture(ctx, resource.ID, run.ResultCommit, working)
	if err != nil {
		t.Fatal(err)
	}
	for _, commit := range []string{"HEAD", strings.Repeat("d", 40), unregistered} {
		if _, err := service.ResourceFile(ctx, resource.ID, "brief.md", commit); err == nil {
			t.Fatalf("unretained revision accepted: %s", commit)
		}
		if _, _, err := service.ResourceFiles(ctx, resource.ID, commit); err == nil {
			t.Fatalf("unretained revision listed: %s", commit)
		}
	}
	original, err := os.ReadFile(filepath.Join(source, "brief.md"))
	if err != nil || string(original) != "Version originally reviewed." {
		t.Fatalf("import source changed: %q %v", original, err)
	}
}
