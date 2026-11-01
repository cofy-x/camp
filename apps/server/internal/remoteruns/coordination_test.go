package remoteruns

import (
	"bytes"
	"encoding/json"
	"testing"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

func TestCoordinationIntentPreservesJSONAndOwnsItsSlices(t *testing.T) {
	for _, wire := range []*p.CoordinationIntent{
		nil,
		{CallID: "handoff", Kind: "handoff", TargetAgentID: "reviewer", Instruction: "Review the draft.", ArtifactPaths: []string{"guide.md"}},
		{CallID: "question", Kind: "ask_person", Question: "Which title?", Options: []string{"A", "B"}, ArtifactPaths: []string{}},
		{CallID: "finish", Kind: "finish", Summary: "Ready for human review."},
		{CallID: "empty", Kind: "finish", Options: []string{}, ArtifactPaths: []string{}},
	} {
		name := "nil"
		if wire != nil {
			name = wire.CallID
		}
		t.Run(name, func(t *testing.T) {
			stored := coordinationIntent(wire)
			wireJSON, err := json.Marshal(wire)
			if err != nil {
				t.Fatal(err)
			}
			storedJSON, err := json.Marshal(stored)
			if err != nil || !bytes.Equal(wireJSON, storedJSON) {
				t.Fatalf("decision representation changed: %s -> %s: %v", wireJSON, storedJSON, err)
			}
			if wire == nil {
				if stored != nil {
					t.Fatal("absent decision became present", stored)
				}
				return
			}
			if (wire.Options == nil) != (stored.Options == nil) || (wire.ArtifactPaths == nil) != (stored.ArtifactPaths == nil) {
				t.Fatal("nil and empty slices were interchanged", wire, stored)
			}
			if len(wire.Options) > 0 {
				wire.Options[0] = "Changed after conversion"
			}
			if len(wire.ArtifactPaths) > 0 {
				wire.ArtifactPaths[0] = "other.md"
			}
			after, err := json.Marshal(stored)
			if err != nil || !bytes.Equal(storedJSON, after) {
				t.Fatal("decoded Runner result still owns durable decision slices", wire, stored)
			}
		})
	}
}
