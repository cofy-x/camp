package storage

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

const automationEvaluationViewBytes = 128 << 10

type AutomationEvaluationView struct {
	WorkItems        []orchestration.WorkItemSummary `json:"workItems"`
	TotalWorkItems   int                             `json:"totalWorkItems"`
	WorkItemsOmitted bool                            `json:"workItemsOmitted"`
	Memories         []orchestration.Memory          `json:"memories"`
	TotalMemories    int                             `json:"totalMemories"`
	MemoriesOmitted  bool                            `json:"memoriesOmitted"`
	ID               string                          `json:"id"`
	Status           string                          `json:"status"`
	Policy           orchestration.Policy            `json:"policy"`
	Events           []orchestration.SourceEvent     `json:"events"`
	Tasks            []orchestration.TaskSummary     `json:"tasks"`
	TotalTasks       int                             `json:"totalTasks"`
	TasksOmitted     bool                            `json:"tasksOmitted"`
	ContextLimited   bool                            `json:"contextLimited"`
	ModelRevision    string                          `json:"modelRevision"`
	Assessment       *orchestration.Assessment       `json:"assessment"`
	CreatedAt        string                          `json:"createdAt"`
}

// GetAutomationEvaluation exposes frozen decision evidence, without source
// bodies, execution configuration, credentials or any model/execution side effect.
func (s *Store) GetAutomationEvaluation(ctx context.Context, id string) (out AutomationEvaluationView, err error) {
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := requireActor(ctx, q, false); e != nil {
			return e
		}
		row, e := q.GetAutomationEvaluation(ctx, id)
		if e != nil {
			return missing(e, "The event assessment")
		}
		var candidate orchestration.Candidate
		if e = json.Unmarshal([]byte(row.Candidate), &candidate); e != nil {
			return e
		}
		if _, e = q.GetRoom(ctx, candidate.Policy.RoomID); e != nil {
			return missing(e, "The Room")
		}
		out = AutomationEvaluationView{ID: row.ID, Status: row.Status, Policy: candidate.Policy, Events: []orchestration.SourceEvent{}, Tasks: []orchestration.TaskSummary{}, TotalTasks: len(candidate.Tasks), ModelRevision: candidate.ModelRevision, CreatedAt: row.CreatedAt}
		out.WorkItems = []orchestration.WorkItemSummary{}
		out.Memories = []orchestration.Memory{}
		out.TotalWorkItems, out.TotalMemories = len(candidate.WorkItems), len(candidate.Memories)
		out.ContextLimited = candidate.ContextLimited
		if candidate.TotalTaskCount > out.TotalTasks {
			out.TotalTasks, out.TasksOmitted = candidate.TotalTaskCount, true
		}
		out.Policy.CredentialRef = ""
		for _, event := range candidate.Events {
			event.Content = ""
			out.Events = append(out.Events, event)
		}
		if row.Status != "running" {
			var assessment orchestration.Assessment
			if e = json.Unmarshal([]byte(row.Assessment), &assessment); e != nil {
				return e
			}
			out.Assessment = &assessment
		}
		encoded, e := json.Marshal(out)
		if e != nil {
			return e
		}
		if len(encoded)+1 > automationEvaluationViewBytes {
			return problem("invalid_page", "The frozen decision metadata exceeds its bounded view.")
		}
		for _, task := range candidate.Tasks {
			out.Tasks = append(out.Tasks, task)
			encoded, e = json.Marshal(out)
			if e != nil {
				return e
			}
			if len(encoded)+1 > automationEvaluationViewBytes {
				out.Tasks = out.Tasks[:len(out.Tasks)-1]
				out.TasksOmitted = true
				break
			}
		}
		for _, work := range candidate.WorkItems {
			out.WorkItems = append(out.WorkItems, work)
			encoded, e = json.Marshal(out)
			if e != nil {
				return e
			}
			if len(encoded)+1 > automationEvaluationViewBytes {
				out.WorkItems = out.WorkItems[:len(out.WorkItems)-1]
				out.WorkItemsOmitted = true
				break
			}
		}
		for _, memory := range candidate.Memories {
			out.Memories = append(out.Memories, memory)
			encoded, e = json.Marshal(out)
			if e != nil {
				return e
			}
			if len(encoded)+1 > automationEvaluationViewBytes {
				out.Memories = out.Memories[:len(out.Memories)-1]
				out.MemoriesOmitted = true
				break
			}
		}
		return nil
	})
	return out, err
}

// AutomationContextPage pages the stored candidate, never a current entity
// lookup. after is an index into the immutable array selected by kind.
type AutomationContextPage struct {
	Items          []json.RawMessage `json:"items"`
	NextAfter      int64             `json:"nextAfter"`
	HasMore        bool              `json:"hasMore"`
	Total          int               `json:"total"`
	ContextLimited bool              `json:"contextLimited,omitempty"`
}

func (s *Store) AutomationEvaluationContext(ctx context.Context, id, kind string, after int64, limit int) (out AutomationContextPage, err error) {
	out.Items = []json.RawMessage{}
	if (kind != "work-items" && kind != "memories") || after < 0 || limit < 0 || limit > 10 {
		return out, problem("invalid_page", "Choose work-items or memories, a nonnegative index and at most 10 frozen records.")
	}
	if limit == 0 {
		limit = 10
	}
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := requireActor(ctx, q, false); e != nil {
			return e
		}
		row, e := q.GetAutomationEvaluation(ctx, id)
		if e != nil {
			return missing(e, "The event assessment")
		}
		var candidate orchestration.Candidate
		if e = json.Unmarshal([]byte(row.Candidate), &candidate); e != nil {
			return e
		}
		if _, e = q.GetRoom(ctx, candidate.Policy.RoomID); e != nil {
			return missing(e, "The Room")
		}
		out.ContextLimited = candidate.ContextLimited
		var count int
		if kind == "work-items" {
			count = len(candidate.WorkItems)
		} else {
			count = len(candidate.Memories)
		}
		if after > int64(count) {
			return problem("invalid_page", "The context cursor exceeds this frozen array.")
		}
		out.Total, out.NextAfter = count, after
		for index := int(after); index < count && len(out.Items) < limit; index++ {
			var raw []byte
			if kind == "work-items" {
				raw, e = json.Marshal(candidate.WorkItems[index])
			} else {
				raw, e = json.Marshal(candidate.Memories[index])
			}
			if e != nil {
				return e
			}
			out.Items = append(out.Items, json.RawMessage(raw))
			out.NextAfter, out.HasMore = int64(index+1), index+1 < count
			encoded, e := json.Marshal(out)
			if e != nil {
				return e
			}
			if len(encoded)+1 > automationEvaluationViewBytes {
				out.Items = out.Items[:len(out.Items)-1]
				out.NextAfter, out.HasMore = int64(index), true
				if len(out.Items) == 0 {
					return problem("invalid_page", "This frozen context record exceeds the bounded response.")
				}
				break
			}
		}
		return nil
	})
	return
}
