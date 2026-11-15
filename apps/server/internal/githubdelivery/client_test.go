package githubdelivery

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/resources"
)

type fixtureCredential struct{}

func (fixtureCredential) Read(string) (configuration.GitHubCredential, error) {
	return configuration.GitHubCredential{Token: "private-repository-token", ActorID: 7}, nil
}

type fixtureFiles struct{}

func (fixtureFiles) Changes(context.Context, string, string, string, []string) ([]resources.Change, error) {
	return []resources.Change{{Path: "notes.md", OldSHA: strings.Repeat("a", 40), OldMode: "100644", SHA: strings.Repeat("b", 40), Content: []byte("Checked note"), Mode: "100644"}}, nil
}

type protocolFixture struct {
	private                      bool
	client                       *Client
	delivery                     orchestration.GitHubDelivery
	writes                       map[string]int
	lost                         string
	absent                       bool
	branch, pr, merged, released bool
	ci                           string
	otherHead                    bool
	repositoryID                 int64
}

func newProtocol(t *testing.T) *protocolFixture {
	t.Helper()
	f := &protocolFixture{writes: map[string]int{}, ci: "success", repositoryID: 10, delivery: orchestration.GitHubDelivery{ID: "delivery-one", SourceSelection: "gitignore-v1", WorkItemID: "work-one", Repository: "acme/notes", RepositoryID: 10, ActorID: 7, CIWorkflowID: 11, Spec: orchestration.GitHubDeliverySpec{BaseBranch: "main", Mode: "release", Tag: "v0.1.1"}, Stage: "prepare", Branch: "camp/work-one", ResourceID: "files", BaselineCommit: strings.Repeat("c", 40), ResultCommit: strings.Repeat("d", 40), Title: "Improve notes", CreatedAt: time.Now().UTC().Format(time.RFC3339Nano)}}
	server := httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(server.Close)
	f.client = NewClient(fixtureCredential{}, fixtureFiles{})
	f.client.origin = server.URL
	return f
}
func (f *protocolFixture) pull() map[string]any {
	head := strings.Repeat("e", 40)
	if f.otherHead {
		head = strings.Repeat("f", 40)
	}
	return map[string]any{"number": 12, "html_url": "https://github.com/acme/notes/pull/12", "body": sourceBody(f.delivery), "state": "open", "user": map[string]any{"id": 7}, "head": map[string]any{"sha": head, "ref": f.delivery.Branch, "repo": map[string]any{"id": 10}}, "base": map[string]any{"ref": "main", "repo": map[string]any{"id": 10}}, "merged": f.merged, "merge_commit_sha": strings.Repeat("f", 40)}
}
func (f *protocolFixture) serve(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer private-repository-token" {
		w.WriteHeader(401)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/repos/acme/notes")
	reply := func(value any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(value)
	}
	if r.Method != "GET" {
		f.writes[path]++
	}
	lost := func(stage string) bool {
		if f.lost == stage {
			w.WriteHeader(500)
			return true
		}
		return false
	}
	switch {
	case r.URL.Path == "/user":
		reply(map[string]any{"id": 7})
	case path == "":
		reply(map[string]any{"id": f.repositoryID, "full_name": "acme/notes", "private": f.private, "permissions": map[string]any{"push": true}})
	case path == "/git/ref/heads/main":
		reply(map[string]any{"object": map[string]any{"sha": strings.Repeat("c", 40), "type": "commit"}})
	case path == "/git/commits/"+strings.Repeat("c", 40):
		reply(map[string]any{"tree": map[string]any{"sha": strings.Repeat("a", 40)}})
	case path == "/git/trees/"+strings.Repeat("a", 40):
		reply(map[string]any{"tree": []map[string]any{{"path": "notes.md", "sha": strings.Repeat("a", 40), "mode": "100644", "type": "blob"}, {"path": ".github/workflows/ci.yml", "sha": strings.Repeat("d", 40), "mode": "100644", "type": "blob"}}})
	case path == "/git/blobs":
		reply(map[string]any{"sha": strings.Repeat("b", 40)})
	case path == "/git/trees":
		var input struct {
			Base string           `json:"base_tree"`
			Tree []map[string]any `json:"tree"`
		}
		_ = json.NewDecoder(r.Body).Decode(&input)
		if input.Base != strings.Repeat("a", 40) || len(input.Tree) != 1 || input.Tree[0]["path"] != "notes.md" {
			w.WriteHeader(422)
			return
		}
		reply(map[string]any{"sha": strings.Repeat("b", 40)})
	case path == "/git/commits":
		reply(map[string]any{"sha": strings.Repeat("e", 40)})
	case path == "/git/refs":
		f.branch = !f.absent
		if lost("branch") {
			return
		}
		reply(map[string]any{"object": map[string]any{"sha": strings.Repeat("e", 40), "type": "commit"}})
	case path == "/git/ref/heads/"+f.delivery.Branch:
		if !f.branch {
			w.WriteHeader(404)
			return
		}
		reply(map[string]any{"object": map[string]any{"sha": strings.Repeat("e", 40), "type": "commit"}})
	case path == "/pulls" && r.Method == "POST":
		f.pr = !f.absent
		if lost("pull_request") {
			return
		}
		reply(f.pull())
	case path == "/pulls":
		if f.pr {
			reply([]map[string]any{f.pull()})
		} else {
			reply([]any{})
		}
	case path == "/pulls/12":
		reply(f.pull())
	case path == "/actions/runs":
		reply(map[string]any{"total_count": 1, "workflow_runs": []map[string]any{{"id": 1, "workflow_id": 11, "head_sha": strings.Repeat("e", 40), "event": "pull_request", "status": "completed", "conclusion": f.ci}}})
	case path == "/pulls/12/merge":
		var input map[string]any
		_ = json.NewDecoder(r.Body).Decode(&input)
		if input["sha"] != strings.Repeat("e", 40) || input["merge_method"] != "squash" {
			w.WriteHeader(422)
			return
		}
		f.merged = !f.absent
		if lost("merge") {
			return
		}
		reply(map[string]any{"merged": true, "sha": strings.Repeat("f", 40)})
	case path == "/git/ref/tags/v0.1.1":
		if !f.released {
			w.WriteHeader(404)
			return
		}
		reply(map[string]any{"object": map[string]any{"sha": strings.Repeat("f", 40), "type": "commit"}})
	case path == "/releases":
		f.released = !f.absent
		if lost("release") {
			return
		}
		reply(map[string]any{"html_url": "https://github.com/acme/notes/releases/tag/v0.1.1"})
	case path == "/releases/tags/v0.1.1":
		if !f.released {
			w.WriteHeader(404)
			return
		}
		reply(map[string]any{"html_url": "https://github.com/acme/notes/releases/tag/v0.1.1", "tag_name": "v0.1.1", "body": sourceBody(f.delivery), "author": map[string]any{"id": 7}})
	default:
		w.WriteHeader(404)
	}
}
func (f *protocolFixture) step(t *testing.T) orchestration.GitHubDeliveryResult {
	t.Helper()
	r := f.client.Step(context.Background(), f.delivery, "private-ref")
	f.delivery = r.Delivery
	return r
}
func TestPublicationReconcilesEveryLostVisibleResponseWithoutResending(t *testing.T) {
	for _, lost := range []string{"", "branch", "pull_request", "merge", "release"} {
		t.Run(fmt.Sprintf("lost_%s", lost), func(t *testing.T) {
			f := newProtocol(t)
			f.lost = lost
			for range 8 {
				r := f.step(t)
				if r.Delivery.Status == "succeeded" {
					break
				}
				if r.Delivery.Status != "queued" {
					t.Fatalf("%s: %+v", lost, r)
				}
			}
			if f.delivery.Status != "succeeded" || f.delivery.HeadSHA != strings.Repeat("e", 40) || f.delivery.MergeSHA != strings.Repeat("f", 40) || f.delivery.ReleaseURL == "" {
				t.Fatal(f.delivery)
			}
			for _, path := range []string{"/git/refs", "/pulls", "/pulls/12/merge", "/releases"} {
				if f.writes[path] != 1 {
					t.Fatal("visible effect repeated", path, f.writes)
				}
			}
		})
	}
}
func TestUnknownBranchAbsenceNeverPermitsAnAutomaticResend(t *testing.T) {
	f := newProtocol(t)
	f.step(t)
	f.lost, f.absent = "branch", true
	f.step(t)
	if f.delivery.Status != "unknown" || !f.delivery.Reconcile {
		t.Fatal(f.delivery)
	}
	for range 2 {
		f.step(t)
	}
	if f.writes["/git/refs"] != 1 {
		t.Fatal("uncertain branch was resent", f.writes)
	}
}
func TestPublicationChecksExactHeadAndCurrentRepositoryIdentity(t *testing.T) {
	for _, failure := range []string{"ci", "head", "repository", "visibility"} {
		t.Run(failure, func(t *testing.T) {
			f := newProtocol(t)
			for range 3 {
				f.step(t)
			}
			switch failure {
			case "ci":
				f.ci = "failure"
			case "head":
				f.otherHead = true
			case "repository":
				f.repositoryID = 99
			case "visibility":
				f.private = true
			}
			f.step(t)
			if f.delivery.Status != "blocked" || f.writes["/pulls/12/merge"] != 0 || f.writes["/releases"] != 0 {
				t.Fatal(f.delivery, f.writes)
			}
		})
	}
}
func TestMissingRequiredWorkflowWaitsWithoutMerging(t *testing.T) {
	f := newProtocol(t)
	for range 3 {
		f.step(t)
	}
	f.delivery.CIWorkflowID = 99
	f.step(t)
	if f.delivery.Status != "waiting" || f.delivery.Checks != 1 || f.writes["/pulls/12/merge"] != 0 {
		t.Fatal(f.delivery, f.writes)
	}
}
