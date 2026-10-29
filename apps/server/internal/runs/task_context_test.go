package runs

import (
	"context"
	"github.com/cofy-x/camp/apps/server/internal/storage"
	"path/filepath"
	"strings"
	"testing"
)

func TestTaskContextIncludesRoomAndThreadOnlyThroughFrozenCutoff(t *testing.T) {
	ctx := context.Background()
	store, e := storage.Open(filepath.Join(t.TempDir(), "desktop.sqlite"))
	if e != nil {
		t.Fatal(e)
	}
	defer store.Close()
	room, e := store.SeedRoom(ctx, storage.Resource{ID: "context-files", Name: "Files", CurrentCommit: strings.Repeat("a", 40)})
	if e != nil {
		t.Fatal(e)
	}
	budget := storage.Budget{MaxTokens: 4096, MaxSteps: 8, TimeoutSeconds: 180}
	send := func(id, thread, text string) storage.Message {
		t.Helper()
		result, e := store.SendMessage(ctx, room.ID, storage.SendMessageInput{RequestID: id, ThreadRootID: thread, Content: text, PostOnly: true}, budget)
		if e != nil {
			t.Fatal(e)
		}
		return result.Message
	}
	send("room-context", "", "The audience is a new teammate.")
	goal := send("goal", "", "Write the guide.")
	frozen := send("thread-context", goal.ID, "Use plain English.")
	send("late", "", "This arrived after the round started.")
	agent, e := store.GetAgent(ctx, room.DefaultAgentID)
	if e != nil {
		t.Fatal(e)
	}
	run := storage.Run{ID: "round", TaskID: "task", RoomID: room.ID, ThreadRootID: goal.ID, OriginMessageID: goal.ID, ContextThroughSeq: frozen.Seq, ResourceID: room.ResourceID, BaselineCommit: strings.Repeat("a", 40), AgentID: agent.ID, AgentSnapshot: agent, Prompt: "Write the guide."}
	m := &Manager{store: store}
	prompt, e := m.conversationPrompt(ctx, run, storage.Conversation{})
	if e != nil {
		t.Fatal(e)
	}
	if !strings.Contains(prompt, "The audience is a new teammate.") || !strings.Contains(prompt, "Use plain English.") || strings.Contains(prompt, "This arrived after the round started.") {
		t.Fatal("task context crossed its frozen boundary", prompt)
	}
}
