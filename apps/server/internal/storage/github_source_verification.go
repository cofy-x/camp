package storage

import (
	"context"
	"database/sql"
	"errors"
	"slices"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

type GitHubSourceVerification struct {
	SourceRevision int64               `json:"sourceRevision"`
	SourceEnabled  bool                `json:"sourceEnabled"`
	LastReceivedAt string              `json:"lastReceivedAt,omitempty"`
	RuleRevision   int64               `json:"ruleRevision"`
	Mode           string              `json:"mode"`
	RunnerIssue    *WorkExecutionIssue `json:"runnerIssue,omitempty"`
	Sample         *GitHubRuleSample   `json:"sample,omitempty"`
	Delivery       *GitHubRuleDelivery `json:"delivery,omitempty"`
}

type GitHubRuleSample struct {
	Kind    string `json:"kind"`
	ActorID string `json:"actorId"`
	Outcome string `json:"outcome"`
}

type GitHubRuleDelivery struct {
	State      string              `json:"state"`
	Repository string              `json:"repository,omitempty"`
	Branch     string              `json:"branch,omitempty"`
	CIWorkflow string              `json:"ciWorkflow,omitempty"`
	Issue      *WorkExecutionIssue `json:"issue,omitempty"`
}

// VerifyGitHubSource reads saved authorization and bounded local receipts in
// one database snapshot. It does not claim an event or contact a provider.
func (s *Store) VerifyGitHubSource(ctx context.Context, roomID, sourceID string) (out GitHubSourceVerification, err error) {
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := githubActor(ctx, q, true); e != nil {
			return e
		}
		source, e := q.GetGitHubSource(ctx, sourceID)
		if e != nil {
			return missing(e, "The GitHub source")
		}
		if source.RoomID != roomID {
			return missing(sql.ErrNoRows, "The GitHub source")
		}
		out.SourceRevision, out.SourceEnabled, out.Mode = source.Revision, source.Enabled != 0, "off"
		proof, e := q.RecentGitHubSourceProof(ctx, sqlgen.RecentGitHubSourceProofParams{SourceID: sourceID, SourceRevision: source.Revision})
		if e != nil {
			return e
		}
		if len(proof) > 0 {
			out.LastReceivedAt = proof[0].ReceivedAt
		}
		p := orchestration.Policy{}
		row, e := q.GetAutomationPolicy(ctx, sqlgen.GetAutomationPolicyParams{RoomID: roomID, SourceID: sourceID})
		if e == nil {
			p, e = automationPolicy(row)
			if e != nil {
				return e
			}
			out.RuleRevision = p.Revision
			if p.Enabled {
				out.Mode = p.Mode
			}
		} else if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		for _, sample := range proof {
			if sample.Disposition == "pending" && sample.EventSeq != 0 {
				out.Sample = &GitHubRuleSample{Kind: sample.Kind, ActorID: sample.ActorID, Outcome: githubSampleOutcome(sample, out.SourceEnabled, p)}
				break
			}
		}
		if !out.SourceEnabled || !p.Enabled {
			return nil
		}
		if out.RunnerIssue, e = githubVerificationIssue(automationAuthority(ctx, q, p)); e != nil {
			return e
		}
		if p.Mode != "auto" || out.RunnerIssue != nil {
			return nil
		}
		out.Delivery, e = verifyGitHubRuleDelivery(ctx, q, p)
		return e
	})
	return
}

func githubSampleOutcome(sample sqlgen.RecentGitHubSourceProofRow, enabled bool, p orchestration.Policy) string {
	switch {
	case sample.Status == "ignored" || sample.Status == "answered":
		return "recorded_only"
	case !enabled || !p.Enabled:
		return "off"
	case sample.EventSeq <= p.StartAfter:
		return "before_rule"
	case sample.ActorKind != "user" || !slices.Contains(p.AllowedActorIDs, sample.ActorID):
		return "sender_not_allowed"
	default:
		return "eligible"
	}
}

func githubVerificationIssue(err error) (*WorkExecutionIssue, error) {
	var issue *Error
	if errors.As(err, &issue) {
		return &WorkExecutionIssue{Code: issue.Code, Message: issue.Message}, nil
	}
	return nil, err
}

func verifyGitHubRuleDelivery(ctx context.Context, q *sqlgen.Queries, p orchestration.Policy) (*GitHubRuleDelivery, error) {
	if p.OwnerUserID != actorID(ctx) {
		return &GitHubRuleDelivery{State: "private"}, nil
	}
	settings, err := projectSettings(ctx, q, p.OwnerUserID, p.RoomID)
	if err != nil {
		return nil, err
	}
	if settings == nil || !settings.Enabled || settings.RuleID != p.ID || settings.GitHubDelivery == nil {
		return &GitHubRuleDelivery{State: "room_files"}, nil
	}
	if settings.RuleRevision != p.Revision {
		return &GitHubRuleDelivery{State: "stale"}, nil
	}
	d := &GitHubRuleDelivery{State: "saved", Branch: settings.GitHubDelivery.BaseBranch}
	if issue, e := githubVerificationIssue(validateProjectSettings(ctx, q, *settings)); e != nil || issue != nil {
		d.State, d.Issue = "blocked", issue
		return d, e
	}
	// A rule-created matter uses the rule budget, independently of the
	// personal project defaults used by validateProjectSettings.
	repo, _, err := validateGitHubDelivery(ctx, q, p.OwnerUserID, p.ItemBudget.MaxActions, settings.GitHubDelivery)
	if issue, e := githubVerificationIssue(err); e != nil || issue != nil {
		d.State, d.Issue = "blocked", issue
		return d, e
	}
	d.Repository, d.CIWorkflow = repo.Repository, repo.CIWorkflow
	room, err := q.GetRoom(ctx, p.RoomID)
	if err != nil {
		return nil, err
	}
	participants, err := taskParticipants(ctx, q, room, p.ParticipantAgentIDs)
	if err == nil {
		err = workCompletionRunnersCompatible(ctx, q, participants, true)
	}
	if issue, e := githubVerificationIssue(err); e != nil || issue != nil {
		d.State, d.Issue = "blocked", issue
		return d, e
	}
	return d, nil
}
