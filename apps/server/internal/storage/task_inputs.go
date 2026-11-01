package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

const TaskInputPageLimit = 20
const TaskInputPageBytes = 64 << 10

type TaskInputPage struct {
	Items     []TaskInput `json:"items"`
	Through   int64       `json:"through"`
	NextAfter *int64      `json:"nextAfter"`
	HasMore   bool        `json:"hasMore"`
}

func taskInputRange(ctx context.Context, q *sqlgen.Queries, id string, after, through int64, limit int) ([]TaskInput, error) {
	rows, err := q.TaskInputRange(ctx, sqlgen.TaskInputRangeParams{TaskID: id, AfterSeq: after, ThroughSeq: through, PageLimit: int64(limit)})
	if err != nil {
		return nil, err
	}
	inputs := make([]TaskInput, 0, len(rows))
	for _, raw := range rows {
		var input TaskInput
		if err := json.Unmarshal([]byte(raw), &input); err != nil {
			return nil, err
		}
		inputs = append(inputs, input)
	}
	return inputs, nil
}

// TaskInputs reads a frozen range for preparation, never a moving input tail.
func (s *Store) TaskInputs(ctx context.Context, id string, after, through int64) ([]TaskInput, error) {
	t, err := getTask(ctx, s.queries(), id)
	if err != nil {
		return nil, err
	}
	if after < 0 || through < after || through > t.InputRevision {
		return nil, problem("invalid_page", "The input range is outside this collaboration.")
	}
	// Legacy preparation still delivers the entire bounded prefix. Larger
	// histories require the batch protocol before admission limits can change.
	if through-after > 128 {
		return nil, problem("task_pagination_required", "This input range requires bounded round preparation.")
	}
	return taskInputRange(ctx, s.queries(), id, after, through, 128)
}

// TaskInputsPage is snapshot-bounded. The transport owns caller authorization.
func (s *Store) TaskInputsPage(ctx context.Context, id string, after int64, through *int64, limit int) (TaskInputPage, error) {
	page := TaskInputPage{Items: []TaskInput{}}
	t, err := getTask(ctx, s.queries(), id)
	if err != nil {
		return page, err
	}
	return taskInputsPage(ctx, s.queries(), t, after, through, limit)
}

func taskInputsPage(ctx context.Context, q *sqlgen.Queries, t CollaborationTask, after int64, through *int64, limit int) (TaskInputPage, error) {
	page := TaskInputPage{Items: []TaskInput{}}
	page.Through = t.InputRevision
	if through != nil {
		page.Through = *through
	}
	if after < 0 || page.Through < after || page.Through > t.InputRevision || limit < 0 || limit > TaskInputPageLimit {
		return page, problem("invalid_page", "Choose a valid frozen input range and at most 20 inputs.")
	}
	if limit == 0 {
		limit = TaskInputPageLimit
	}
	inputs, err := taskInputRange(ctx, q, t.ID, after, page.Through, limit+1)
	if err != nil {
		return page, err
	}
	for _, input := range inputs {
		if len(page.Items) == limit {
			break
		}
		if input.Seq != after+int64(len(page.Items))+1 {
			return page, problem("invalid_task", "The stored input sequence is incomplete.")
		}
		page.Items = append(page.Items, input)
		page.HasMore = input.Seq < page.Through
		page.NextAfter = &page.Items[len(page.Items)-1].Seq
		encoded, err := json.Marshal(page)
		if err != nil {
			return page, err
		}
		if len(encoded)+1 > TaskInputPageBytes {
			page.Items = page.Items[:len(page.Items)-1]
			break
		}
	}
	if len(page.Items) == 0 {
		page.NextAfter = nil
		page.HasMore = after < page.Through
		if page.HasMore {
			return page, problem("invalid_task", "The stored input range cannot be read as a bounded page.")
		}
	} else {
		last := page.Items[len(page.Items)-1].Seq
		page.HasMore = last < page.Through
		if page.HasMore {
			page.NextAfter = &last
		} else {
			page.NextAfter = nil
		}
	}
	return page, nil
}

func taskOutput(ctx context.Context, q *sqlgen.Queries, t CollaborationTask, err error, paged bool) (CollaborationTask, error) {
	if err != nil || paged {
		t.Inputs = nil
		return t, err
	}
	if t.InputRevision > 128 || t.InputBytes > 64<<10 {
		return t, problem("task_pagination_required", "Refresh Camp to read this collaboration's input pages.")
	}
	// Legacy receipts carry their own originals. New receipts reference the
	// append-only log at their accepted revision, never today's latest inputs.
	if int64(len(t.Inputs)) == t.InputRevision && t.Inputs != nil {
		return t, nil
	}
	t.Inputs, err = taskInputRange(ctx, q, t.ID, 0, t.InputRevision, 129)
	return t, err
}

type TaskRunSummary struct {
	ID             string `json:"id"`
	AgentID        string `json:"agentId"`
	AgentName      string `json:"agentName"`
	RunnerName     string `json:"runnerName"`
	ChecksDeferred bool   `json:"checksDeferred"`
	Status         string `json:"status"`
	CreatedAt      string `json:"createdAt"`
	UpdatedAt      string `json:"updatedAt"`
}

type TaskRunPage struct {
	Items      []TaskRunSummary `json:"items"`
	NextCursor string           `json:"nextCursor"`
}

type taskRunCursor struct {
	Task string `json:"task"`
	At   string `json:"at"`
	ID   string `json:"id"`
}

func (s *Store) TaskRunsPage(ctx context.Context, id, cursor string, limit int) (TaskRunPage, error) {
	page := TaskRunPage{Items: []TaskRunSummary{}}
	if _, err := getTask(ctx, s.queries(), id); err != nil {
		return page, err
	}
	position := taskRunCursor{Task: id}
	if cursor != "" {
		if len(cursor) > 684 {
			return page, problem("invalid_page", "The history cursor is too large.")
		}
		data, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || len(data) > 512 || json.Unmarshal(data, &position) != nil || position.Task != id || !validText(position.ID, 128, false) {
			return page, problem("invalid_page", "The history cursor is invalid for this collaboration.")
		}
		if _, err := time.Parse(time.RFC3339Nano, position.At); err != nil {
			return page, problem("invalid_page", "The history cursor is invalid.")
		}
	}
	if limit < 0 || limit > TaskInputPageLimit {
		return page, problem("invalid_page", "Read at most 20 rounds at a time.")
	}
	if limit == 0 {
		limit = TaskInputPageLimit
	}
	rows, err := s.queries().TaskRunsPage(ctx, sqlgen.TaskRunsPageParams{TaskID: id, AfterCreatedAt: position.At, AfterID: position.ID, PageLimit: int64(limit + 1)})
	if err != nil {
		return page, err
	}
	for i, row := range rows {
		if i == limit {
			last := page.Items[i-1]
			data, _ := json.Marshal(taskRunCursor{Task: id, At: last.CreatedAt, ID: last.ID})
			page.NextCursor = base64.RawURLEncoding.EncodeToString(data)
			break
		}
		page.Items = append(page.Items, TaskRunSummary{ID: row.ID, AgentID: row.AgentID, AgentName: row.AgentName, RunnerName: row.RunnerName, ChecksDeferred: row.ChecksDeferred != 0, Status: row.Status, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt})
	}
	return page, nil
}

func validateInputProtocol(version int, paged bool) error {
	if version < 0 || version > 1 {
		return problem("invalid_task", "Unsupported task input protocol.")
	}
	if version == 1 && !paged {
		return problem("task_pagination_required", "Use summary responses with the bounded input protocol.")
	}
	return nil
}

// TaskBatchInputs validates the entire frozen batch before constructing a brief.
func (s *Store) TaskBatchInputs(ctx context.Context, id string, after, through int64) ([]TaskInput, error) {
	t, err := getTask(ctx, s.queries(), id)
	if err != nil {
		return nil, err
	}
	if after < 0 || through < after || through > t.InputRevision {
		return nil, problem("invalid_page", "The input batch is outside this collaboration.")
	}
	inputs, end, err := taskInputBatch(ctx, s.queries(), id, after, through)
	if err != nil {
		return nil, err
	}
	if end != through {
		return nil, problem("invalid_task", "The frozen input batch exceeds its budget.")
	}
	return inputs, nil
}

func taskInputRunnerCompatible(capabilities string, version int) error {
	if version == 0 {
		return nil
	}
	if version != 1 {
		return problem("invalid_task", "Unsupported task input protocol.")
	}
	var caps struct {
		TaskInputsVersion int `json:"taskInputsVersion"`
	}
	if err := json.Unmarshal([]byte(capabilities), &caps); err != nil {
		return err
	}
	if caps.TaskInputsVersion != 1 {
		return problem("runner_upgrade_required", "Update the assigned Runner to process bounded task inputs.")
	}
	return nil
}

// taskInputBatch plans by the actual JSON array bytes used by TaskBrief, rather
// than spending another execution turn merely because a history page is full.
func taskInputBatch(ctx context.Context, q *sqlgen.Queries, id string, after, through int64) ([]TaskInput, int64, error) {
	inputs := []TaskInput{}
	size, end := 3, after // brackets and the input section's trailing newline
	if after < 0 || through < after {
		return nil, after, problem("invalid_task", "The input batch has invalid frontiers.")
	}
	for end < through {
		rows, err := taskInputRange(ctx, q, id, end, through, TaskInputPageLimit)
		if err != nil {
			return nil, after, err
		}
		if len(rows) == 0 {
			return nil, after, problem("invalid_task", "The stored input sequence is incomplete.")
		}
		for _, input := range rows {
			if input.Seq != end+1 {
				return nil, after, problem("invalid_task", "The stored input sequence is incomplete.")
			}
			encoded, err := json.Marshal(input)
			if err != nil {
				return nil, after, err
			}
			added := len(encoded)
			if len(inputs) > 0 {
				added++
			}
			if size+added > TaskInputPageBytes {
				if len(inputs) == 0 {
					return nil, after, problem("invalid_task", "A stored input cannot fit the round budget.")
				}
				return inputs, end, nil
			}
			inputs = append(inputs, input)
			size += added
			end = input.Seq
		}
	}
	return inputs, end, nil
}
