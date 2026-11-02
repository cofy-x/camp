package orchestration

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
	"unicode/utf8"
)

type Member struct{ ID, Handle string }

var (
	ErrTooManyRecipients  = errors.New("address at most 16 agents, including mentions")
	ErrPostOnlyRecipients = errors.New("a post-only message cannot address an agent")
)

// RoomInput contains explicit user choices and the current Room membership.
// Membership, enabled identities, Runner grants and file references are checked
// again by storage in the same transaction that creates the work.
type RoomInput struct {
	Content           string
	RecipientAgentIDs []string
	PostOnly          bool
	Members           []Member
}

// PlanRoomMessage resolves explicit recipients without granting execution. Only
// storage can turn this plan into a committed Decision after checking authority.
// Message bodies never become task commands.
func PlanRoomMessage(actorKind string, input RoomInput) (Decision, error) {
	d := newDecision("ignore", "non_human")
	if actorKind != "user" {
		return d, nil
	}
	if input.PostOnly {
		if len(input.RecipientAgentIDs) > 0 {
			return Decision{}, ErrPostOnlyRecipients
		}
		d.Reason = "post_only"
		return d, nil
	}
	d.RecipientAgentIDs = slices.Clone(input.RecipientAgentIDs)
	for _, member := range input.Members {
		if MentionsHandle(input.Content, member.Handle) {
			d.RecipientAgentIDs = append(d.RecipientAgentIDs, member.ID)
		}
	}
	slices.Sort(d.RecipientAgentIDs)
	d.RecipientAgentIDs = slices.Compact(d.RecipientAgentIDs)
	if len(d.RecipientAgentIDs) > MaxRecipients {
		return Decision{}, ErrTooManyRecipients
	}
	if len(d.RecipientAgentIDs) == 0 {
		d.RecipientAgentIDs = []string{}
		d.Reason = "discussion"
		return d, nil
	}
	d.Outcome, d.Reason = "accept", "explicit_request"
	return d, nil
}

// TaskDecision describes a command already authorized by the task state machine.
// It classifies the committed effect; it cannot authorize or schedule a command.
func TaskDecision(kind Kind) (Decision, error) {
	d := newDecision("", "")
	switch kind {
	case TaskStart:
		d.Outcome, d.Reason = "accept", "explicit_goal"
	case TaskInput:
		d.Outcome, d.Reason = "context", "task_input"
	case TaskAnswer:
		d.Outcome, d.Reason = "context", "task_answer"
	case TaskContinue:
		d.Outcome, d.Reason = "accept", "explicit_continuation"
	case TaskCancel:
		d.Outcome, d.Reason = "stop", "explicit_cancellation"
	default:
		return Decision{}, fmt.Errorf("unsupported orchestration event kind %q", kind)
	}
	return d, nil
}

func newDecision(outcome, reason string) Decision {
	return Decision{PolicyVersion: PolicyVersion, Outcome: outcome, Reason: reason, RecipientAgentIDs: []string{}, Work: []Work{}}
}

// MentionsHandle requires token boundaries, so email addresses and longer
// handles do not address a member. Human notifications use the same grammar.
func MentionsHandle(content, handle string) bool {
	if handle == "" {
		return false
	}
	content = strings.ToLower(content)
	target := "@" + strings.ToLower(handle)
	for offset := 0; offset < len(content); {
		i := strings.Index(content[offset:], target)
		if i < 0 {
			return false
		}
		i += offset
		end := i + len(target)
		previous, _ := utf8.DecodeLastRuneInString(content[:i])
		next, _ := utf8.DecodeRuneInString(content[end:])
		if (i == 0 || !handleCharacter(previous)) && (end == len(content) || !handleCharacter(next)) {
			return true
		}
		offset = end
	}
	return false
}

func handleCharacter(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '-' || r == '@'
}
