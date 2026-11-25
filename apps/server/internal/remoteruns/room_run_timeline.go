package remoteruns

import (
	"context"
	"encoding/json"

	"github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func (m *Manager) ListRoomRuns(ctx context.Context, room string) ([]storage.Run, error) {
	items, err := m.store.ListRoomRuns(ctx, room)
	if err != nil {
		return nil, err
	}
	for i := range items {
		item := &items[i]
		if item.Status == "queued" {
			item.WaitingReason, err = m.queueReason(ctx, item.RunnerID, item.TaskID)
			if err != nil {
				return nil, err
			}
		}
	}
	return items, nil
}

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
	if taskID == "" {
		return reason, nil
	}
	task, err := m.store.GetTaskState(ctx, taskID)
	if err != nil {
		return "", err
	}
	if task.InputProtocol != 1 && !task.WorkItemCompletionAuthorized {
		return reason, nil
	}
	runner, err := m.store.GetRunner(ctx, runnerID)
	if err != nil {
		return "", err
	}
	var caps runnerprotocol.Capabilities
	if err = json.Unmarshal(runner.Capabilities, &caps); err != nil {
		return "", err
	}
	switch {
	case task.InputProtocol == 1 && caps.TaskInputsVersion != 1:
		return "Update the assigned Runner to process this collaboration's inputs.", nil
	case task.WorkItemCompletionAuthorized && caps.WorkItemCompletionVersion != 1:
		return "Update the assigned Runner to support automatic Work completion.", nil
	default:
		return reason, nil
	}
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

func (m *Manager) ConversationRuns(ctx context.Context, room string, ids []string) (storage.ConversationActivity, error) {
	page, err := m.store.ConversationRuns(ctx, room, ids)
	if err != nil {
		return page, err
	}
	for i := range page.Items {
		item := &page.Items[i]
		if item.Status == "queued" {
			item.WaitingReason, err = m.queueReason(ctx, item.RunnerID, item.TaskID)
			if err != nil {
				return page, err
			}
		}
	}
	return page, page.Bound()
}
