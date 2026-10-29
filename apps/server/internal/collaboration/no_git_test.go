package collaboration

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/runs"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestFreshDesktopAndRoomsWorkWithoutGit(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
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
	if err != nil || len(rooms) != 1 || rooms[0].Name != "general" {
		t.Fatalf("rooms %+v %v", rooms, err)
	}
	files, _, err := service.ResourceFiles(ctx, rooms[0].ResourceID, "")
	if err != nil || len(files) != 0 {
		t.Fatalf("files %v %v", files, err)
	}
	if _, err = service.CreateRoom(ctx, "ideas", "A new room without native dependencies."); err != nil {
		t.Fatal(err)
	}
	posted, err := service.Send(ctx, rooms[0].ID, storage.SendMessageInput{RequestID: "offline-note", Content: "Keep this useful note.", PostOnly: true})
	if err != nil || len(posted.Runs) != 0 {
		t.Fatalf("post %+v %v", posted, err)
	}
	bootstrap, err := store.Bootstrap(ctx, "test")
	if err != nil || bootstrap.Preferences.Wallpaper == "" {
		t.Fatal("desktop preferences unavailable", err)
	}
	if available, _, _ := manager.Availability(); available {
		t.Fatal("Execution should be unavailable without native dependencies or credentials")
	}
}
