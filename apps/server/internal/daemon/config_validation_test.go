package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

func TestInvalidRunnerCapabilitiesFailBeforeEnrollmentPersistenceOrNetwork(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		_ = json.NewEncoder(w).Encode(p.Joined{RunnerID: "runner"})
	}))
	defer server.Close()
	many := map[string]string{}
	for i := 0; i <= p.MaxSources; i++ {
		many[fmt.Sprintf("source-%d", i)] = "/approved/source"
	}
	for _, test := range []struct {
		name    string
		sources map[string]string
		slots   int
	}{
		{"uppercase alias", map[string]string{"Notes": "/approved/source"}, 1},
		{"long alias", map[string]string{strings.Repeat("a", p.MaxSourceIDLength+1): "/approved/source"}, 1},
		{"path alias", map[string]string{"../notes": "/approved/source"}, 1},
		{"too many aliases", many, 1},
		{"too many slots", nil, p.MaxSlots + 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			config := Config{DataDir: t.TempDir(), ServerURL: server.URL, Executor: "fixture", Sources: test.sources, Slots: test.slots}
			if _, err := Join(context.Background(), config, "unused-token"); err == nil {
				t.Fatal("invalid capabilities consumed enrollment")
			}
			if _, err := LoadConfig(config.DataDir); !os.IsNotExist(err) {
				t.Fatal("invalid capabilities persisted a pending credential", err)
			}
			config.RunnerID, config.Credential, config.StorageID = "runner", "credential", "storage"
			if err := Run(context.Background(), config, io.Discard); err == nil {
				t.Fatal("invalid persisted capabilities entered the reconnect loop")
			}
			if calls.Load() != 0 {
				t.Fatal("invalid capabilities reached the network", calls.Load())
			}
		})
	}
	valid := map[string]string{strings.Repeat("a", p.MaxSourceIDLength): "/approved/source"}
	for i := 1; i < p.MaxSources; i++ {
		valid[fmt.Sprintf("source-%d", i)] = "/approved/source"
	}
	joined, err := Join(context.Background(), Config{DataDir: t.TempDir(), ServerURL: server.URL, Executor: "fixture", Sources: valid, Slots: p.MaxSlots}, "valid-token")
	if err != nil || joined.RunnerID != "runner" || calls.Load() != 1 {
		t.Fatal("valid boundary capabilities were rejected", err, calls.Load())
	}
}
