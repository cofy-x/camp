package collaboration

import (
	"context"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type roomTimelineManager interface {
	RoomRunTimeline(context.Context, string, string, int) (storage.RoomRunTimelinePage, error)
}

func (s *Service) RoomRunTimeline(ctx context.Context, room, cursor string, limit int) (storage.RoomRunTimelinePage, error) {
	if reader, ok := s.runs.(roomTimelineManager); ok {
		return reader.RoomRunTimeline(ctx, room, cursor, limit)
	}
	return storage.RoomRunTimelinePage{}, &storage.Error{Code: "execution_unavailable", Message: "Execution timeline summaries are unavailable."}
}
