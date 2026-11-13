package storage

import (
	"slices"
	"strings"
)

// validateIntent checks the round decision using only frozen task/run facts.
// Callers recheck current execution authority inside the completion transaction.
func validateIntent(t CollaborationTask, r Run, i *CoordinationIntent) error {
	if i == nil || !validText(i.CallID, 128, false) || len(i.ArtifactPaths) > 128 {
		return problem("invalid_intent", "A successful collaboration round requires one durable coordination intent.")
	}
	if i.WorkItemCompletion != nil && (i.Kind != "finish" || !t.WorkItemCompletionAuthorized || !validText(i.WorkItemCompletion.Reason, 8192, false)) {
		return problem("invalid_intent", "A completion judgment requires its original Work authorization and a bounded reason.")
	}
	if i.Kind == "finish" && t.WorkItemCompletionAuthorized && i.WorkItemCompletion == nil {
		return problem("invalid_intent", "State whether the authorized Work completion criteria were met and provide the evidence.")
	}
	for _, p := range i.ArtifactPaths {
		if !validTaskPath(p) {
			return problem("invalid_intent", "Artifact paths must be relative workspace files.")
		}
	}
	switch i.Kind {
	case "handoff":
		if _, ok := taskParticipant(t, i.TargetAgentID); !ok || !validText(i.Instruction, MaxRunPromptSize, false) {
			return problem("invalid_intent", "Handoff requires an approved participant and an instruction.")
		}
	case "ask_person":
		if !validText(i.Question, MaxRunPromptSize, false) || len(i.Options) > 8 {
			return problem("invalid_intent", "A question must be nonempty with at most eight options.")
		}
		for _, option := range i.Options {
			if !validText(option, 512, false) {
				return problem("invalid_intent", "Question options must be short nonempty text.")
			}
		}
	case "finish":
		if !validText(i.Summary, 32<<10, false) {
			return problem("invalid_intent", "A finish intent requires a result summary.")
		}
		if r.AgentSnapshot.Access != "write" && t.CheckpointCommit != t.BaseSharedCommit {
			return problem("invalid_intent", "A writer must run final checks before publishing this draft.")
		}
	default:
		return problem("invalid_intent", "Unknown collaboration intent.")
	}
	return nil
}
func validTaskPath(p string) bool {
	return validText(p, 4096, false) && p != "." && !strings.HasPrefix(p, "/") && !strings.ContainsAny(p, "\\\x00") && !slices.Contains(strings.Split(p, "/"), "..") && !strings.Contains(p, "//")
}
