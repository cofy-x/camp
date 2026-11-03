// Package githubchannel delivers frozen Camp messages as GitHub issue comments.
// It owns HTTP delivery and reconciliation, never task or execution authority.
package githubchannel

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/githubhooks"
	"github.com/cofy-x/camp/apps/server/internal/orchestration"
)

const maxResponseBytes = 4 << 20
const maxReconciliationPages = 10

type CredentialReader interface {
	Read(string) (configuration.GitHubCredential, error)
}

type Client struct {
	credentials CredentialReader
	client      *http.Client
	baseURL     string
}

func New(credentials CredentialReader) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 15 * time.Second
	return newClient(credentials, "https://api.github.com", &http.Client{Transport: transport})
}

func newClient(credentials CredentialReader, baseURL string, client *http.Client) *Client {
	bounded := *client
	bounded.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &Client{credentials: credentials, baseURL: strings.TrimRight(baseURL, "/"), client: &bounded}
}

type comment struct {
	ID      int64  `json:"id"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	User    struct {
		ID int64 `json:"id"`
	} `json:"user"`
}

// Deliver makes at most one POST. A reconciliation claim performs only GETs;
// absence from a bounded listing cannot prove an earlier POST had no effect.
func (c *Client) Deliver(ctx context.Context, delivery orchestration.Delivery) orchestration.DeliveryResult {
	failed := func(reason string) orchestration.DeliveryResult {
		return orchestration.DeliveryResult{Status: "failed", Reason: reason}
	}
	unknown := func(reason string) orchestration.DeliveryResult {
		return orchestration.DeliveryResult{Status: "unknown", Reason: reason}
	}
	preflightFailure := failed
	if delivery.Reconcile {
		// A failed read cannot establish that an earlier comment was not posted.
		preflightFailure = unknown
	}
	marker := "<!-- camp-effect:" + delivery.ID + " -->"
	if !validID(delivery.ID) || delivery.Marker != marker || !githubhooks.RepositoryPattern.MatchString(delivery.Repository) ||
		delivery.RepositoryID < 1 || delivery.Number < 1 || delivery.ActorID < 1 || len(delivery.Body) == 0 || len(delivery.Body) > 48<<10 ||
		!utf8.ValidString(delivery.Body) || strings.ContainsRune(delivery.Body, 0) {
		return preflightFailure("The frozen GitHub message is invalid.")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	base := "/repos/" + delivery.Repository
	credential, ok := c.repositoryCredential(ctx, delivery, base)
	if !ok {
		return preflightFailure("The private GitHub credential or configured repository could not be verified.")
	}
	body := delivery.Body + "\n\n" + marker
	path := base + "/issues/" + strconv.FormatInt(delivery.Number, 10) + "/comments"
	if delivery.Reconcile {
		actors := map[int64]bool{delivery.ActorID: true}
		for _, attempt := range delivery.Attempts {
			actors[attempt.ActorID] = true
		}
		var receipts []orchestration.DeliveryReceipt
		for page := 1; page <= maxReconciliationPages; page++ {
			var comments []comment
			status, err := c.request(ctx, credential.Token, http.MethodGet, path+"?per_page=100&page="+strconv.Itoa(page), nil, &comments)
			if err != nil || status != http.StatusOK {
				if len(receipts) > 0 {
					result := reconciled(receipts)
					result.Reason = "A matching comment was found, but the remaining bounded history could not be read."
					result.ReceiptScanLimited = true
					return result
				}
				return unknown("GitHub delivery could not be reconciled. No comment was resent.")
			}
			for _, item := range comments {
				if item.Body == body && actors[item.User.ID] && validComment(item, delivery) {
					receipts = append(receipts, commentEvidence(item))
					if len(receipts) == 32 {
						result := reconciled(receipts)
						result.ReceiptScanLimited = true
						result.Reason = "Matching comments were confirmed. Additional matches may remain outside the bounded receipt scan."
						return result
					}
				}
			}
			if len(comments) < 100 {
				break
			}
			if page == maxReconciliationPages && len(receipts) > 0 {
				result := reconciled(receipts)
				result.ReceiptScanLimited = true
				result.Reason = "Matching comments were confirmed. Additional matches may remain outside the bounded receipt scan."
				return result
			}
		}
		if len(receipts) > 0 {
			return reconciled(receipts)
		}
		return unknown("The original GitHub comment was not found. Its delivery remains uncertain; no comment was resent.")
	}
	var actor struct {
		ID int64 `json:"id"`
	}
	if status, err := c.request(ctx, credential.Token, http.MethodGet, "/user", nil, &actor); err != nil || status != http.StatusOK || actor.ID != credential.ActorID {
		return failed("The GitHub credential does not match its configured account.")
	}
	raw, _ := json.Marshal(struct {
		Body string `json:"body"`
	}{body})
	var result comment
	status, err := c.request(ctx, credential.Token, http.MethodPost, path, raw, &result)
	if err != nil || status >= 500 || status == http.StatusRequestTimeout || (status >= 200 && status < 300 && status != http.StatusCreated) {
		return unknown("GitHub did not confirm delivery. Reconcile the original comment before explicitly resending.")
	}
	if status != http.StatusCreated {
		return failed("GitHub rejected this comment. No automatic retry was made.")
	}
	if result.Body != body || result.User.ID != credential.ActorID || !validComment(result, delivery) {
		return unknown("GitHub returned an incomplete delivery receipt. Reconcile the original comment before resending.")
	}
	return delivered(result)
}

// Read-only reconciliation may use a rotated current credential to inspect
// comments written by any retained dispatch identity. A fresh POST always uses
// exactly its frozen account, and cannot silently fall back to another token.
func (c *Client) repositoryCredential(ctx context.Context, delivery orchestration.Delivery, path string) (configuration.GitHubCredential, bool) {
	refs := []string{delivery.CredentialRef}
	actors := map[string]int64{delivery.CredentialRef: delivery.ActorID}
	if delivery.Reconcile {
		refs = append([]string{delivery.ReconciliationCredentialRef}, refs...)
		for index := len(delivery.Attempts) - 1; index >= 0; index-- {
			refs = append(refs, delivery.Attempts[index].CredentialRef)
			actors[delivery.Attempts[index].CredentialRef] = delivery.Attempts[index].ActorID
		}
	}
	seen := make(map[string]bool)
	for _, ref := range refs {
		if ref == "" || seen[ref] {
			continue
		}
		seen[ref] = true
		credential, err := c.credentials.Read(ref)
		currentRead := delivery.Reconcile && ref == delivery.ReconciliationCredentialRef
		if err != nil || credential.Token == "" || credential.ActorID < 1 || (!currentRead && credential.ActorID != actors[ref]) {
			continue
		}
		var repository struct {
			ID int64 `json:"id"`
		}
		if status, err := c.request(ctx, credential.Token, http.MethodGet, path, nil, &repository); err == nil && status == http.StatusOK && repository.ID == delivery.RepositoryID {
			return credential, true
		}
	}
	return configuration.GitHubCredential{}, false
}

func validID(id string) bool {
	if len(id) < 1 || len(id) > 128 {
		return false
	}
	for _, r := range id {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_') {
			return false
		}
	}
	return true
}

func validComment(item comment, delivery orchestration.Delivery) bool {
	u, err := url.Parse(item.HTMLURL)
	if err != nil || item.ID < 1 || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.RawQuery != "" ||
		u.Fragment != "issuecomment-"+strconv.FormatInt(item.ID, 10) {
		return false
	}
	for _, kind := range []string{"issues", "pull"} {
		if strings.EqualFold(u.Path, "/"+delivery.Repository+"/"+kind+"/"+strconv.FormatInt(delivery.Number, 10)) {
			return true
		}
	}
	return false
}

func delivered(item comment) orchestration.DeliveryResult {
	return orchestration.DeliveryResult{Status: "delivered", CommentID: item.ID, URL: item.HTMLURL, Receipts: []orchestration.DeliveryReceipt{commentEvidence(item)}}
}

func commentEvidence(item comment) orchestration.DeliveryReceipt {
	return orchestration.DeliveryReceipt{CommentID: item.ID, ActorID: item.User.ID, URL: item.HTMLURL}
}

func reconciled(receipts []orchestration.DeliveryReceipt) orchestration.DeliveryResult {
	latest := receipts[len(receipts)-1]
	return orchestration.DeliveryResult{Status: "delivered", CommentID: latest.CommentID, URL: latest.URL, Receipts: receipts}
}

func (c *Client) request(ctx context.Context, token, method, path string, body []byte, result any) (int, error) {
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return 0, errors.New("GitHub request could not be created")
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2026-03-10")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.client.Do(request)
	if err != nil {
		return 0, errors.New("GitHub request did not return a receipt")
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, nil
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil || len(data) > maxResponseBytes {
		return response.StatusCode, errors.New("GitHub response exceeded its limit or was interrupted")
	}
	if json.Unmarshal(data, result) != nil {
		return response.StatusCode, errors.New("GitHub returned an invalid response")
	}
	return response.StatusCode, nil
}
