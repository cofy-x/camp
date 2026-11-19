package githubdelivery

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type workflowState uint8

const (
	workflowPending workflowState = iota
	workflowPassed
	workflowNeedsInspection
	workflowSourceCandidate
	workflowSourceFailure
)

type workflowRun struct {
	ID         int64  `json:"id"`
	WorkflowID int64  `json:"workflow_id"`
	Attempt    int64  `json:"run_attempt"`
	SHA        string `json:"head_sha"`
	Event      string `json:"event"`
	Status     string `json:"status"`
	Conclusion string `json:"conclusion"`
}

type workflowCheck struct {
	state       workflowState
	run         workflowRun
	reason      string
	diagnostics string
}

func (c workflowCheck) url(repository string) string {
	if c.run.ID < 1 {
		return ""
	}
	address := fmt.Sprintf("https://github.com/%s/actions/runs/%d", repository, c.run.ID)
	if c.run.Attempt > 0 {
		address += fmt.Sprintf("/attempts/%d", c.run.Attempt)
	}
	return address
}

func (c *Client) checks(ctx context.Context, token, base, sha string, workflowID int64, event string) (workflowCheck, error) {
	check, err := c.observeWorkflows(ctx, token, base, sha, workflowID, event)
	if err != nil || check.state != workflowSourceCandidate {
		return check, err
	}
	diagnostic, err := c.sourceFailure(ctx, token, base, check.run)
	if _, retry := githubReadRetry(err, time.Now()); retry {
		return workflowCheck{}, err
	}
	// The failed attempt can be superseded while its logs are being read.
	// Verify the current exact-head snapshot before blocking or repairing.
	current, err := c.observeWorkflows(ctx, token, base, sha, workflowID, event)
	if err != nil || current.state != workflowSourceCandidate {
		return current, err
	}
	if current.run != check.run {
		return workflowCheck{}, nil
	}
	if diagnostic == "" {
		check.state = workflowNeedsInspection
		return check, nil
	}
	check.state, check.diagnostics = workflowSourceFailure, diagnostic
	return check, nil
}

func (c *Client) observeWorkflows(ctx context.Context, token, base, sha string, workflowID int64, event string) (workflowCheck, error) {
	if !validSHA(sha) || workflowID < 1 {
		return workflowCheck{}, evidenceError{"Review the authorized source and required workflow before accepting its checks."}
	}
	var response struct {
		Total int           `json:"total_count"`
		Runs  []workflowRun `json:"workflow_runs"`
	}
	path := base + "/actions/runs?per_page=100&head_sha=" + sha + "&event=" + url.QueryEscape(event)
	if err := c.call(ctx, token, "GET", path, nil, &response); err != nil {
		return workflowCheck{}, err
	}
	if response.Total > 100 {
		return workflowCheck{}, evidenceError{"The exact-source workflow history exceeds the bounded check reader. Review the CI results."}
	}
	latest := make(map[int64]workflowRun)
	for _, run := range response.Runs {
		if run.ID < 1 || run.WorkflowID < 1 || run.SHA != sha || run.Event != event {
			return workflowCheck{}, evidenceError{"The workflow evidence does not match the authorized source and event."}
		}
		prior := latest[run.WorkflowID]
		if run.ID > prior.ID || run.ID == prior.ID && run.Attempt > prior.Attempt {
			latest[run.WorkflowID] = run
		}
	}
	if _, found := latest[workflowID]; !found {
		return workflowCheck{}, nil
	}
	var unsuccessful workflowRun
	failures := 0
	for _, run := range latest {
		if run.Status != "completed" {
			return workflowCheck{}, nil
		}
		if run.Conclusion != "success" {
			failures++
			if unsuccessful.ID == 0 || run.WorkflowID == workflowID || unsuccessful.WorkflowID != workflowID && run.ID < unsuccessful.ID {
				unsuccessful = run
			}
		}
	}
	if failures == 0 {
		return workflowCheck{state: workflowPassed}, nil
	}
	check := workflowCheck{
		state:  workflowNeedsInspection,
		run:    unsuccessful,
		reason: workflowInspectionReason(unsuccessful.Conclusion),
	}
	// Only the configured workflow can report a source failure eligible for
	// repair. Other workflow results still gate merge on the current head.
	if event == "pull_request" && failures == 1 && unsuccessful.WorkflowID == workflowID && unsuccessful.Conclusion == "failure" && unsuccessful.Attempt > 0 {
		check.state = workflowSourceCandidate
	}
	return check, nil
}

func workflowInspectionReason(conclusion string) string {
	switch conclusion {
	case "cancelled":
		return "A CI workflow was cancelled. Rerun it on the current pull request, then check again."
	case "timed_out", "startup_failure":
		return "A CI workflow timed out or could not start. Inspect the run before checking again."
	case "skipped", "neutral", "stale":
		return "A CI workflow ended without a passing result. Inspect the run before checking again."
	case "action_required":
		return "GitHub requires an action in CI. Open the run to see what is needed."
	default:
		return "CI did not pass. Inspect the run for the cause before checking again."
	}
}

// GitHub can use either 403 or 429 for rate limits. A 403 without rate-limit
// evidence follows the ordinary permission recovery path.
func githubReadRetry(err error, now time.Time) (time.Time, bool) {
	var failure protocolError
	if !errors.As(err, &failure) {
		return time.Time{}, false
	}
	retryAfter := failure.headers.Get("Retry-After")
	exhausted := failure.headers.Get("X-RateLimit-Remaining") == "0"
	limited := failure.status == http.StatusTooManyRequests || failure.status == http.StatusForbidden && (retryAfter != "" || exhausted)
	if !limited && failure.status != 0 && failure.status < 500 {
		return time.Time{}, false
	}
	next := now.Add(30 * time.Second)
	var hint time.Time
	if seconds, e := strconv.ParseInt(retryAfter, 10, 32); e == nil && seconds >= 0 {
		hint = now.Add(time.Duration(seconds) * time.Second)
	} else if parsed, e := http.ParseTime(retryAfter); e == nil {
		hint = parsed
	}
	if limited && exhausted {
		if reset, e := strconv.ParseInt(failure.headers.Get("X-RateLimit-Reset"), 10, 64); e == nil {
			if resetAt := time.Unix(reset, 0); resetAt.After(hint) {
				hint = resetAt
			}
		}
	}
	if limited && (hint.IsZero() || hint.Before(now)) {
		hint = now.Add(time.Minute)
	}
	if hint.After(next) {
		next = hint
	}
	return next, true
}

const sourceFailureMarker = "CAMP_SOURCE_REPAIR:"
const sourceCheckName = "Camp source check"

type workflowStep struct {
	Name       string `json:"name"`
	Conclusion string `json:"conclusion"`
}

func dedicatedSourceCheck(steps []workflowStep) bool {
	failedCheck := false
	for _, step := range steps {
		switch {
		case step.Name == sourceCheckName:
			if failedCheck || step.Conclusion != "failure" {
				return false
			}
			failedCheck = true
		case step.Name == "Set up job", step.Name == "Complete job",
			strings.HasPrefix(step.Name, "Run actions/checkout@"),
			strings.HasPrefix(step.Name, "Post Run actions/checkout@"):
			if step.Conclusion != "success" && step.Conclusion != "skipped" {
				return false
			}
		default:
			return false
		}
	}
	return failedCheck
}

// sourceFailure reads only jobs for the exact observed run attempt. Matching
// failure and actionable output are both needed to consider a source repair.
func (c *Client) sourceFailure(ctx context.Context, token, base string, run workflowRun) (string, error) {
	var response struct {
		Total int `json:"total_count"`
		Jobs  []struct {
			ID         int64          `json:"id"`
			RunID      int64          `json:"run_id"`
			SHA        string         `json:"head_sha"`
			Name       string         `json:"name"`
			Conclusion string         `json:"conclusion"`
			Steps      []workflowStep `json:"steps"`
		} `json:"jobs"`
	}
	path := fmt.Sprintf("%s/actions/runs/%d/attempts/%d/jobs?per_page=100", base, run.ID, run.Attempt)
	if err := c.call(ctx, token, "GET", path, nil, &response); err != nil {
		return "", err
	}
	if response.Total > 100 {
		return "", nil
	}
	remaining := 2
	for _, job := range response.Jobs {
		if job.ID < 1 || job.RunID != run.ID || job.SHA != run.SHA ||
			job.Name != sourceCheckName || job.Conclusion != "failure" || !dedicatedSourceCheck(job.Steps) {
			continue
		}
		remaining--
		logs, err := c.jobLog(ctx, token, base, job.ID)
		if err != nil {
			if _, retry := githubReadRetry(err, time.Now()); retry {
				return "", err
			}
		} else if diagnostic := reportedSourceFailure(logs); diagnostic != "" {
			return fmt.Sprintf("Workflow run %d, attempt %d, job %d on %s:\n%s", run.ID, run.Attempt, job.ID, run.SHA, diagnostic), nil
		}
		if remaining == 0 {
			break
		}
	}
	return "", nil
}

func reportedSourceFailure(logs string) string {
	for _, line := range strings.Split(logs, "\n") {
		if stamp, rest, ok := strings.Cut(line, " "); ok {
			if _, err := time.Parse(time.RFC3339Nano, stamp); err == nil {
				line = rest
			}
		}
		if message, ok := strings.CutPrefix(line, sourceFailureMarker); ok {
			if message = strings.TrimSpace(message); message != "" {
				return excerpt(message, 4096)
			}
		}
	}
	return ""
}
