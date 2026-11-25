package collaboration

import (
	"context"
	"errors"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type timelineManagerFixture struct {
	RunManager
	calls, getCalls int
}

func (m *timelineManagerFixture) RoomRunTimeline(context.Context, string, string, int) (storage.RoomRunTimelinePage, error) {
	m.calls++
	return storage.RoomRunTimelinePage{Items: []storage.RunTimeline{{ID: "one"}, {ID: "two"}}}, nil
}
func (m *timelineManagerFixture) Get(context.Context, string) (storage.Run, error) {
	m.getCalls++
	return storage.Run{}, errors.New("full Run reads must stay lazy")
}
func (m *timelineManagerFixture) GetRunSummary(context.Context, string) (storage.RunTimeline, error) {
	m.calls++
	return storage.RunTimeline{ID: "one"}, nil
}
func (m *timelineManagerFixture) ListRoomRuns(context.Context, string) ([]storage.Run, error) {
	m.calls++
	return []storage.Run{{ID: "one", Output: "Full output"}, {ID: "two"}}, nil
}

func TestRoomRunsDelegatesOneFullListRead(t *testing.T) {
	manager := &timelineManagerFixture{}
	service := &Service{runs: manager}
	runs, err := service.ListRoomRuns(t.Context(), "room")
	if err != nil || len(runs) != 2 || runs[0].Output != "Full output" || manager.calls != 1 || manager.getCalls != 0 {
		t.Fatal("full Run lists must use the batched reader", runs, manager, err)
	}
}

func TestRoomRunTimelineDelegatesOnceWithoutFullRunFanout(t *testing.T) {
	manager := &timelineManagerFixture{}
	service := &Service{runs: manager}
	page, err := service.RoomRunTimeline(context.Background(), "room", "", 20)
	if err != nil || len(page.Items) != 2 || manager.calls != 1 || manager.getCalls != 0 {
		t.Fatal("timeline used full Run fanout", page, manager, err)
	}
}

func TestRunSummaryDelegatesOnceWithoutFullRunRead(t *testing.T) {
	manager := &timelineManagerFixture{}
	service := &Service{runs: manager}
	summary, err := service.GetRunSummary(context.Background(), "one")
	if err != nil || summary.ID != "one" || manager.calls != 1 || manager.getCalls != 0 {
		t.Fatal("summary read the full Run", summary, manager, err)
	}
	service.runs = struct{ RunManager }{}
	_, err = service.GetRunSummary(context.Background(), "one")
	if err == nil {
		t.Fatal("unsupported managed reader silently omitted its current state")
	}
}

func (m *timelineManagerFixture) ConversationRuns(context.Context, string, []string) (storage.ConversationActivity, error) {
	m.calls++
	return storage.ConversationActivity{Items: []storage.RunTimeline{{ID: "one"}}}, nil
}
func TestConversationActivityDelegatesWithoutFullRunRead(t *testing.T) {
	manager := &timelineManagerFixture{}
	service := &Service{runs: manager}
	page, err := service.ConversationRuns(context.Background(), "room", []string{"message"})
	if err != nil || len(page.Items) != 1 || manager.calls != 1 || manager.getCalls != 0 {
		t.Fatal(page, manager, err)
	}
}
