package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

type AutomationReceipt struct {
	Kind         string                      `json:"kind"`
	Policy       *orchestration.Policy       `json:"policy,omitempty"`
	Delivery     *orchestration.Delivery     `json:"delivery,omitempty"`
	Subscription *orchestration.Subscription `json:"subscription,omitempty"`
}

func automationPolicy(row sqlgen.AutomationPolicy) (p orchestration.Policy, err error) {
	err = json.Unmarshal([]byte(row.Value), &p)
	p.ID, p.RoomID, p.SourceID, p.Revision, p.OwnerUserID = row.ID, row.RoomID, row.SourceID, row.Revision, row.OwnerUserID
	p.Enabled, p.Mode, p.CredentialRef, p.StartAfter = row.Enabled != 0, row.Mode, row.CredentialRef, row.StartAfter
	p.CredentialConfigured = p.CredentialRef != ""
	return
}

func automationReceipt(ctx context.Context, q *sqlgen.Queries, request string, in any) (AutomationReceipt, bool, string, error) {
	if !validText(request, 128, false) {
		return AutomationReceipt{}, false, "", problem("invalid_source", "A stable request ID is required.")
	}
	raw, e := json.Marshal(in)
	if e != nil {
		return AutomationReceipt{}, false, "", e
	}
	hash := remoteHash(raw)
	old, e := q.GetAutomationRequest(ctx, sqlgen.GetAutomationRequestParams{UserID: actorID(ctx), RequestID: request})
	if errors.Is(e, sql.ErrNoRows) {
		return AutomationReceipt{}, false, hash, nil
	}
	if e != nil {
		return AutomationReceipt{}, false, hash, e
	}
	var r AutomationReceipt
	e = json.Unmarshal([]byte(old.Result), &r)
	if e != nil {
		return r, false, hash, e
	}
	if r.Kind == "cancelled" {
		return r, false, hash, problem("request_cancelled", "This unconfirmed operation was cancelled. Keep its draft and use a new request ID for another operation.")
	}
	if old.RequestHash != hash {
		return AutomationReceipt{}, false, hash, problem("request_conflict", "This request ID belongs to another automation operation.")
	}
	return r, true, hash, e
}

// CancelAutomationRequest serializes with every supported automation mutation.
// Either its original receipt wins, or a tombstone rejects any delayed write.
func (s *Store) CancelAutomationRequest(ctx context.Context, id string) (out AutomationReceipt, err error) {
	if !validText(id, 128, false) {
		return out, problem("invalid_source", "A stable request ID is required.")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := requireActor(ctx, q, false); e != nil {
			return e
		}
		old, e := q.GetAutomationRequest(ctx, sqlgen.GetAutomationRequestParams{UserID: actorID(ctx), RequestID: id})
		if e == nil {
			return json.Unmarshal([]byte(old.Result), &out)
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		out.Kind = "cancelled"
		return saveAutomationReceipt(ctx, q, id, "cancelled", out)
	})
	return
}
func saveAutomationReceipt(ctx context.Context, q *sqlgen.Queries, request, hash string, r AutomationReceipt) error {
	raw, e := json.Marshal(r)
	if e != nil {
		return e
	}
	return q.SaveAutomationRequest(ctx, sqlgen.SaveAutomationRequestParams{UserID: actorID(ctx), RequestID: request, RequestHash: hash, Result: string(raw)})
}
func (s *Store) AutomationRequest(ctx context.Context, id string) (AutomationReceipt, error) {
	if e := requireActor(ctx, s.queries(), false); e != nil {
		return AutomationReceipt{}, e
	}
	row, e := s.queries().GetAutomationRequest(ctx, sqlgen.GetAutomationRequestParams{UserID: actorID(ctx), RequestID: id})
	if e != nil {
		return AutomationReceipt{}, missing(e, "The automation operation")
	}
	var r AutomationReceipt
	e = json.Unmarshal([]byte(row.Result), &r)
	return r, e
}
func (s *Store) AutomationPolicies(ctx context.Context, room string) ([]orchestration.Policy, error) {
	if _, e := s.GetRoom(ctx, room); e != nil {
		return nil, e
	}
	rows, e := s.queries().ListAutomationPolicies(ctx, room)
	if e != nil {
		return nil, e
	}
	out := []orchestration.Policy{}
	for _, r := range rows {
		p, e := automationPolicy(r)
		if e != nil {
			return nil, e
		}
		out = append(out, p)
	}
	return out, nil
}

func validateAutomationPolicy(in *orchestration.PolicyInput) error {
	in.Rules = strings.TrimSpace(in.Rules)
	if in.Mode != "observe" && in.Mode != "auto" {
		return problem("invalid_source", "Choose observation or automatic execution.")
	}
	if !validText(in.Rules, 8192, true) || len(in.AllowedActorIDs) > 128 || len(in.ParticipantAgentIDs) > 8 {
		return problem("invalid_source", "The automation policy exceeds its limits.")
	}
	participants := map[string]bool{}
	for _, id := range in.ParticipantAgentIDs {
		if !validText(id, 128, false) || participants[id] {
			return problem("invalid_source", "Choose up to eight distinct collaboration participants.")
		}
		participants[id] = true
	}
	for _, id := range in.AllowedActorIDs {
		if !validText(id, 128, false) {
			return problem("invalid_source", "Use bounded, nonempty allowed sender IDs.")
		}
	}
	if in.MaxTurns == 0 {
		in.MaxTurns = 6
	}
	if in.MaxModelRequests == 0 {
		in.MaxModelRequests = 24
	}
	if in.MaxTurns < 1 || in.MaxTurns > 24 || in.MaxModelRequests < 1 || in.MaxModelRequests > 96 || in.DailyEvaluations < 0 || in.DailyEvaluations > 1000 || in.DailyTasks < 0 || in.DailyTasks > 100 {
		return problem("invalid_source", "Use finite automation and execution limits.")
	}
	if in.Enabled && (in.Rules == "" || len(in.AllowedActorIDs) == 0 || len(in.ParticipantAgentIDs) == 0 || in.DailyEvaluations < 1 || in.DailyTasks < 1) {
		return problem("invalid_source", "Enabled policies need rules, allowed senders, participants and daily limits.")
	}
	return nil
}
func automationSourceAvailable(ctx context.Context, q *sqlgen.Queries, p orchestration.Policy) error {
	if p.SourceID == "room" {
		return nil
	}
	src, e := q.GetGitHubSource(ctx, p.SourceID)
	if e != nil || src.RoomID != p.RoomID || src.Enabled == 0 {
		return problem("source_unavailable", "The automation source is unavailable.")
	}
	return nil
}
func automationAuthority(ctx context.Context, q *sqlgen.Queries, p orchestration.Policy) error {
	if len(p.ParticipantAgentIDs) < 1 || len(p.ParticipantAgentIDs) > 8 {
		return problem("invalid_agent", "An automation policy requires one to eight distinct participants.")
	}
	seen := map[string]bool{}
	for _, id := range p.ParticipantAgentIDs {
		if seen[id] {
			return problem("invalid_agent", "An automation participant may appear only once.")
		}
		seen[id] = true
	}
	owner, e := q.GetUser(ctx, p.OwnerUserID)
	if e != nil || !owner.Enabled {
		return problem("forbidden", "The authorizing member is unavailable.")
	}
	if e = automationSourceAvailable(ctx, q, p); e != nil {
		return e
	}
	room, e := q.GetRoom(ctx, p.RoomID)
	if e != nil {
		return e
	}
	// Task participants use exactly the same current route and access checks as
	// explicitly created collaborations. The rule itself cannot add permissions.
	for _, id := range p.ParticipantAgentIDs {
		row, e := q.GetAgent(ctx, id)
		if e != nil || !row.Enabled {
			return problem("invalid_agent", "An automation participant is unavailable.")
		}
		if _, e = q.GetMembership(ctx, sqlgen.GetMembershipParams{RoomID: p.RoomID, AgentID: id}); e != nil {
			return problem("invalid_agent", "Every automation participant must belong to the Room.")
		}
		agent := agentRow(row)
		runner, e := resolveAgentRunner(ctx, q, room, agent)
		if e != nil {
			return e
		}
		if e = authorizeRunExecution(ctx, q, Run{RequestedByUserID: p.OwnerUserID, RoomID: p.RoomID, AgentID: id, AgentSnapshot: agent, RunnerID: runner.ID}); e != nil {
			return e
		}
	}
	return nil
}
func (s *Store) SaveAutomationPolicy(ctx context.Context, room, source string, in orchestration.PolicyInput) (out orchestration.Policy, err error) {
	// Preserve the canonical body used by existing receipts, then resolve an
	// accepted request before applying today's admission validation.
	in.Rules = strings.TrimSpace(in.Rules)
	if in.MaxTurns == 0 {
		in.MaxTurns = 6
	}
	if in.MaxModelRequests == 0 {
		in.MaxModelRequests = 24
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := requireActor(ctx, q, true); e != nil {
			return e
		}
		old, replay, hash, e := automationReceipt(ctx, q, in.RequestID, []any{"policy", room, source, in})
		if e != nil {
			return e
		}
		if replay {
			out = *old.Policy
			return nil
		}
		if e = validateAutomationPolicy(&in); e != nil {
			return e
		}
		if _, e = q.GetRoom(ctx, room); e != nil {
			return missing(e, "The Room")
		}
		row, e := q.GetAutomationPolicy(ctx, sqlgen.GetAutomationPolicyParams{RoomID: room, SourceID: source})
		exists := e == nil
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		out = orchestration.Policy{ID: newID(), RoomID: room, SourceID: source, Revision: 1, OwnerUserID: actorID(ctx)}
		if exists {
			out, e = automationPolicy(row)
			if e != nil {
				return e
			}
		}
		if in.Revision != out.Revision && !(in.Revision == 0 && !exists) {
			return problem("revision_conflict", "The automation policy changed. Reload it before saving.")
		}
		if exists {
			out.Revision++
		}
		out.OwnerUserID = actorID(ctx)
		out.Enabled = in.Enabled
		out.Mode = in.Mode
		out.Rules = in.Rules
		out.AllowedActorIDs = slices.Clone(in.AllowedActorIDs)
		out.ParticipantAgentIDs = slices.Clone(in.ParticipantAgentIDs)
		out.FirstAgentID = in.FirstAgentID
		out.MaxTurns = in.MaxTurns
		out.MaxModelRequests = in.MaxModelRequests
		out.DailyEvaluations = in.DailyEvaluations
		out.DailyTasks = in.DailyTasks
		out.AllowAnswers = in.AllowAnswers
		out.AllowComments = in.AllowComments
		if source != "room" {
			for _, id := range out.AllowedActorIDs {
				n, e := strconv.ParseInt(id, 10, 64)
				if e != nil || n < 1 || strconv.FormatInt(n, 10) != id {
					return problem("invalid_source", "Use immutable numeric GitHub sender IDs.")
				}
			}
		}
		if out.Enabled {
			if !slices.Contains(out.ParticipantAgentIDs, out.FirstAgentID) {
				return problem("invalid_agent", "Choose the first Agent from the approved participants.")
			}
			if e = automationAuthority(ctx, q, out); e != nil {
				return e
			}
		} else if source != "room" {
			src, err := q.GetGitHubSource(ctx, source)
			if err != nil || src.RoomID != room {
				return problem("invalid_source", "Choose a GitHub source belonging to this Room.")
			}
		}
		// Every edit establishes a new intake frontier. Observation history and
		// events admitted under an older policy are never silently executed later.
		out.StartAfter, e = q.AutomationFrontier(ctx, room)
		if e != nil {
			return e
		}
		raw, _ := json.Marshal(out)
		enabled := int64(0)
		if out.Enabled {
			enabled = 1
		}
		if exists {
			e = q.UpdateAutomationPolicy(ctx, sqlgen.UpdateAutomationPolicyParams{ID: out.ID, Revision: out.Revision, OwnerUserID: out.OwnerUserID, Enabled: enabled, Mode: out.Mode, Value: string(raw), CredentialRef: out.CredentialRef, StartAfter: out.StartAfter})
		} else {
			e = q.InsertAutomationPolicy(ctx, sqlgen.InsertAutomationPolicyParams{ID: out.ID, RoomID: room, SourceID: source, Revision: out.Revision, OwnerUserID: out.OwnerUserID, Enabled: enabled, Mode: out.Mode, Value: string(raw), CredentialRef: out.CredentialRef, StartAfter: out.StartAfter})
		}
		if e != nil {
			return e
		}
		if e = event(ctx, q, "automation.policy.updated", room, out.ID); e != nil {
			return e
		}
		return saveAutomationReceipt(ctx, q, in.RequestID, hash, AutomationReceipt{Kind: "policy", Policy: &out})
	})
	return
}

type AutomationCredentialInput struct {
	RequestID string `json:"requestId"`
	Revision  int64  `json:"revision"`
	Token     string `json:"token"`
	ActorID   int64  `json:"actorId"`
}

// FindAutomationCredential resolves uncertain requests before writing another
// immutable private file. Secret bytes only contribute to the canonical hash.
func (s *Store) FindAutomationCredential(ctx context.Context, source string, in AutomationCredentialInput) (AutomationReceipt, bool, error) {
	if e := requireActor(ctx, s.queries(), true); e != nil {
		return AutomationReceipt{}, false, e
	}
	r, ok, _, e := automationReceipt(ctx, s.queries(), in.RequestID, []any{"credential", source, in})
	return r, ok, e
}
func (s *Store) SetAutomationCredential(ctx context.Context, source string, in AutomationCredentialInput, ref string) (out AutomationReceipt, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := requireActor(ctx, q, true); e != nil {
			return e
		}
		old, replay, hash, e := automationReceipt(ctx, q, in.RequestID, []any{"credential", source, in})
		if e != nil {
			return e
		}
		if replay {
			out = old
			return nil
		}
		src, e := q.GetGitHubSource(ctx, source)
		if e != nil {
			return missing(e, "The GitHub source")
		}
		row, e := q.GetAutomationPolicy(ctx, sqlgen.GetAutomationPolicyParams{RoomID: src.RoomID, SourceID: source})
		if e != nil {
			return missing(e, "The automation policy")
		}
		p, e := automationPolicy(row)
		if e != nil {
			return e
		}
		if p.Revision != in.Revision {
			return problem("revision_conflict", "The automation policy changed.")
		}
		if len(ref) != 48 {
			return problem("invalid_source", "Save a private comment credential first.")
		}
		p.Revision++
		p.CredentialRef = ref
		p.CredentialConfigured = true
		p.CommentActorID = in.ActorID
		raw, _ := json.Marshal(p)
		e = q.UpdateAutomationPolicy(ctx, sqlgen.UpdateAutomationPolicyParams{ID: p.ID, Revision: p.Revision, OwnerUserID: p.OwnerUserID, Enabled: row.Enabled, Mode: p.Mode, Value: string(raw), CredentialRef: ref, StartAfter: p.StartAfter})
		if e != nil {
			return e
		}
		out = AutomationReceipt{Kind: "credential", Policy: &p}
		if e = event(ctx, q, "automation.policy.updated", p.RoomID, p.ID); e != nil {
			return e
		}
		return saveAutomationReceipt(ctx, q, in.RequestID, hash, out)
	})
	return
}
