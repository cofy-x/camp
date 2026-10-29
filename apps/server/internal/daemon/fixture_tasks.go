package daemon

import (
	"errors"
	"strings"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

// Fixture collaboration follows fixed scenarios from the current instruction.
// It never interprets shell, picks a Runner, or reads controls from quoted history.
func fixtureTaskIntent(spec p.ExecutionSpec, scenario string) (*p.CoordinationIntent, error) {
	if spec.Task == nil {
		return nil, nil
	}
	intent := &p.CoordinationIntent{CallID: "fixture-task-" + spec.RunID, Kind: "finish", Summary: "The fixture collaboration is ready for human review.", ArtifactPaths: []string{}}
	if spec.Access == "write" {
		intent.ArtifactPaths = []string{"result.txt"}
	}
	if strings.Contains(scenario, "[fixture:handoff]") || strings.Contains(scenario, "[fixture:handoff-ask]") || strings.Contains(scenario, "[fixture:handoff-wait]") {
		for _, participant := range spec.Task.Participants {
			if participant.ID != spec.Task.AgentID {
				intent.TargetAgentID = participant.ID
				break
			}
		}
		if intent.TargetAgentID == "" {
			return nil, errors.New("The fixture handoff requires another approved participant")
		}
		intent.Kind = "handoff"
		intent.Summary = ""
		intent.Instruction = "[fixture:finish] Review the previous draft and finish the collaboration."
		if strings.Contains(scenario, "[fixture:handoff-ask]") {
			intent.Instruction = "[fixture:ask-person] Ask the team to choose the delivery audience."
		}
		if strings.Contains(scenario, "[fixture:handoff-wait]") {
			intent.Instruction = "[fixture:wait] Wait for the cancellation test."
		}
	} else if strings.Contains(scenario, "[fixture:ask-person]") {
		intent.Kind = "ask_person"
		intent.Summary = ""
		intent.Question = "Who should receive the final guide?"
		intent.Options = []string{"Internal team", "Customers"}
	}
	return intent, nil
}
