package runs

import (
	"context"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

// GetRunSummary preserves an unpersisted terminal result without loading its
// original output or files from storage.
func (m *Manager) GetRunSummary(ctx context.Context, id string) (storage.RunTimeline, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if run, ok := m.unsaved[id]; ok {
		return storage.BoundRunSummary(storage.ProjectRunTimeline(run))
	}
	return m.store.GetRunSummary(ctx, id)
}

// RoomRunTimeline retains unpersisted terminal state without reloading every Run.
func (m *Manager) RoomRunTimeline(ctx context.Context, room, cursor string, limit int) (storage.RoomRunTimelinePage, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	page, err := m.store.RoomRunTimeline(ctx, room, cursor, limit)
	if err != nil {
		return page, err
	}
	for index, item := range page.Items {
		if run, ok := m.unsaved[item.ID]; ok {
			page.Items[index] = storage.ProjectRunTimeline(run)
		}
	}
	err = page.Bound()
	return page, err
}
