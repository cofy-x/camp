package runnerprotocol

import (
	"strings"
	"testing"
)

func TestSourceMachineIdentityBoundaries(t *testing.T) {
	for _, id := range []string{"a", "notes_2026-v2", strings.Repeat("a", MaxSourceIDLength)} {
		if !ValidSourceID(id) {
			t.Errorf("valid source identity rejected: %q", id)
		}
	}
	for _, id := range []string{"", "Notes", "1notes", ".", "../notes", "notes.name", "notes space", "文档", strings.Repeat("a", MaxSourceIDLength+1)} {
		if ValidSourceID(id) {
			t.Errorf("invalid source identity accepted: %q", id)
		}
	}
}
