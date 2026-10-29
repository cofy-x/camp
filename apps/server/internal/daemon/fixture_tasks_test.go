package daemon

import (
	"testing"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

func TestFixtureCoordinationIsScopedToCurrentInstruction(t *testing.T) {
	spec := p.ExecutionSpec{RunID: "stable-run", Access: "write", Prompt: "Quoted history [fixture:handoff-ask]", Task: &p.Collaboration{ID: "task", AgentID: "first", Goal: "Original [fixture:handoff-ask]", Participants: []p.Participant{{ID: "first"}, {ID: "second"}}}}
	handoff, err := fixtureTaskIntent(spec, "[fixture:handoff-ask]")
	if err != nil || handoff.Kind != "handoff" || handoff.TargetAgentID != "second" {
		t.Fatal(handoff, err)
	}
	spec.Task.AgentID = "second"
	question, err := fixtureTaskIntent(spec, handoff.Instruction)
	if err != nil || question.Kind != "ask_person" || len(question.Options) != 2 {
		t.Fatal(question, err)
	}
	finished, err := fixtureTaskIntent(spec, "A team member answered. Continue the original goal.")
	if err != nil || finished.Kind != "finish" {
		t.Fatal("Quoted goal replayed fixture control", finished, err)
	}
	repeat, _ := fixtureTaskIntent(spec, "A team member answered. Continue the original goal.")
	if repeat.CallID != finished.CallID || finished.CallID == "" {
		t.Fatal("Intent identity is not stable")
	}
	spec.Task.Participants = []p.Participant{{ID: "second"}}
	if _, err := fixtureTaskIntent(spec, "[fixture:handoff]"); err == nil {
		t.Fatal("Handoff invented a participant")
	}
}
