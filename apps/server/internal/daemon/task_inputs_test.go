package daemon

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

func TestTaskInputReaderKeepsMachineAndReadCredentialsSeparate(t *testing.T) {
	scope := p.TaskInputScope{Version: 1, After: 1, Through: 2}
	token := strings.Repeat("a", 64)
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch r.URL.Path {
		case "/api/daemon/operations/attempt/task-input-capability":
			if r.Header.Get("Authorization") != "Bearer machine" {
				t.Error("wrong machine credential")
			}
			_ = json.NewEncoder(w).Encode(p.TaskInputGrant{Token: token, URL: "/api/daemon/task-inputs/attempt", Scope: scope})
		case "/api/daemon/task-inputs/attempt":
			if r.Header.Get("Authorization") != "Bearer "+token || r.URL.RawQuery != "after=0&limit=1" {
				t.Error("read changed its private scope")
			}
			_ = json.NewEncoder(w).Encode(p.TaskInputPage{Items: []p.TaskInput{{Seq: 1, ID: "original", Content: "Exact original"}}, Through: 2, HasMore: true})
		default:
			t.Error("unexpected path", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	client, err := httpClient(Config{})
	if err != nil {
		t.Fatal(err)
	}
	d := &Daemon{client: client, config: Config{ServerURL: server.URL, Credential: "machine"}}
	reader, err := d.taskInputReader(context.Background(), "attempt", scope)
	if err != nil {
		t.Fatal(err)
	}
	page, err := reader(context.Background(), 0, 1)
	if err != nil || len(page.Items) != 1 || page.Items[0].Content != "Exact original" {
		t.Fatal(page, err)
	}
	for _, bounds := range [][2]int{{-1, 1}, {3, 1}, {0, 21}, {0, 0}} {
		if _, err = reader(context.Background(), int64(bounds[0]), bounds[1]); err == nil {
			t.Fatal("invalid range reached HTTPS", bounds)
		}
	}
	if _, err = d.taskInputReader(context.Background(), "attempt", p.TaskInputScope{Version: 2}); err == nil {
		t.Fatal("future wire protocol was accepted")
	}
	if calls != 2 {
		t.Fatal("rejected ranges or protocols caused network requests", calls)
	}
}

func TestTaskInputReaderRejectsRedirectsAndChangedGrant(t *testing.T) {
	for _, variant := range []string{"origin", "scope", "redirect", "oversize"} {
		t.Run(variant, func(t *testing.T) {
			scope := p.TaskInputScope{Version: 1, Through: 1}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if strings.HasSuffix(r.URL.Path, "task-input-capability") {
					grant := p.TaskInputGrant{Token: strings.Repeat("a", 64), URL: "/api/daemon/task-inputs/attempt", Scope: scope}
					if variant == "origin" {
						grant.URL = "https://outside.invalid/api/daemon/task-inputs/attempt"
					}
					if variant == "scope" {
						grant.Scope.Through = 2
					}
					_ = json.NewEncoder(w).Encode(grant)
					return
				}
				if variant == "redirect" {
					http.Redirect(w, r, "/other", http.StatusTemporaryRedirect)
					return
				}
				_, _ = w.Write([]byte(strings.Repeat(" ", 65537)))
			}))
			defer server.Close()
			client, err := httpClient(Config{})
			if err != nil {
				t.Fatal(err)
			}
			d := &Daemon{client: client, config: Config{ServerURL: server.URL, Credential: "machine"}}
			reader, err := d.taskInputReader(context.Background(), "attempt", scope)
			if variant == "origin" || variant == "scope" {
				if err == nil {
					t.Fatal("changed grant accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err = reader(context.Background(), 0, 1); err == nil {
				t.Fatal("redirect or oversized response accepted")
			}
		})
	}
}
