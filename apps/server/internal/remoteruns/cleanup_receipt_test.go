package remoteruns

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

func TestSuspendedRunnerCanReplayOnlyTerminalReceipts(t *testing.T) {
	m, store, _, attempt, _ := proxyFixture(t, "http://127.0.0.1:1")
	server := httptest.NewTLSServer(m.Handler())
	defer server.Close()
	result := p.Result{CompletionID: "cleanup-failed", Status: "interrupted", Error: "Could not confirm cleanup.", CleanupConfirmed: false}
	encoded, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	request := func(method, path string, body []byte) (int, []byte) {
		t.Helper()
		r, err := http.NewRequest(method, server.URL+path, bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("Authorization", "Bearer "+strings.Repeat("c", 64))
		response, err := server.Client().Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return response.StatusCode, data
	}
	complete := "/api/daemon/operations/" + attempt.ID + "/complete"
	for i := 0; i < 2; i++ {
		status, data := request("POST", complete, encoded)
		var receipt p.Receipt
		if err = json.Unmarshal(data, &receipt); err != nil || status != 200 || receipt.Disposition != "committed" || receipt.Status != "interrupted" {
			t.Fatalf("completion %d: status=%d receipt=%+v err=%v", i, status, receipt, err)
		}
	}
	runner, err := store.GetRunner(context.Background(), attempt.RunnerID)
	if err != nil || runner.Enabled {
		t.Fatal("runner remained available after uncertain cleanup", err)
	}
	if status, _ := request("GET", "/api/daemon/connect", nil); status != 403 {
		t.Fatalf("disabled runner could connect: %d", status)
	}
	if status, _ := request("POST", "/api/daemon/operations/unknown/finalizing", []byte(`{}`)); status != 403 {
		t.Fatalf("disabled runner accessed an unknown operation: %d", status)
	}
	result.Error = "Different manifest under the same identity."
	changed, _ := json.Marshal(result)
	status, data := request("POST", complete, changed)
	var receipt p.Receipt
	if err = json.Unmarshal(data, &receipt); err != nil || status != 200 || receipt.Disposition != "rejected" {
		t.Fatalf("suspended replay skipped content validation: status=%d receipt=%+v err=%v", status, receipt, err)
	}
}
