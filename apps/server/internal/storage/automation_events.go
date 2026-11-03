package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func automationEvent(row sqlgen.AutomationEvent) (e orchestration.SourceEvent, err error) {
	err = json.Unmarshal([]byte(row.Value), &e)
	e.Seq = row.Seq
	e.Status = row.Status
	e.Reason = row.Reason
	e.TaskID = row.TaskID
	e.EvaluationID = row.EvaluationID
	e.PolicyRevision = row.PolicyRevision
	return
}
func updateAutomationEvent(ctx context.Context, q *sqlgen.Queries, e orchestration.SourceEvent) error {
	if err := q.UpdateAutomationEvent(ctx, sqlgen.UpdateAutomationEventParams{ID: e.ID, Status: e.Status, EvaluationID: e.EvaluationID, TaskID: e.TaskID, Reason: e.Reason, PolicyRevision: e.PolicyRevision}); err != nil {
		return err
	}
	// Intake already commits the source's own workspace notification. Only
	// asynchronous processing needs another notification before final completion.
	if e.Status == "needs_attention" || e.Status == "evaluating" {
		return event(ctx, q, "automation.event.updated", e.RoomID, e.ID)
	}
	return nil
}

// Source intake, deterministic subscription fan-out and any visible Room
// projection share a transaction. An execution cannot see an unlogged input.
func recordAutomationSourceEvent(ctx context.Context, tx *sql.Tx, e orchestration.SourceEvent, eligible bool) error {
	q := sqlgen.New(tx)
	e.Status = "ignored"
	if eligible {
		e.Status = "pending"
	}
	raw, err := json.Marshal(e)
	if err != nil {
		return err
	}
	e.Seq, err = q.InsertAutomationEvent(ctx, sqlgen.InsertAutomationEventParams{ID: e.ID, RoomID: e.RoomID, SourceID: e.SourceID, ThreadKey: e.ThreadKey, Kind: e.Kind, ActorID: e.ActorID, ActorKind: e.ActorKind, MessageID: e.MessageID, ExternalEventID: e.ExternalEventID, Status: e.Status, Value: string(raw)})
	if err != nil {
		return err
	}
	if !eligible {
		return updateAutomationEvent(ctx, q, e)
	}
	row, policyErr := q.GetAutomationPolicy(ctx, sqlgen.GetAutomationPolicyParams{RoomID: e.RoomID, SourceID: e.SourceID})
	var p orchestration.Policy
	if policyErr == nil {
		p, policyErr = automationPolicy(row)
	} else if !errors.Is(policyErr, sql.ErrNoRows) {
		return policyErr
	}
	if e.SourceID != "room" && automationAnswerCommand(e.Content) {
		e.Status = "ignored"
		e.Reason = "answer_unavailable"
		if policyErr == nil && p.Enabled && p.Mode == "auto" && p.AllowAnswers && slices.Contains(p.AllowedActorIDs, e.ActorID) && automationAuthority(ctx, q, p) == nil {
			handled, err := answerAutomationEvent(ctx, tx, e, p)
			if err != nil {
				return err
			}
			if handled {
				e.Status = "answered"
				e.Reason = "external_answer"
			}
		}
		return updateAutomationEvent(ctx, q, e)
	}
	if err = deliverAutomationEvent(ctx, tx, e); err != nil {
		return err
	}
	if policyErr != nil || !p.Enabled || e.Seq <= p.StartAfter {
		e.Status = "context"
		e.Reason = "no_enabled_policy"
	} else if e.ActorKind != "user" || !slices.Contains(p.AllowedActorIDs, e.ActorID) {
		e.Status = "context"
		e.Reason = "sender_not_allowed"
	}
	return updateAutomationEvent(ctx, q, e)
}

func recordAutomationRoomEvent(ctx context.Context, tx *sql.Tx, m Message, in SendMessageInput, decision orchestration.Decision) error {
	thread := m.ThreadRootID
	if thread == "" {
		thread = "room"
	}
	e := orchestration.SourceEvent{ID: "room:" + m.ID, RoomID: m.RoomID, SourceID: "room", ThreadKey: m.RoomID + ":" + thread, Kind: "room.message", ActorID: m.AuthorUserID, ActorLogin: m.AuthorName, ActorKind: m.AuthorKind, MessageID: m.ID, Content: m.Content, OccurredAt: m.CreatedAt}
	if in.PostOnly {
		e.Reason = "post_only"
	} else if m.AuthorKind != "user" || m.AuthorUserID == "" {
		e.Reason = "non_human"
	}
	if err := recordAutomationSourceEvent(ctx, tx, e, !in.PostOnly && m.AuthorKind == "user" && m.AuthorUserID != ""); err != nil {
		return err
	}
	if decision.Outcome == "accept" {
		e.Status, e.Reason = "accepted", "explicit_run"
		return updateAutomationEvent(ctx, sqlgen.New(tx), e)
	}
	return nil
}
func recordAutomationGitHubEvent(ctx context.Context, tx *sql.Tx, e GitHubEvent) error {
	content := e.Observation.Context()
	if e.Kind == "issue_comment" && automationAnswerCommand(e.Body) {
		content = e.Body
	}
	v := orchestration.SourceEvent{ID: "github:" + e.ID, RoomID: e.RoomID, SourceID: e.SourceID, ThreadKey: fmt.Sprintf("%d:%d", e.RepositoryID, e.Number), Kind: e.Kind + "." + e.Action, ActorID: strconv.FormatInt(e.ActorID, 10), ActorLogin: e.ActorLogin, ActorKind: "bot", ExternalEventID: e.ID, Repository: e.Repository, RepositoryID: e.RepositoryID, Number: e.Number, Content: content, OccurredAt: e.ReceivedAt}
	if e.ActorKind == "User" {
		v.ActorKind = "user"
	}
	if e.Kind == "issue_comment" {
		own, err := automationOwnGitHubOutput(ctx, sqlgen.New(tx), e)
		if err != nil {
			return err
		}
		if own {
			v.Reason = "own_output"
			return recordAutomationSourceEvent(ctx, tx, v, false)
		}
	}
	return recordAutomationSourceEvent(ctx, tx, v, e.Number > 0 && (e.IgnoreReason == "" || e.ActorKind == "Bot"))
}

var automationEffectMarker = regexp.MustCompile(`^<!-- camp-effect:([a-f0-9]{64}) -->$`)

// A delayed webhook can arrive after the policy rotates to another account.
// Match its immutable delivery identity and original sender, not today's token.
func automationOwnGitHubOutput(ctx context.Context, q *sqlgen.Queries, event GitHubEvent) (bool, error) {
	// Only the sender's fixed-size protocol tail identifies the effect. Quoted
	// source data may itself contain any number of marker-shaped strings.
	const markerBytes = len("<!-- camp-effect:") + 64 + len(" -->")
	if len(event.Body) < markerBytes+2 || event.Body[len(event.Body)-markerBytes-2:len(event.Body)-markerBytes] != "\n\n" {
		return false, nil
	}
	marker := automationEffectMarker.FindStringSubmatch(event.Body[len(event.Body)-markerBytes:])
	if marker == nil {
		return false, nil
	}
	row, err := q.GetAutomationDelivery(ctx, marker[1])
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	delivery, err := automationDelivery(row)
	if err != nil {
		return false, err
	}
	ownActor := delivery.ActorID > 0 && delivery.ActorID == event.ActorID
	for _, attempt := range delivery.Attempts {
		ownActor = ownActor || (attempt.ActorID > 0 && attempt.ActorID == event.ActorID)
	}
	return delivery.ID == marker[1] && delivery.Marker == marker[0] && ownActor &&
		delivery.RepositoryID == event.RepositoryID && delivery.Number == event.Number &&
		event.Body == delivery.Body+"\n\n"+delivery.Marker, nil
}

func automationAnswerCommand(content string) bool {
	fields := strings.Fields(content)
	return len(fields) >= 2 && fields[0] == "/camp" && fields[1] == "answer"
}

// Manual confirmation and automatic admission contend on the same immutable
// event inside SQLite, in addition to the legacy one-final-decision constraint.
func markAutomationManualDecision(ctx context.Context, q *sqlgen.Queries, eventID, taskID string) error {
	row, err := q.GetAutomationExternalEvent(ctx, eventID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if row.TaskID != "" && row.TaskID != taskID {
		return problem("event_decided", "This event already admitted a collaboration.")
	}
	e, err := automationEvent(row)
	if err != nil {
		return err
	}
	e.Status = "accepted"
	if taskID == "" {
		e.Status = "ignored"
	}
	e.TaskID = taskID
	e.Reason = "manual_decision"
	return updateAutomationEvent(ctx, q, e)
}
