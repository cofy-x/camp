package githubdelivery

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWorkflowOutcomesOnlyRepairAnExactMarkedSourceFailure(t *testing.T) {
	for _, test := range []struct {
		name            string
		conclusion      string
		job             string
		step            string
		log             string
		logStatus       int
		truncatedLog    bool
		other           bool
		rerun           bool
		rerunDuringLogs bool
		wrongHead       bool
		otherStep       bool
		attempt         int64
		want            string
	}{
		{name: "cancelled", conclusion: "cancelled", want: "ci_inspection"},
		{name: "skipped", conclusion: "skipped", want: "ci_inspection"},
		{name: "timed out", conclusion: "timed_out", want: "ci_inspection"},
		{name: "startup failed", conclusion: "startup_failure", want: "ci_inspection"},
		{name: "required action", conclusion: "action_required", want: "ci_inspection"},
		{name: "logs unavailable", conclusion: "failure", job: "failure", step: sourceCheckName, attempt: 2, want: "ci_inspection"},
		{name: "redirected logs missing", conclusion: "failure", job: "failure", step: sourceCheckName, attempt: 2, log: "missing", logStatus: http.StatusNotFound, want: "ci_inspection"},
		{name: "redirected logs throttled", conclusion: "failure", job: "failure", step: sourceCheckName, attempt: 2, log: "throttled", logStatus: http.StatusTooManyRequests},
		{name: "redirected logs interrupted", conclusion: "failure", job: "failure", step: sourceCheckName, attempt: 2, log: "truncated", truncatedLog: true},
		{name: "unmarked failure", conclusion: "failure", job: "failure", step: sourceCheckName, attempt: 2, log: "package download failed", want: "ci_inspection"},
		{name: "other step", conclusion: "failure", job: "failure", step: "Setup", attempt: 2, log: "CAMP_SOURCE_REPAIR: known failure", want: "ci_inspection"},
		{name: "multiple source steps", conclusion: "failure", job: "failure", step: sourceCheckName, attempt: 2, otherStep: true, log: "CAMP_SOURCE_REPAIR: reported by a previous custom step", want: "ci_inspection"},
		{name: "different head", conclusion: "failure", job: "failure", step: sourceCheckName, attempt: 2, wrongHead: true, log: "CAMP_SOURCE_REPAIR: known failure", want: "ci_inspection"},
		{name: "other required observation failed", conclusion: "failure", job: "failure", step: sourceCheckName, attempt: 2, other: true, log: "CAMP_SOURCE_REPAIR: known failure", want: "ci_inspection"},
		{name: "rerun in progress", conclusion: "failure", attempt: 2, rerun: true},
		{name: "rerun during diagnostics", conclusion: "failure", job: "failure", step: sourceCheckName, attempt: 2, log: "CAMP_SOURCE_REPAIR: stale failure", rerunDuringLogs: true},
		{name: "rerun while logs are missing", conclusion: "failure", job: "failure", step: sourceCheckName, attempt: 2, rerunDuringLogs: true},
		{name: "authorized source check", conclusion: "failure", job: "failure", step: sourceCheckName, attempt: 2, log: "2026-11-18T14:00:02.012Z CAMP_SOURCE_REPAIR: append the release marker", want: "ci_source_failed"},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newProtocol(t)
			for range 3 {
				f.step(t)
			}
			head := f.delivery.HeadSHA
			logReads := 0
			logServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "" {
					t.Error("GitHub credential reached the redirected log download")
				}
				if test.logStatus != 0 {
					w.Header().Set("Retry-After", "120")
					w.WriteHeader(test.logStatus)
				}
				if test.truncatedLog {
					w.Header().Set("Content-Length", "1024")
				}
				_, _ = fmt.Fprintln(w, test.log)
			}))
			t.Cleanup(logServer.Close)
			f.client.http.Transport = logServer.Client().Transport
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.URL.Path {
				case "/repos/acme/notes/actions/runs":
					run := workflowRun{ID: 19, WorkflowID: 11, Attempt: test.attempt, Status: "completed", Conclusion: test.conclusion, SHA: head, Event: "pull_request"}
					runs := []workflowRun{run}
					if test.rerun || (test.rerunDuringLogs && logReads > 0) {
						run.Attempt++
						run.Status, run.Conclusion = "in_progress", ""
						runs = append(runs, run)
					}
					if test.other {
						run.ID, run.WorkflowID, run.Conclusion = 20, 22, "cancelled"
						runs = append(runs, run)
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"total_count": len(runs), "workflow_runs": runs})
				case "/repos/acme/notes/actions/runs/19/attempts/2/jobs":
					jobHead := head
					if test.wrongHead {
						jobHead = strings.Repeat("a", 40)
					}
					steps := []workflowStep{
						{Name: "Set up job", Conclusion: "success"},
						{Name: "Run actions/checkout@pinned", Conclusion: "success"},
						{Name: test.step, Conclusion: "failure"},
						{Name: "Post Run actions/checkout@pinned", Conclusion: "success"},
						{Name: "Complete job", Conclusion: "success"},
					}
					if test.otherStep {
						steps = append(steps, workflowStep{Name: "Another custom step", Conclusion: "success"})
					}
					_ = json.NewEncoder(w).Encode(map[string]any{"total_count": 1, "jobs": []map[string]any{
						{"id": 29, "run_id": 19, "head_sha": jobHead, "name": sourceCheckName, "conclusion": test.job, "steps": steps},
					}})
				case "/repos/acme/notes/actions/jobs/29/logs":
					logReads++
					if test.log == "" {
						http.NotFound(w, r)
						return
					}
					http.Redirect(w, r, logServer.URL, http.StatusFound)
				default:
					f.serve(w, r)
				}
			}))
			t.Cleanup(server.Close)
			f.client.origin = server.URL
			result := f.step(t)
			delivery := result.Delivery
			if result.Effect != "" || f.writes["/pulls/12/merge"] != 0 || delivery.FailureCode != test.want {
				t.Fatal("unexpected observation or external effect", result)
			}
			if test.logStatus == http.StatusTooManyRequests || test.truncatedLog {
				if delivery.Status != "waiting" || delivery.ReadFailures != 1 || delivery.HasRepairEvidence() {
					t.Fatal("a transient log download must retry within its read allowance", result)
				}
				return
			}
			if test.rerun || test.rerunDuringLogs {
				if delivery.Status != "waiting" || delivery.Checks != 1 || (!test.rerunDuringLogs && logReads != 0) {
					t.Fatal("a newer attempt must be allowed to finish", result)
				}
				return
			}
			if delivery.Status != "blocked" || !strings.HasPrefix(delivery.CheckURL, "https://github.com/acme/notes/actions/runs/19") {
				t.Fatal("the blocked observation must link to the exact run", result)
			}
			if test.want == "ci_source_failed" {
				if !delivery.HasRepairEvidence() || delivery.CheckURL != "https://github.com/acme/notes/actions/runs/19/attempts/2" || !strings.Contains(delivery.Diagnostics, "append the release marker") {
					t.Fatal("expected the exact run attempt and bounded failure diagnostic", result)
				}
			} else if delivery.HasRepairEvidence() || delivery.Diagnostics != "" {
				t.Fatal("an unclassified workflow must not admit a repair", result)
			}
		})
	}
}

func TestGitHubReadBackoffUsesProviderHintsWithoutTreatingPermissionAsThrottle(t *testing.T) {
	for _, test := range []struct {
		name    string
		status  int
		hint    string
		reset   bool
		delay   time.Duration
		waiting bool
	}{
		{name: "secondary rate limit", status: 403, hint: "120", delay: 120 * time.Second, waiting: true},
		{name: "primary rate limit", status: 403, reset: true, delay: 90 * time.Second, waiting: true},
		{name: "no rate hint", status: 429, delay: time.Minute, waiting: true},
		{name: "permission", status: 403},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newProtocol(t)
			for range 3 {
				f.step(t)
			}
			before := time.Now()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/repos/acme/notes/actions/runs" {
					f.serve(w, r)
					return
				}
				if test.hint != "" {
					w.Header().Set("Retry-After", test.hint)
				}
				if test.reset {
					w.Header().Set("X-RateLimit-Remaining", "0")
					w.Header().Set("X-RateLimit-Reset", fmt.Sprint(before.Add(test.delay).Unix()))
				}
				w.WriteHeader(test.status)
			}))
			t.Cleanup(server.Close)
			f.client.origin = server.URL
			d := f.step(t).Delivery
			if !test.waiting {
				if d.Status != "blocked" || d.FailureCode != "connection" {
					t.Fatal(d)
				}
				return
			}
			retry, err := time.Parse(time.RFC3339Nano, d.NextCheckAt)
			if err != nil || d.Status != "waiting" || d.ReadFailures != 1 || retry.Before(before.Add(test.delay-time.Second)) {
				t.Fatal(d, retry, err)
			}
			deadline := before.Add(5 * time.Second).UTC()
			f.delivery.Deadline = deadline.Format(time.RFC3339Nano)
			d = f.step(t).Delivery
			if d.NextCheckAt != f.delivery.Deadline || d.ReadFailures != 2 || d.Status != "waiting" {
				t.Fatal("the read may wait only up to the original deadline", d)
			}
		})
	}
}
