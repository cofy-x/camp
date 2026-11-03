package githubhooks

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"strings"
	"testing"
)

func TestSignatureUsesExactBytesAndSHA256(t *testing.T) {
	body := []byte(`{"title":"你好 🌱"}`)
	mac := hmac.New(sha256.New, []byte("a private webhook secret"))
	mac.Write(body)
	signature := "sha256=" + hex.EncodeToString(mac.Sum(nil))
	if !Verify("a private webhook secret", body, signature) {
		t.Fatal("valid signature rejected")
	}
	for _, s := range []string{"", "sha1=" + signature[7:], signature + "00", "sha256=xyz"} {
		if Verify("a private webhook secret", body, s) {
			t.Fatal("invalid signature accepted")
		}
	}
	if Verify("another secret", body, signature) || Verify("a private webhook secret", append(body, '\n'), signature) {
		t.Fatal("signature did not bind raw bytes and secret")
	}
	if PayloadFingerprint(body) == PayloadFingerprint(append(body, '\n')) {
		t.Fatal("payload identity did not bind exact bytes")
	}
}

func TestPayloadNormalizationAndFilters(t *testing.T) {
	for _, kind := range []string{"issues", "pull_request", "issue_comment"} {
		t.Run(kind, func(t *testing.T) {
			p := map[string]any{"action": "opened", "repository": map[string]any{"id": 123, "full_name": "Acme/Project"}, "sender": map[string]any{"id": 7, "login": "person", "type": "User"}}
			subject := map[string]any{"id": 101, "number": 3, "title": "A title", "body": "  authored\n你好\n ", "updated_at": "2026-11-02T21:00:00+08:00"}
			if kind == "issue_comment" {
				p["action"] = "created"
				p["issue"] = map[string]any{"number": 3, "title": "PR title", "pull_request": map[string]any{}}
				p["comment"] = subject
			} else if kind == "pull_request" {
				p["pull_request"] = subject
			} else {
				p["issue"] = subject
			}
			raw, _ := json.Marshal(p)
			o, err := Parse("acme/project", kind, raw)
			if err != nil || o.IgnoreReason != "" || o.Body != subject["body"] || o.OccurredAt != "2026-11-02T13:00:00Z" {
				t.Fatal(o, err)
			}
			prepared, err := ParseDelivery(kind, raw)
			if err != nil || prepared != o {
				t.Fatal("preparing outside storage changed the immutable observation", prepared, o, err)
			}
			if !strings.HasPrefix(o.URL, "https://github.com/acme/project/") || !strings.Contains(o.Context(), "untrusted context") {
				t.Fatal(o)
			}
			if kind != "issues" && !strings.Contains(o.URL, "/pull/3") {
				t.Fatal(o.URL)
			}
			p["sender"] = map[string]any{"id": 10, "login": "robot[bot]", "type": "Bot"}
			raw, _ = json.Marshal(p)
			o, err = Parse("acme/project", kind, raw)
			if err != nil || o.IgnoreReason != "nonhuman_actor" {
				t.Fatal(o, err)
			}
			if _, err = Parse("other/repository", kind, raw); err == nil {
				t.Fatal("wrong repository accepted")
			}
			subject["body"] = strings.Repeat("x", MaxBodyBytes+1)
			raw, _ = json.Marshal(p)
			if _, err = Parse("acme/project", kind, raw); err == nil {
				t.Fatal("oversized original accepted")
			}
		})
	}
	for _, raw := range []string{"null", "{}", "[]", "{", `{"repository":{"id":1,"full_name":"a/b"},"issue":{"id":2,"number":1,"title":"title"},"action":"opened"}`} {
		o, err := Parse("a/b", "issues", []byte(raw))
		if err == nil && o.IgnoreReason != "nonhuman_actor" {
			t.Fatal("invalid event became pending", o)
		}
	}
}

func TestPreparedDeliveryRejectsInvalidRepositoryNames(t *testing.T) {
	for _, repository := range []string{"a/.", "a/..", "a/../../b", "a/b\n", "https://github.com/a/b"} {
		raw, _ := json.Marshal(map[string]any{"repository": map[string]any{"id": 1, "full_name": repository}})
		if _, err := ParseDelivery("ping", raw); err == nil {
			t.Fatal("unbound preparation accepted an invalid repository name", repository)
		}
	}
}
