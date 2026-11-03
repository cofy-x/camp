package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestAutomationHTTPPolicyReceiptsAndVisibility(t *testing.T) {
	f := newTeamFixture(t)
	path := "/api/rooms/" + f.room.ID + "/automation-policies"
	for _, p := range []string{path, "/api/rooms/" + f.room.ID + "/automation-events", "/api/automation-requests/save"} {
		requireHTTP(t, teamRequest(t, f, nil, "GET", p, nil), http.StatusUnauthorized)
	}
	input := orchestration.PolicyInput{RequestID: "save", Mode: "observe", Rules: "A private editing draft becomes shared policy only on Save.", MaxTurns: 6, MaxModelRequests: 24}
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "PUT", path+"/room", input), 403)
	response := teamRequest(t, f, f.adminCookie, "PUT", path+"/room", input)
	requireHTTP(t, response, 200)
	var result struct {
		Policy orchestration.Policy `json:"policy"`
	}
	if e := json.Unmarshal(response.Body.Bytes(), &result); e != nil || result.Policy.Enabled || result.Policy.Mode != "observe" || result.Policy.Revision != 1 {
		t.Fatal(response.Body.String(), e)
	}
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PUT", path+"/room", input), 200)
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "GET", "/api/automation-requests/save", nil), 200)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", "/api/automation-requests/save", nil), 404)
	input.Rules = "Changed intent"
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PUT", path+"/room", input), 409)
	input.RequestID = "stale"
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PUT", path+"/room", input), 409)
	input.Revision = 1
	input.Enabled = true
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PUT", path+"/room", input), 400)
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", path, nil), 200)
	events := "/api/rooms/" + f.room.ID + "/automation-events"
	for _, query := range []string{"after=-1", "through=99999", "limit=21", "after=1&after=2"} {
		requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", events+"?"+query, nil), 400)
	}
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "GET", events, nil), 200)
}

func TestAutomationHTTPCommentCredentialIsPrivateAndReplayable(t *testing.T) {
	f := newTeamFixture(t)
	webhooks, e := configuration.OpenWebhookSecrets(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	f.api.EnableGitHub(webhooks)
	credentials, e := configuration.OpenGitHubCredentials(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	f.api.EnableAutomation(credentials)
	response := teamRequest(t, f, f.adminCookie, "POST", "/api/rooms/"+f.room.ID+"/github-sources", storage.CreateGitHubSourceInput{RequestID: "source", Repository: "acme/project", Secret: strings.Repeat("s", 32)})
	requireHTTP(t, response, 200)
	var created struct {
		Source storage.GitHubSource `json:"source"`
	}
	if e = json.Unmarshal(response.Body.Bytes(), &created); e != nil {
		t.Fatal(e)
	}
	policyPath := "/api/rooms/" + f.room.ID + "/automation-policies/" + created.Source.ID
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PUT", policyPath, orchestration.PolicyInput{RequestID: "policy", Mode: "observe"}), 200)
	path := "/api/github-sources/" + created.Source.ID + "/comment-credential"
	input := storage.AutomationCredentialInput{RequestID: "credential", Revision: 1, Token: "github_pat_private_test_only", ActorID: 123}
	requireHTTP(t, teamRequest(t, f, f.memberCookie, "PUT", path, input), 403)
	response = teamRequest(t, f, f.adminCookie, "PUT", path, input)
	requireHTTP(t, response, 200)
	if strings.Contains(response.Body.String(), input.Token) || strings.Contains(response.Body.String(), "credentialRef") {
		t.Fatal("Private comment credential escaped", response.Body.String())
	}
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PUT", path, input), 200)
	receipt := teamRequest(t, f, f.adminCookie, "GET", "/api/automation-requests/credential", nil)
	requireHTTP(t, receipt, 200)
	if strings.Contains(receipt.Body.String(), input.Token) {
		t.Fatal("Credential escaped in receipt")
	}
	input.Token += "changed"
	requireHTTP(t, teamRequest(t, f, f.adminCookie, "PUT", path, input), 409)
}
