package storage

import (
	"encoding/json"
	"testing"
)

func TestRunReadsHistoricalCoordinationDecisions(t *testing.T) {
	for _, decision := range []string{
		`{"callId":"handoff","kind":"handoff","targetAgentId":"reviewer","instruction":"Review the draft.","artifactPaths":["guide.md"]}`,
		`{"callId":"question","kind":"ask_person","question":"Which title?","options":["A","B"],"artifactPaths":[]}`,
		`{"callId":"finish","kind":"finish","summary":"Ready for human review.","artifactPaths":null}`,
	} {
		run, err := runFromSnapshot(`{"id":"historical","status":"succeeded","intent":` + decision + `}`)
		if err != nil || run.Intent == nil {
			t.Fatal("historical decision could not be read", run, err)
		}
		data, err := json.Marshal(run.Intent)
		if err != nil || string(data) != decision {
			t.Fatalf("historical decision changed: %s -> %s: %v", decision, data, err)
		}
	}
}
