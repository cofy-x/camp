package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func projectSettings(ctx context.Context, q *sqlgen.Queries, owner, room string) (*orchestration.ProjectSettings, error) {
	row, err := q.ProjectSettings(ctx, sqlgen.ProjectSettingsParams{OwnerUserID: owner, RoomID: room})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var p orchestration.ProjectSettings
	if err = json.Unmarshal([]byte(row.Value), &p); err != nil {
		return nil, err
	}
	p.RoomID, p.Revision = room, row.Revision
	return &p, nil
}

func validateProjectSettings(ctx context.Context, q *sqlgen.Queries, p orchestration.ProjectSettings) error {
	if _, err := q.GetRoom(ctx, p.RoomID); err != nil {
		return missing(err, "The Room")
	}
	if !p.Enabled {
		return nil
	}
	if err := validateWorkBudget(p.Budget); err != nil {
		return err
	}
	if p.CompletionMode != "automatic" && p.CompletionMode != "human" {
		return problem("invalid_project", "Choose automatic completion or human confirmation.")
	}
	members, err := q.ListMembers(ctx, p.RoomID)
	if err != nil {
		return err
	}
	if len(p.ParticipantAgentIDs) < 1 || len(p.ParticipantAgentIDs) > 8 {
		return problem("invalid_project", "Choose one to eight Room Agents.")
	}
	seen := map[string]bool{}
	for _, id := range p.ParticipantAgentIDs {
		available := false
		for _, member := range members {
			available = available || member.ID == id && member.Enabled
		}
		if !available || seen[id] {
			return problem("invalid_project", "Choose distinct enabled Agents in this Room.")
		}
		seen[id] = true
	}
	found := false
	for _, id := range p.ParticipantAgentIDs {
		found = found || id == p.FirstAgentID
	}
	if !found {
		return problem("invalid_project", "Choose a first Agent from the approved participants.")
	}
	if p.GitHubDelivery != nil {
		if p.CompletionMode != "automatic" {
			return problem("invalid_project", "GitHub delivery requires automatic completion.")
		}
		if _, _, err = validateGitHubDelivery(ctx, q, orchestration.WorkItem{OwnerUserID: actorID(ctx), Budget: p.Budget}, p.GitHubDelivery); err != nil {
			return err
		}
	}
	if p.RuleID != "" {
		row, err := q.GetAutomationPolicyID(ctx, p.RuleID)
		if err != nil {
			return missing(err, "The rule")
		}
		policy, err := automationPolicy(row)
		if err != nil {
			return err
		}
		if policy.OwnerUserID != actorID(ctx) || policy.RoomID != p.RoomID {
			return problem("forbidden", "Choose your own rule in this Room.")
		}
		if policy.Revision != p.RuleRevision {
			return problem("revision_conflict", "The rule changed. Review its current authorization.")
		}
		if !policy.Enabled || policy.Mode != "auto" {
			return problem("invalid_project", "Choose an enabled automatic rule.")
		}
		if p.GitHubDelivery == nil {
			return problem("invalid_project", "Choose a delivery destination before authorizing rule delivery.")
		}
		if p.GitHubDelivery.Mode == "release" && p.GitHubDelivery.VersionLine == "" {
			return problem("invalid_project", "Recurring release delivery needs a source version within an approved release line.")
		}
		return automationAuthority(ctx, q, policy)
	}
	return nil
}

func (s *Store) SaveProjectSettings(ctx context.Context, room string, in orchestration.ProjectSettingsInput) (out orchestration.ProjectSettingsReceipt, err error) {
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if err := requireActor(ctx, q, false); err != nil {
			return err
		}
		if !validText(in.RequestID, 128, false) {
			return problem("invalid_project", "A stable request ID is required.")
		}
		if in.RoomID != room {
			return problem("invalid_project", "Save settings in their original Room.")
		}
		raw, _ := json.Marshal(in)
		hash := remoteHash(raw)
		old, err := q.ProjectSettingRequest(ctx, sqlgen.ProjectSettingRequestParams{OwnerUserID: actorID(ctx), RequestID: in.RequestID})
		if err == nil {
			if old.RequestHash == "" {
				return problem("request_cancelled", "This settings operation was cancelled.")
			}
			if old.RequestHash != hash {
				return problem("request_conflict", "This request belongs to another settings change.")
			}
			return json.Unmarshal([]byte(old.Result), &out)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		current, err := projectSettings(ctx, q, actorID(ctx), room)
		if err != nil {
			return err
		}
		if (current == nil && in.Revision != 0) || (current != nil && current.Revision != in.Revision) {
			return problem("revision_conflict", "Project settings changed. Reload them before saving.")
		}
		if err = validateProjectSettings(ctx, q, in.ProjectSettings); err != nil {
			return err
		}
		p := in.ProjectSettings
		p.Revision++
		raw, _ = json.Marshal(p)
		if current == nil {
			err = q.InsertProjectSettings(ctx, sqlgen.InsertProjectSettingsParams{OwnerUserID: actorID(ctx), RoomID: room, Value: string(raw)})
		} else {
			_, err = q.SaveProjectSettings(ctx, sqlgen.SaveProjectSettingsParams{OwnerUserID: actorID(ctx), RoomID: room, Revision: in.Revision, Value: string(raw)})
		}
		if err != nil {
			return err
		}
		out = orchestration.ProjectSettingsReceipt{Kind: "saved", Settings: &p}
		raw, _ = json.Marshal(out)
		if err = q.InsertProjectSettingRequest(ctx, sqlgen.InsertProjectSettingRequestParams{OwnerUserID: actorID(ctx), RequestID: in.RequestID, RequestHash: hash, Result: string(raw)}); err != nil {
			return err
		}
		return event(ctx, q, "project.updated", room, room)
	})
	return
}

func (s *Store) ProjectSettingRequest(ctx context.Context, id string, cancel bool) (out orchestration.ProjectSettingsReceipt, err error) {
	transaction := s.readTransaction
	if cancel {
		transaction = s.transaction
	}
	err = transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if err := requireActor(ctx, q, false); err != nil {
			return err
		}
		row, err := q.ProjectSettingRequest(ctx, sqlgen.ProjectSettingRequestParams{OwnerUserID: actorID(ctx), RequestID: id})
		if errors.Is(err, sql.ErrNoRows) && cancel {
			if !validText(id, 128, false) {
				return problem("invalid_project", "A stable request ID is required.")
			}
			out.Kind = "cancelled"
			raw, _ := json.Marshal(out)
			return q.InsertProjectSettingRequest(ctx, sqlgen.InsertProjectSettingRequestParams{OwnerUserID: actorID(ctx), RequestID: id, Result: string(raw)})
		}
		if err != nil {
			return missing(err, "The settings operation")
		}
		return json.Unmarshal([]byte(row.Result), &out)
	})
	return
}

// Only admission through this exact, current personal rule grant can inherit
// delivery. Ordinary defaults and untrusted source text confer no authority.
func projectRuleDelivery(ctx context.Context, q *sqlgen.Queries, w *orchestration.WorkItem, candidate orchestration.Candidate) error {
	policy := candidate.Policy
	owner, err := q.GetUser(ctx, w.OwnerUserID)
	if err != nil || !owner.Enabled {
		return problem("forbidden", "The project owner is unavailable.")
	}
	ctx = WithUser(ctx, userRow(owner))
	p, err := projectSettings(ctx, q, w.OwnerUserID, w.RoomID)
	if err != nil || p == nil || !p.Enabled || p.RuleID != policy.ID || p.RuleRevision != policy.Revision {
		return err
	}
	if p.GitHubDelivery == nil {
		return nil
	}
	if err = validateProjectSettings(ctx, q, *p); err != nil {
		return err
	}
	w.CompletionMode = "automatic"
	w.ProjectRevision = p.Revision
	if w.CompletionCriteria == "" {
		w.CompletionCriteria = w.Goal
	}
	if err = authorizeGitHubDelivery(ctx, q, w, p.GitHubDelivery); err != nil {
		return err
	}
	row, err := q.GitHubDelivery(ctx, w.ID)
	if err != nil {
		return err
	}
	d, err := githubDeliveryValue(row)
	if err != nil {
		return err
	}
	d.ProjectRevision, d.PolicyRevision = p.Revision, policy.Revision
	for _, event := range candidate.Events {
		if event.SourceID != policy.SourceID || event.RepositoryID != d.RepositoryID ||
			event.Number == 0 || !strings.HasPrefix(event.Kind, "issues.") {
			continue
		}
		issue, err := githubEvent(ctx, q, event.ExternalEventID)
		if err != nil {
			return err
		}
		d.IssueNumber = event.Number
		if title := strings.Join(strings.Fields(issue.Title), " "); title != "" {
			d.Title = truncateUTF8(title, 180)
		}
		break
	}
	return saveGitHubDelivery(ctx, q, &d)
}
