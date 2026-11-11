package runs

import (
	"context"
	"encoding/json"
	"github.com/cofy-x/camp/apps/server/internal/storage"
	"strings"
	"testing"
)

func TestRoomRunTimelinePreservesUnsavedTerminalOverlay(t *testing.T) {
	m, s, _, room := testManager(t)
	sent, err := s.SendMessage(context.Background(), room.ID, storage.SendMessageInput{RequestID: "timeline-overlay", Content: "Work", RecipientAgentIDs: []string{room.DefaultAgentID}}, storage.Budget{MaxTokens: 32, MaxSteps: 2, TimeoutSeconds: 2})
	if err != nil {
		t.Fatal(err)
	}
	run := sent.Runs[0]
	run.Status = "failed"
	run.Error = "Final persistence failed."
	run.Output = "PRIVATE_OUTPUT"
	run.Files = []storage.RunFile{{Path: "saved.txt", Status: "added", After: "PRIVATE_FILE"}}
	m.mu.Lock()
	m.unsaved[run.ID] = run
	m.mu.Unlock()
	page, err := m.RoomRunTimeline(context.Background(), room.ID, "", 20)
	if err != nil || len(page.Items) != 1 || page.Items[0].Status != "failed" || page.Items[0].Error != run.Error || page.Items[0].FileCount != 1 {
		t.Fatal("unsaved terminal state lost", page, err)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "PRIVATE_") {
		t.Fatal("overlay bypassed projection")
	}
	stored, err := s.GetRun(context.Background(), run.ID)
	if err != nil || stored.Status != "queued" {
		t.Fatal("read persisted overlay", err)
	}
	summary, err := m.GetRunSummary(context.Background(), run.ID)
	if err != nil || summary.Status != "failed" || summary.Error != run.Error || summary.FileCount != 1 {
		t.Fatal("single summary lost terminal overlay", summary, err)
	}
	raw, _ = json.Marshal(summary)
	if strings.Contains(string(raw), "PRIVATE_") {
		t.Fatal("single overlay bypassed projection")
	}
}

func TestConversationActivityPreservesUnsavedTerminalOverlay(t *testing.T) {
	m, s, _, room := testManager(t)
	sent, err := s.SendMessage(context.Background(), room.ID, storage.SendMessageInput{RequestID: "window-overlay", Content: "Work", RecipientAgentIDs: []string{room.DefaultAgentID}}, storage.Budget{MaxTokens: 32, MaxSteps: 2, TimeoutSeconds: 2})
	if err != nil {
		t.Fatal(err)
	}
	run := sent.Runs[0]
	run.Status = "failed"
	run.Error = "Final persistence failed."
	run.Output = "PRIVATE_OUTPUT"
	m.mu.Lock()
	m.unsaved[run.ID] = run
	m.mu.Unlock()
	page, err := m.ConversationRuns(context.Background(), room.ID, []string{sent.Message.ID})
	if err != nil || len(page.Items) != 1 || page.Items[0].Status != "failed" || page.Items[0].Error != run.Error {
		t.Fatal(page, err)
	}
	raw, _ := json.Marshal(page)
	if strings.Contains(string(raw), "PRIVATE_OUTPUT") {
		t.Fatal("full output escaped projection")
	}
}
