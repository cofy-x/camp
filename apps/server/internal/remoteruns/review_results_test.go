package remoteruns

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/projectenv"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestSuccessfulResultRejectsContradictoryCheckExitCode(t *testing.T) {
	profile := projectenv.Profile{Kind: "command", Checks: []projectenv.Check{{Name: "verify", Arguments: []string{"true"}}}}
	environment, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	run := storage.Run{AgentSnapshot: storage.Agent{Access: "write"}, Environment: environment}
	zero, failure := 0, 1
	stamp := time.Now().UTC().Format(time.RFC3339Nano)
	result := p.Result{CompletionID: "complete", Status: "succeeded", ExitCode: &zero, TestExitCode: &failure, CleanupConfirmed: true, Archive: &p.Artifact{SHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Bytes: 1024}, Checks: []p.CheckResult{{Name: "verify", Command: projectenv.CheckCommand(profile.Checks[0]), Status: "passed", ExitCode: &zero, StartedAt: stamp, FinishedAt: stamp}}}
	if err := validateResult(run, result); err == nil {
		t.Fatal("a successful result with a failing aggregate check exit code was accepted")
	}
}
