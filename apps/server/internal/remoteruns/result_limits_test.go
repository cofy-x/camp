package remoteruns

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/projectenv"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestResultValidationMatchesDurableTextAndCheckLimits(t *testing.T) {
	for _, result := range []p.Result{
		{CompletionID: "  ", Status: "failed"},
		{CompletionID: "bad\x00id", Status: "failed"},
		{CompletionID: "valid", Status: "failed", Error: "bad\x00error"},
	} {
		if validateResult(storage.Run{}, result) == nil {
			t.Fatal("result could pass protocol validation but fail durable validation")
		}
	}
	profile := projectenv.Profile{Kind: "command"}
	result := p.Result{CompletionID: "checks", Status: "failed"}
	zero := 0
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	for i := 0; i < 8; i++ {
		name := fmt.Sprintf("check-%d", i)
		profile.Checks = append(profile.Checks, projectenv.Check{Name: name, Arguments: []string{"true"}})
		result.Checks = append(result.Checks, p.CheckResult{Name: name, Command: "true", Status: "passed", ExitCode: &zero, Output: strings.Repeat("x", 40<<10), StartedAt: stamp, FinishedAt: stamp})
	}
	environment, _ := json.Marshal(profile)
	if validateResult(storage.Run{AgentSnapshot: storage.Agent{Access: "write"}, Environment: environment}, result) == nil {
		t.Fatal("individually bounded checks exceeded the combined durable limit")
	}
}
