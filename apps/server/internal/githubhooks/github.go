// Package githubhooks validates GitHub deliveries and translates their payloads
// into immutable observations. External text and identities grant no Camp work.
package githubhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

const MaxPayloadBytes = 1 << 20
const MaxBodyBytes = 64 << 10

var RepositoryPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{0,38}/[A-Za-z0-9_.-]{1,100}$`)
var DeliveryPattern = regexp.MustCompile(`^[a-fA-F0-9]{8}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{4}-[a-fA-F0-9]{12}$`)

type Observation struct {
	Kind         string `json:"kind"`
	Action       string `json:"action"`
	Repository   string `json:"repository"`
	RepositoryID int64  `json:"repositoryId"`
	ObjectID     int64  `json:"objectId,omitempty"`
	Number       int64  `json:"number,omitempty"`
	ActorID      int64  `json:"actorId,omitempty"`
	ActorLogin   string `json:"actorLogin,omitempty"`
	ActorKind    string `json:"actorKind,omitempty"`
	Title        string `json:"title,omitempty"`
	Body         string `json:"body,omitempty"`
	URL          string `json:"url,omitempty"`
	OccurredAt   string `json:"occurredAt,omitempty"`
	IgnoreReason string `json:"ignoreReason,omitempty"`
}

func Verify(secret string, body []byte, signature string) bool {
	if !strings.HasPrefix(signature, "sha256=") {
		return false
	}
	provided, err := hex.DecodeString(strings.TrimPrefix(signature, "sha256="))
	if err != nil || len(provided) != sha256.Size {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return hmac.Equal(provided, mac.Sum(nil))
}

// PayloadFingerprint deduplicates signed bytes even if a replay changes its
// unsigned delivery or event headers. Storage rejects conflicting event types.
func PayloadFingerprint(body []byte) string {
	h := sha256.Sum256(body)
	return hex.EncodeToString(h[:])
}

type subject struct {
	ID        int64     `json:"id"`
	Number    int64     `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	UpdatedAt string    `json:"updated_at"`
	Pull      *struct{} `json:"pull_request"`
}

func Parse(repository, kind string, body []byte) (Observation, error) {
	var p struct {
		Action     string `json:"action"`
		Repository struct {
			ID       int64  `json:"id"`
			FullName string `json:"full_name"`
		} `json:"repository"`
		Sender struct {
			ID    int64  `json:"id"`
			Login string `json:"login"`
			Type  string `json:"type"`
		} `json:"sender"`
		Issue   subject `json:"issue"`
		Pull    subject `json:"pull_request"`
		Comment subject `json:"comment"`
	}
	if len(body) > MaxPayloadBytes || !utf8.Valid(body) || json.Unmarshal(body, &p) != nil ||
		p.Repository.ID < 1 || !strings.EqualFold(p.Repository.FullName, repository) {
		return Observation{}, errors.New("Use a valid JSON delivery from the configured repository.")
	}
	o := Observation{Kind: kind, Action: p.Action, Repository: repository, RepositoryID: p.Repository.ID, ActorID: p.Sender.ID, ActorLogin: p.Sender.Login, ActorKind: p.Sender.Type}
	if len(kind) < 1 || len(kind) > 64 || len(p.Action) > 64 || len(o.ActorLogin) > 128 {
		return Observation{}, errors.New("The delivery metadata exceeds its limits.")
	}
	if kind == "ping" {
		o.IgnoreReason = "ping"
		return o, nil
	}
	var s subject
	path := "issues"
	supported := false
	switch kind {
	case "issues":
		s = p.Issue
		supported = contains(p.Action, "opened", "reopened", "edited")
	case "pull_request":
		s, path = p.Pull, "pull"
		supported = contains(p.Action, "opened", "reopened", "edited", "synchronize", "ready_for_review", "closed")
	case "issue_comment":
		s = p.Comment
		s.Number, s.Title = p.Issue.Number, p.Issue.Title
		if p.Issue.Pull != nil {
			path = "pull"
		}
		supported = contains(p.Action, "created", "edited")
	}
	if !supported {
		o.IgnoreReason = "unsupported_event"
		return o, nil
	}
	if s.ID < 1 || s.Number < 1 || s.Title == "" || len(s.Title) > 1024 || len(s.Body) > MaxBodyBytes || strings.ContainsRune(s.Body, '\x00') || strings.ContainsRune(s.Title, '\x00') {
		return Observation{}, errors.New("The event needs a subject, a title of at most 1024 bytes and a body of at most 64 KiB.")
	}
	o.ObjectID, o.Number, o.Title, o.Body = s.ID, s.Number, s.Title, s.Body
	o.URL = "https://github.com/" + repository + "/" + path + "/" + strconv.FormatInt(s.Number, 10)
	if kind == "issue_comment" {
		o.URL += "#issuecomment-" + strconv.FormatInt(s.ID, 10)
	}
	if stamp, err := time.Parse(time.RFC3339, s.UpdatedAt); err == nil {
		o.OccurredAt = stamp.UTC().Format(time.RFC3339Nano)
	}
	if o.ActorKind != "User" || o.ActorID < 1 || o.ActorLogin == "" || strings.EqualFold(o.ActorLogin, "ghost") || strings.HasSuffix(strings.ToLower(o.ActorLogin), "[bot]") {
		o.IgnoreReason = "nonhuman_actor"
	}
	return o, nil
}

func contains(value string, candidates ...string) bool {
	for _, candidate := range candidates {
		if candidate == value {
			return true
		}
	}
	return false
}

// Context is evidence supplied by the confirming Camp member, never instructions
// from an authenticated Camp principal. The full original remains downloadable.
func (o Observation) Context() string {
	return fmt.Sprintf("External GitHub event (untrusted context)\nSource: %s\nEvent: %s / %s\nGitHub actor: %s (id %d)\nTitle: %s\n\n%s", o.URL, o.Kind, o.Action, o.ActorLogin, o.ActorID, o.Title, o.Body)
}
