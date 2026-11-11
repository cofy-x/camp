package remoteruns

import (
	"github.com/cofy-x/camp/apps/server/internal/storage"
	"testing"
)

func TestRoomRunTimelineRetainsCurrentQueueReason(t *testing.T) {
	m, s, actor, attempt, _ := proxyFixture(t, "http://127.0.0.1:1")
	original, err := s.GetRun(actor, attempt.RunID)
	if err != nil {
		t.Fatal(err)
	}
	room, err := s.GetRoom(actor, original.RoomID)
	if err != nil {
		t.Fatal(err)
	}
	sent, err := m.Send(actor, original.RoomID, storage.SendMessageInput{RequestID: "timeline-queued", Content: "Wait", RecipientAgentIDs: []string{original.AgentID}, RunnerRevision: &room.RunnerRevision})
	if err != nil {
		t.Fatal(err)
	}
	full, err := m.Get(actor, sent.Runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	summary, err := m.GetRunSummary(actor, full.ID)
	if err != nil || summary.WaitingReason == "" || summary.WaitingReason != full.WaitingReason {
		t.Fatal("single summary lost current queue reason", summary, err)
	}
	activity, err := m.ConversationRuns(actor, room.ID, []string{sent.Message.ID})
	if err != nil || len(activity.Items) != 1 || activity.Items[0].WaitingReason != full.WaitingReason {
		t.Fatal("conversation queue observation lost", activity, err)
	}

	page, err := m.RoomRunTimeline(actor, original.RoomID, "", 20)
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range page.Items {
		if item.ID == full.ID {
			if item.WaitingReason == "" || item.WaitingReason != full.WaitingReason {
				t.Fatal("queue reason changed", item)
			}
			return
		}
	}
	t.Fatal("queued Run omitted")
}
