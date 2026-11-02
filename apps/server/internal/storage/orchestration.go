package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

const OrchestrationPageLimit = 20
const OrchestrationPageBytes = 64 << 10

// planRoomMessage is shared by readiness preflight and transactional admission.
// Only the latter can authorize work, using the membership it reads itself.
func planRoomMessage(ctx context.Context, q *sqlgen.Queries, room string, in SendMessageInput) (orchestration.Decision, error) {
	input := orchestration.RoomInput{Content: in.Content, RecipientAgentIDs: in.RecipientAgentIDs, PostOnly: in.PostOnly}
	if !in.PostOnly {
		members, err := q.ListMembers(ctx, room)
		if err != nil {
			return orchestration.Decision{}, err
		}
		for _, member := range members {
			input.Members = append(input.Members, orchestration.Member{ID: member.ID, Handle: member.Handle})
		}
	}
	decision, err := orchestration.PlanRoomMessage("user", input)
	if errors.Is(err, orchestration.ErrTooManyRecipients) || errors.Is(err, orchestration.ErrPostOnlyRecipients) {
		return orchestration.Decision{}, problem("invalid_message", err.Error())
	}
	return decision, err
}

func recordOrchestration(ctx context.Context, q *sqlgen.Queries, record orchestration.Record) error {
	raw, err := json.Marshal(record)
	if err != nil {
		return err
	}
	if len(raw) > OrchestrationPageBytes/2 {
		return problem("invalid_message", "The admission record exceeds its size limit.")
	}
	t := record.Trigger
	return q.InsertOrchestrationRecord(ctx, sqlgen.InsertOrchestrationRecordParams{Source: t.Source, EventID: t.EventID, RoomID: t.RoomID, MessageID: nullableParent(t.MessageID), TaskID: nullableParent(t.TaskID), Value: string(raw)})
}

func recordRoomAdmission(ctx context.Context, q *sqlgen.Queries, result SendResult, decision orchestration.Decision, fingerprint string) error {
	m := result.Message
	record := orchestration.Record{Trigger: orchestration.Trigger{Source: "room", EventID: m.ID, Kind: orchestration.RoomMessage, ActorKind: m.AuthorKind, ActorID: m.AuthorUserID, RoomID: m.RoomID, ThreadRootID: m.ThreadRootID, MessageID: m.ID, Request: &orchestration.RequestRef{Scope: m.RoomID, ID: m.RequestID, Hash: fingerprint}, OccurredAt: m.CreatedAt}, Decision: decision, Mandates: []orchestration.Mandate{}}
	for _, run := range result.Runs {
		work := orchestration.Work{Kind: "run", ID: run.ID}
		record.Decision.Work = append(record.Decision.Work, work)
		record.Mandates = append(record.Mandates, orchestration.Mandate{Work: work, RequestedByUserID: run.RequestedByUserID, ResourceID: run.ResourceID, Participants: []orchestration.Participant{admissionParticipant(run.AgentSnapshot, run.RunnerID)}, MaxRounds: 1, MaxTokens: run.MaxTokens, MaxSteps: run.MaxSteps, TimeoutSeconds: run.TimeoutSeconds})
	}
	return recordOrchestration(ctx, q, record)
}

func admissionParticipant(agent Agent, runner string) orchestration.Participant {
	return orchestration.Participant{AgentID: agent.ID, AgentRevision: agent.Revision, RunnerID: runner, Access: agent.Access}
}

// recordTaskAdmission runs after the task's own permission/state checks. Task
// requests, not arbitrary Thread messages, are its explicit input subscription.
func recordTaskAdmission(ctx context.Context, q *sqlgen.Queries, t CollaborationTask, kind orchestration.Kind, request orchestration.RequestRef, messageID string) error {
	decision, err := orchestration.TaskDecision(kind)
	if err != nil {
		return err
	}
	identity, err := json.Marshal([]string{actorID(ctx), request.Scope, request.ID})
	if err != nil {
		return err
	}
	work := orchestration.Work{Kind: "task", ID: t.ID, Revision: t.Revision}
	decision.Work = append(decision.Work, work)
	record := orchestration.Record{Trigger: orchestration.Trigger{Source: "task", EventID: remoteHash(identity), Kind: kind, ActorKind: "user", ActorID: actorID(ctx), RoomID: t.RoomID, ThreadRootID: t.ThreadRootID, MessageID: messageID, TaskID: t.ID, Request: &request, InputRevision: t.InputRevision, OccurredAt: t.UpdatedAt}, Decision: decision, Mandates: []orchestration.Mandate{}}
	if kind == orchestration.TaskStart || kind == orchestration.TaskContinue {
		mandate := orchestration.Mandate{Work: work, RequestedByUserID: t.RequestedByUserID, ResourceID: t.ResourceID, BaseCommit: t.BaseSharedCommit, Participants: []orchestration.Participant{}, MaxRounds: t.MaxTurns, MaxModelRequests: t.MaxModelRequests, MaxTokens: t.Budget.MaxTokens, MaxSteps: t.Budget.MaxSteps, TimeoutSeconds: t.Budget.TimeoutSeconds}
		for _, p := range t.Participants {
			mandate.Participants = append(mandate.Participants, admissionParticipant(p.Agent, p.RunnerID))
		}
		record.Mandates = append(record.Mandates, mandate)
	}
	record.Trigger.SourceEventID = t.SourceEventID
	return recordOrchestration(ctx, q, record)
}

type OrchestrationPage struct {
	Items     []orchestration.Record `json:"items"`
	Through   int64                  `json:"through"`
	NextAfter *int64                 `json:"nextAfter"`
	HasMore   bool                   `json:"hasMore"`
}

// OrchestrationPage reads a stable, bounded journal without copying source text,
// task histories, credentials or model configuration. Transport owns read access.
func (s *Store) OrchestrationPage(ctx context.Context, room string, after int64, through *int64, limit int) (page OrchestrationPage, err error) {
	page.Items = []orchestration.Record{}
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if _, err := q.GetRoom(ctx, room); err != nil {
			return missing(err, "The room")
		}
		frontier, err := q.OrchestrationFrontier(ctx, room)
		if err != nil {
			return err
		}
		page.Through = frontier
		if through != nil {
			page.Through = *through
		}
		if after < 0 || page.Through < after || page.Through > frontier || limit < 0 || limit > OrchestrationPageLimit {
			return problem("invalid_page", "Choose a valid admission range and at most 20 records.")
		}
		if limit == 0 {
			limit = OrchestrationPageLimit
		}
		rows, err := q.OrchestrationPage(ctx, sqlgen.OrchestrationPageParams{RoomID: room, AfterSeq: after, ThroughSeq: page.Through, PageLimit: int64(limit + 1)})
		if err != nil {
			return err
		}
		for i, row := range rows {
			if i == limit {
				break
			}
			var record orchestration.Record
			if err := json.Unmarshal([]byte(row.Value), &record); err != nil {
				return err
			}
			record.Seq = row.Seq
			previous := page
			page.Items = append(page.Items, record)
			page.HasMore = i+1 < len(rows)
			page.NextAfter = nil
			if page.HasMore {
				page.NextAfter = &record.Seq
			}
			// Count the actual final wire shape, including null on the last page.
			encoded, err := json.Marshal(page)
			if err != nil {
				return err
			}
			if len(encoded)+1 > OrchestrationPageBytes {
				page = previous
				if len(page.Items) == 0 {
					return problem("invalid_page", "The stored admission cannot fit in a bounded page.")
				}
				break
			}
		}
		return nil
	})
	return page, err
}
