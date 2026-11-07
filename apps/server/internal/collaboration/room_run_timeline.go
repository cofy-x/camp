package collaboration

import (
	"context"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type roomTimelineManager interface {
	RoomRunTimeline(context.Context, string, string, int) (storage.RoomRunTimelinePage, error)
}

type runSummaryManager interface {
	GetRunSummary(context.Context, string) (storage.RunTimeline, error)
}

func (s *Service) GetRunSummary(ctx context.Context, id string) (storage.RunTimeline, error) {
	if reader, ok := s.runs.(runSummaryManager); ok {
		return reader.GetRunSummary(ctx, id)
	}
	return storage.RunTimeline{}, &storage.Error{Code: "execution_unavailable", Message: "Execution summaries are unavailable."}
}

func (s *Service) RoomRunTimeline(ctx context.Context, room, cursor string, limit int) (storage.RoomRunTimelinePage, error) {
	if reader, ok := s.runs.(roomTimelineManager); ok {
		return reader.RoomRunTimeline(ctx, room, cursor, limit)
	}
	return storage.RoomRunTimelinePage{}, &storage.Error{Code: "execution_unavailable", Message: "Execution timeline summaries are unavailable."}
}
