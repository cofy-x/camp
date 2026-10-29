package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/remoteruns"
)

func TestSettingsAreAdminOnlyWriteOnlyAndAudited(t *testing.T) {
	f := newTeamFixture(t)
	manager, err := remoteruns.New(context.Background(), f.store, remoteruns.Config{DataDir: t.TempDir()}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = manager.Shutdown(context.Background()) })
	f.api.EnableSettings(manager, configuration.ImportPolicy{Roots: []string{}})
	for _, route := range []string{"models", "environments", "storage", "readiness"} {
		requireHTTP(t, teamRequest(t, f, nil, "GET", "/api/settings/"+route, nil), http.StatusUnauthorized)
		requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/settings/"+route, nil), http.StatusForbidden)
	}
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "PATCH", "/api/settings/models", map[string]any{"apiKey": "denied-private-key"}), http.StatusForbidden)
	response := teamRequest(t, f, f.adminCookie, "PATCH", "/api/settings/models", map[string]any{"apiKey": "managed-private-key", "maxTokens": 2048})
	requireHTTP(t, response, http.StatusOK)
	var view map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &view)
	if view["apiKey"] != nil || view["apiKeyConfigured"] != true || strings.Contains(response.Body.String(), "managed-private-key") {
		t.Fatal("model key was returned")
	}
	response = teamRequest(t, f, f.adminCookie, "GET", "/api/settings/models", nil)
	requireHTTP(t, response, http.StatusOK)
	if strings.Contains(response.Body.String(), "managed-private-key") {
		t.Fatal("model key was readable")
	}
	events, err := f.store.ListWorkspaceEvents(context.Background(), 0)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Type == "management.updated" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("model update was not audited: %+v", events)
	}
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "GET", "/api/settings/readiness", nil), http.StatusOK)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "POST", "/api/settings/readiness/recheck", map[string]any{}), http.StatusNotFound)
	response = teamRequest(t, f, f.adminCookie, "POST", "/api/resources", map[string]any{"name": "Private server files", "path": "/tmp"})
	requireHTTP(t, response, http.StatusForbidden)
	if !strings.Contains(response.Body.String(), "import_disabled") {
		t.Fatal("server import was not explicitly disabled")
	}
}
