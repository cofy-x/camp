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
	ID             string                      `json:"id"`
	Status         string                      `json:"status"`
	Policy         orchestration.Policy        `json:"policy"`
	Events         []orchestration.SourceEvent `json:"events"`
	Tasks          []orchestration.TaskSummary `json:"tasks"`
	TotalTasks     int                         `json:"totalTasks"`
	TasksOmitted   bool                        `json:"tasksOmitted"`
	ContextLimited bool                        `json:"contextLimited"`
	ModelRevision  string                      `json:"modelRevision"`
	Assessment     *orchestration.Assessment   `json:"assessment"`
	CreatedAt      string                      `json:"createdAt"`
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
		return nil
	})
	return out, err
}
