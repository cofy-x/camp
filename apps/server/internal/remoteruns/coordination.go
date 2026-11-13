package remoteruns

import (
	"slices"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

// coordinationIntent converts the wire decision without retaining mutable
// slices owned by the decoded Runner result. Storage owns business validation.
func coordinationIntent(intent *p.CoordinationIntent) *storage.CoordinationIntent {
	if intent == nil {
		return nil
	}
	out := &storage.CoordinationIntent{
		CallID: intent.CallID, Kind: intent.Kind,
		TargetAgentID: intent.TargetAgentID, Instruction: intent.Instruction,
		Question: intent.Question, Options: slices.Clone(intent.Options),
		Summary: intent.Summary, ArtifactPaths: slices.Clone(intent.ArtifactPaths),
	}
	if intent.WorkItemCompletion != nil {
		out.WorkItemCompletion = &storage.WorkItemCompletion{CriteriaMet: intent.WorkItemCompletion.CriteriaMet, Reason: intent.WorkItemCompletion.Reason}
	}
	return out
}
