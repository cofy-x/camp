package githubchannel

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/orchestration"
)

type fakeCredentials struct{}

func (fakeCredentials) Read(string) (configuration.GitHubCredential, error) {
	return configuration.GitHubCredential{Token: "test-private-github-token", ActorID: 42}, nil
}

func testDelivery() orchestration.Delivery {
	return orchestration.Delivery{ID: "effect-1", Repository: "owner/repo", RepositoryID: 1, Number: 7, ActorID: 42, Body: "Completed the requested work.", Marker: "<!-- camp-effect:effect-1 -->", CredentialRef: "private-reference"}
}

func commentReceipt(body string, actor int64) map[string]any {
	return map[string]any{"id": 99, "body": body, "html_url": "https://github.com/owner/repo/issues/7#issuecomment-99", "user": map[string]any{"id": actor}}
}

func TestDeliverVerifiesAccountRepositoryAndFrozenMessage(t *testing.T) {
	delivery := testDelivery()
	var posts atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer test-private-github-token" || r.Header.Get("X-GitHub-Api-Version") != "2026-03-10" {
			t.Error("missing fixed authentication/version headers")
		}
		switch r.URL.Path {
		case "/repos/owner/repo":
			_ = json.NewEncoder(w).Encode(map[string]int{"id": 1})
		case "/user":
			_ = json.NewEncoder(w).Encode(map[string]int{"id": 42})
		case "/repos/owner/repo/issues/7/comments":
			posts.Add(1)
			if r.Method != http.MethodPost {
				t.Error("unexpected comment read")
			}
			var body struct {
				Body string `json:"body"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Body != delivery.Body+"\n\n"+delivery.Marker {
				t.Error("frozen message or marker changed")
			}
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(commentReceipt(body.Body, 42))
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
		}
	}))
	defer server.Close()
	client := newClient(fakeCredentials{}, server.URL, server.Client())
	result := client.Deliver(context.Background(), delivery)
	if result.Status != "delivered" || result.CommentID != 99 || posts.Load() != 1 {
		t.Fatalf("delivery: %+v, posts=%d", result, posts.Load())
	}
}

func TestLostReceiptReconcilesWithoutAnotherPost(t *testing.T) {
	delivery := testDelivery()
	var posts atomic.Int64
	var mismatch atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/owner/repo":
			_ = json.NewEncoder(w).Encode(map[string]int{"id": 1})
		case "/user":
			_ = json.NewEncoder(w).Encode(map[string]int{"id": 42})
		case "/repos/owner/repo/issues/7/comments":
			if r.Method == http.MethodPost {
				posts.Add(1)
				w.WriteHeader(http.StatusBadGateway)
				_, _ = w.Write([]byte("provider private-token error"))
				return
			}
			body := delivery.Body + "\n\n" + delivery.Marker
			actor := int64(42)
			if mismatch.Load() {
				actor = 1
			}
			_ = json.NewEncoder(w).Encode([]any{commentReceipt(body, actor)})
		}
	}))
	defer server.Close()
	client := newClient(fakeCredentials{}, server.URL, server.Client())
	first := client.Deliver(context.Background(), delivery)
	if first.Status != "unknown" || strings.Contains(first.Reason, "private-token") {
		t.Fatalf("uncertain POST: %+v", first)
	}
	delivery.Reconcile = true
	second := client.Deliver(context.Background(), delivery)
	if second.Status != "delivered" || posts.Load() != 1 {
		t.Fatalf("reconcile: %+v, posts=%d", second, posts.Load())
	}
	mismatch.Store(true)
	third := client.Deliver(context.Background(), delivery)
	if third.Status != "unknown" || posts.Load() != 1 {
		t.Fatalf("foreign actor matched: %+v", third)
	}
}

func TestDeliveryRefusesRedirectsChangedRepositoryAndWrongActor(t *testing.T) {
	for _, mismatch := range []string{"redirect", "repository", "actor"} {
		t.Run(mismatch, func(t *testing.T) {
			var requests atomic.Int64
			var posts atomic.Int64
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method == http.MethodPost {
					posts.Add(1)
				}
				switch {
				case mismatch == "redirect":
					http.Redirect(w, r, "/followed", http.StatusFound)
				case r.URL.Path == "/repos/owner/repo":
					id := 1
					if mismatch == "repository" {
						id = 2
					}
					_ = json.NewEncoder(w).Encode(map[string]int{"id": id})
				case r.URL.Path == "/user":
					_ = json.NewEncoder(w).Encode(map[string]int{"id": 9})
				default:
					t.Error("a rejected destination caused an outgoing comment")
				}
			}))
			defer server.Close()
			result := newClient(fakeCredentials{}, server.URL, server.Client()).Deliver(context.Background(), testDelivery())
			if result.Status != "failed" || posts.Load() != 0 || (mismatch == "redirect" && requests.Load() != 1) {
				t.Fatalf("invalid destination accepted: %+v", result)
			}
		})
	}
}

func TestReconcileNeverRepostsWhenMarkerMissingOrBodyChanged(t *testing.T) {
	delivery := testDelivery()
	delivery.Reconcile = true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("reconciliation mutated GitHub")
		}
		if r.URL.Path == "/repos/owner/repo" {
			_ = json.NewEncoder(w).Encode(map[string]int{"id": 1})
			return
		}
		_ = json.NewEncoder(w).Encode([]any{commentReceipt("changed\n\n"+delivery.Marker, 42)})
	}))
	defer server.Close()
	result := newClient(fakeCredentials{}, server.URL, server.Client()).Deliver(context.Background(), delivery)
	if result.Status != "unknown" {
		t.Fatalf("unexpected result: %+v", result)
	}
}

type failingCredentials struct{}

func (failingCredentials) Read(string) (configuration.GitHubCredential, error) {
	return configuration.GitHubCredential{}, errors.New("private-token unavailable")
}

func TestMissingCredentialsAndMalformedMarkersNeverSend(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Error("unexpected HTTP request") }))
	defer server.Close()
	result := newClient(failingCredentials{}, server.URL, server.Client()).Deliver(context.Background(), testDelivery())
	if result.Status != "failed" || strings.Contains(result.Reason, "private-token") {
		t.Fatal(result)
	}
	delivery := testDelivery()
	delivery.Marker = "untrusted marker"
	if result := newClient(fakeCredentials{}, server.URL, server.Client()).Deliver(context.Background(), delivery); result.Status != "failed" {
		t.Fatal(result)
	}
}

func TestFrozenActorMismatchNeverSendsOrChangesUncertainOutcome(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("mismatched frozen account reached GitHub")
	}))
	defer server.Close()
	client := newClient(fakeCredentials{}, server.URL, server.Client())
	delivery := testDelivery()
	delivery.ActorID = 43
	if result := client.Deliver(context.Background(), delivery); result.Status != "failed" {
		t.Fatal("fresh delivery accepted another credential identity", result)
	}
	delivery.Reconcile = true
	if result := client.Deliver(context.Background(), delivery); result.Status != "unknown" {
		t.Fatal("failed reconciliation erased an uncertain original outcome", result)
	}
}

type rotatedCredentials struct{}

func (rotatedCredentials) Read(ref string) (configuration.GitHubCredential, error) {
	if ref == "current" {
		return configuration.GitHubCredential{Token: "new-private-token", ActorID: 43}, nil
	}
	return configuration.GitHubCredential{}, errors.New("old private credential revoked")
}

func TestReconciliationUsesRotatedReadCredentialAndRetainsBothActorsReceipts(t *testing.T) {
	delivery := testDelivery()
	delivery.Reconcile = true
	delivery.ReconciliationCredentialRef = "current"
	delivery.Attempts = []orchestration.DeliveryAttempt{{ActorID: 42, CredentialRef: "old"}, {ActorID: 43, CredentialRef: "current"}}
	delivery.ActorID = 43
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Method != http.MethodGet || r.Header.Get("Authorization") != "Bearer new-private-token" {
			t.Error("reconciliation sent a POST or used a revoked credential")
		}
		if r.URL.Path == "/repos/owner/repo" {
			_ = json.NewEncoder(w).Encode(map[string]int{"id": 1})
			return
		}
		first := commentReceipt(delivery.Body+"\n\n"+delivery.Marker, 42)
		second := commentReceipt(delivery.Body+"\n\n"+delivery.Marker, 43)
		second["id"] = 100
		second["html_url"] = "https://github.com/owner/repo/issues/7#issuecomment-100"
		foreign := commentReceipt(delivery.Body+"\n\n"+delivery.Marker, 44)
		_ = json.NewEncoder(w).Encode([]any{first, second, foreign})
	}))
	defer server.Close()
	result := newClient(rotatedCredentials{}, server.URL, server.Client()).Deliver(context.Background(), delivery)
	if result.Status != "delivered" || len(result.Receipts) != 2 || result.Receipts[0].ActorID != 42 || result.Receipts[1].ActorID != 43 || result.CommentID != 100 || requests.Load() != 2 {
		t.Fatal("rotation lost original or duplicate comment evidence", result, requests.Load())
	}
}

func TestReconciliationRetainsMatchingReceiptBeforeLaterReadFailure(t *testing.T) {
	delivery := testDelivery()
	delivery.Reconcile = true
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Error("reconciliation posted a comment")
		}
		if r.URL.Path == "/repos/owner/repo" {
			_ = json.NewEncoder(w).Encode(map[string]int{"id": 1})
			return
		}
		if r.URL.Query().Get("page") == "2" {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		comments := make([]any, 100)
		for index := range comments {
			comments[index] = commentReceipt("unrelated", 42)
		}
		comments[0] = commentReceipt(delivery.Body+"\n\n"+delivery.Marker, 42)
		_ = json.NewEncoder(w).Encode(comments)
	}))
	defer server.Close()
	result := newClient(fakeCredentials{}, server.URL, server.Client()).Deliver(context.Background(), delivery)
	if result.Status != "delivered" || len(result.Receipts) != 1 || result.CommentID != 99 || result.Reason == "" || !result.ReceiptScanLimited {
		t.Fatal("a later read failure erased a proven comment", result)
	}
}

func TestReconciliationBoundsCopiedMarkersAndReportsIncompleteReceiptScan(t *testing.T) {
	delivery := testDelivery()
	delivery.Reconcile = true
	var reads atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reads.Add(1)
		if r.Method != http.MethodGet {
			t.Error("reconciliation sent a comment")
		}
		if r.URL.Path == "/repos/owner/repo" {
			_ = json.NewEncoder(w).Encode(map[string]int{"id": 1})
			return
		}
		comments := make([]any, 100)
		for index := range comments {
			item := commentReceipt(delivery.Body+"\n\n"+delivery.Marker, delivery.ActorID)
			item["id"] = 100 + index
			item["html_url"] = "https://github.com/owner/repo/issues/7#issuecomment-" + strconv.Itoa(100+index)
			comments[index] = item
		}
		_ = json.NewEncoder(w).Encode(comments)
	}))
	defer server.Close()
	result := newClient(fakeCredentials{}, server.URL, server.Client()).Deliver(context.Background(), delivery)
	if result.Status != "delivered" || len(result.Receipts) != 32 || !result.ReceiptScanLimited || result.Reason == "" || reads.Load() != 2 {
		t.Fatal("copied markers bypassed receipt bounds or hid omissions", result, reads.Load())
	}
}
