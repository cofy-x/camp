package collaboration

import (
	"context"
	"errors"
	"github.com/cofy-x/camp/apps/server/internal/storage"
	"testing"
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
func TestRoomRunTimelineDelegatesOnceWithoutFullRunFanout(t *testing.T) {
	manager := &timelineManagerFixture{}
	service := &Service{runs: manager}
	page, err := service.RoomRunTimeline(context.Background(), "room", "", 20)
	if err != nil || len(page.Items) != 2 || manager.calls != 1 || manager.getCalls != 0 {
		t.Fatal("timeline used full Run fanout", page, manager, err)
	}
}
