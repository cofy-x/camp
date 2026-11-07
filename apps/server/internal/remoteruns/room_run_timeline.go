package remoteruns

import (
	"context"
	"encoding/json"

	"github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func (m *Manager) GetRunSummary(ctx context.Context, id string) (storage.RunTimeline, error) {
	item, err := m.store.GetRunSummary(ctx, id)
	if err == nil && item.Status == "queued" {
		item.WaitingReason, err = m.queueReason(ctx, item.RunnerID, item.TaskID)
	}
	if err != nil {
		return item, err
	}
	return storage.BoundRunSummary(item)
}

func (m *Manager) queueReason(ctx context.Context, runnerID, taskID string) (string, error) {
	if runnerID == "" {
		return "Choose a runner and submit a new request.", nil
	}
	if !m.IsOnline(runnerID) {
		return "Waiting for the assigned runner to connect.", nil
	}
	reason := "Waiting for an available execution slot or file writer."
	if taskID != "" {
		task, err := m.store.GetTaskState(ctx, taskID)
		if err != nil {
			return "", err
		}
		if task.InputProtocol == 1 {
			runner, err := m.store.GetRunner(ctx, runnerID)
			if err != nil {
				return "", err
			}
			var caps runnerprotocol.Capabilities
			if err = json.Unmarshal(runner.Capabilities, &caps); err != nil {
				return "", err
			}
			if caps.TaskInputsVersion != 1 {
				reason = "Update the assigned Runner to process this collaboration's inputs."
			}
		}
	}
	return reason, nil
}
func (m *Manager) RoomRunTimeline(ctx context.Context, room, cursor string, limit int) (storage.RoomRunTimelinePage, error) {
	page, err := m.store.RoomRunTimeline(ctx, room, cursor, limit)
	if err != nil {
		return page, err
	}
	for index := range page.Items {
		item := &page.Items[index]
		if item.Status == "queued" {
			item.WaitingReason, err = m.queueReason(ctx, item.RunnerID, item.TaskID)
			if err != nil {
				return page, err
			}
		}
	}
	err = page.Bound()
	return page, err
}
