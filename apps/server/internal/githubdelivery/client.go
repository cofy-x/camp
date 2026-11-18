// Package githubdelivery owns the bounded GitHub publication protocol. Storage
// owns authorization and durable claims; tools never receive these credentials.
package githubdelivery

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

	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/githubhooks"
	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/resources"
)

type Credentials interface {
	Read(string) (configuration.GitHubCredential, error)
}
type Files interface {
	Changes(context.Context, string, string, string, []string) ([]resources.Change, error)
	ReadFile(context.Context, string, string, string) ([]byte, error)
}
type Client struct {
	credentials Credentials
	files       Files
	http        *http.Client
	origin      string
}

func NewClient(credentials Credentials, files Files) *Client {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 15 * time.Second
	return &Client{credentials: credentials, files: files, http: &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}, origin: "https://api.github.com"}
}
func (c *Client) request(ctx context.Context, token, method, path string, body, out any) (int, error) {
	var input io.Reader
	if body != nil {
		raw, e := json.Marshal(body)
		if e != nil {
			return 0, e
		}
		input = bytes.NewReader(raw)
	}
	req, e := http.NewRequestWithContext(ctx, method, c.origin+path, input)
	if e != nil {
		return 0, e
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "Camp")
	response, e := c.http.Do(req)
	if e != nil {
		return 0, errors.New("GitHub did not confirm the response")
	}
	defer response.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(response.Body, 8<<20+1))
	if e != nil || len(raw) > 8<<20 {
		return response.StatusCode, errors.New("The GitHub response could not be read within its limit")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, nil
	}
	if out != nil && json.Unmarshal(raw, out) != nil {
		return response.StatusCode, errors.New("GitHub returned an invalid response")
	}
	return response.StatusCode, nil
}

type repository struct {
	ID            int64  `json:"id"`
	FullName      string `json:"full_name"`
	DefaultBranch string `json:"default_branch"`
	Private       bool   `json:"private"`
	Permissions   struct {
		Push bool `json:"push"`
	} `json:"permissions"`
}

func (c *Client) Verify(ctx context.Context, token string, input orchestration.GitHubRepositoryInput) (out orchestration.GitHubRepository, err error) {
	if !githubhooks.RepositoryPattern.MatchString(input.Repository) {
		return out, errors.New("Use owner/repository for a GitHub repository")
	}
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var actor struct {
		ID int64 `json:"id"`
	}
	status, e := c.request(ctx, token, "GET", "/user", nil, &actor)
	if e != nil || status != 200 || actor.ID < 1 {
		return out, errors.New("The GitHub credential could not be verified")
	}
	var repo repository
	status, e = c.request(ctx, token, "GET", "/repos/"+input.Repository, nil, &repo)
	if e != nil || status != 200 || repo.ID < 1 || !strings.EqualFold(repo.FullName, input.Repository) || !repo.Permissions.Push {
		return out, errors.New("The credential needs write access to this exact GitHub repository")
	}
	out = orchestration.GitHubRepository{Repository: repo.FullName, RepositoryID: repo.ID, DefaultBranch: repo.DefaultBranch, Private: repo.Private, ActorID: actor.ID, Enabled: input.Enabled}
	workflow := func(name string) (int64, error) {
		if name == "" {
			return 0, nil
		}
		if strings.ContainsAny(name, "/\\?#") || len(name) > 128 {
			return 0, errors.New("Use the workflow filename, such as ci.yml")
		}
		var w struct {
			ID    int64  `json:"id"`
			State string `json:"state"`
		}
		status, e := c.request(ctx, token, "GET", "/repos/"+out.Repository+"/actions/workflows/"+url.PathEscape(name), nil, &w)
		if e != nil || status != 200 || w.ID < 1 || w.State != "active" {
			return 0, errors.New("The selected GitHub workflow is unavailable or disabled")
		}
		return w.ID, nil
	}
	out.CIWorkflowID, err = workflow(input.CIWorkflow)
	if err != nil {
		return out, err
	}
	out.CIWorkflow = input.CIWorkflow
	out.DeploymentWorkflowID, err = workflow(input.DeploymentWorkflow)
	out.DeploymentWorkflow = input.DeploymentWorkflow
	return
}

type protocolError struct {
	status    int
	uncertain bool
}

func (e protocolError) Error() string { return "The GitHub operation was not confirmed" }
func (c *Client) call(ctx context.Context, token, method, path string, body, out any) error {
	status, e := c.request(ctx, token, method, path, body, out)
	if e != nil || status >= 500 || status == 0 {
		return protocolError{status: status, uncertain: method != "GET"}
	}
	if status < 200 || status >= 300 {
		return protocolError{status: status}
	}
	return nil
}
func missing(err error) bool                           { var e protocolError; return errors.As(err, &e) && e.status == 404 }
func uncertain(err error) bool                         { var e protocolError; return errors.As(err, &e) && e.uncertain }
func marker(d orchestration.GitHubDelivery) string     { return "<!-- camp-delivery:" + d.ID + " -->" }
func sourceBody(d orchestration.GitHubDelivery) string { return d.PullRequestBody() }

// Step dispatches at most one visible effect. Lost responses are reconciled with
// reads in the same invocation; absence never authorizes a second write.
func (c *Client) Step(ctx context.Context, d orchestration.GitHubDelivery, ref string) orchestration.GitHubDeliveryResult {
	result := orchestration.GitHubDeliveryResult{Delivery: d}
	ctx, cancel := context.WithTimeout(ctx, 90*time.Second)
	defer cancel()
	reconcile := d.Reconcile && d.VisibleWriteStage()
	fail := func(reason string) orchestration.GitHubDeliveryResult {
		result.Delivery.Status = "blocked"
		if reconcile {
			result.Delivery.Status, result.Delivery.Reconcile = "unknown", true
		}
		result.Delivery.Reason = reason
		if result.Delivery.FailureCode == "" {
			result.Delivery.FailureCode = "action_failed"
		}
		return result
	}
	failWith := func(code, reason string) orchestration.GitHubDeliveryResult {
		result.Delivery.FailureCode = code
		return fail(reason)
	}
	readFailure := func(err error, reason string) orchestration.GitHubDeliveryResult {
		var scope evidenceError
		if errors.As(err, &scope) {
			return failWith("check_scope", scope.Error())
		}
		var protocol protocolError
		if errors.As(err, &protocol) && (protocol.status == 0 || protocol.status == 429 || protocol.status >= 500) && !d.Reconcile && d.ReadFailures < 3 {
			result.Delivery.ReadFailures++
			result.Delivery.Status, result.Delivery.Reason = "waiting", "Waiting for GitHub to respond."
			result.Delivery.NextCheckAt = time.Now().Add(30 * time.Second).UTC().Format(time.RFC3339Nano)
			return result
		}
		return failWith("connection", reason)
	}
	credential, e := c.credentials.Read(ref)
	if e != nil || credential.ActorID != d.ActorID {
		return failWith("credential", "The authorized GitHub account credential is unavailable.")
	}
	token := credential.Token
	base := "/repos/" + d.Repository
	var repo repository
	if e = c.call(ctx, token, "GET", base, nil, &repo); e != nil {
		return readFailure(e, "Reconnect the repository before continuing delivery.")
	}
	if repo.ID != d.RepositoryID || repo.Private != d.Private || !strings.EqualFold(repo.FullName, d.Repository) || (!repo.Permissions.Push && !reconcile) {
		return failWith("repository_changed", "Repository identity, visibility, or current write permission no longer matches the authorization.")
	}
	var actor struct {
		ID int64 `json:"id"`
	}
	if e = c.call(ctx, token, "GET", "/user", nil, &actor); e != nil {
		return readFailure(e, "Reconnect the authorized GitHub account.")
	}
	if actor.ID != d.ActorID {
		return failWith("credential", "The GitHub account no longer matches the authorized actor.")
	}
	next := func(stage string) {
		result.Delivery.Stage = stage
		result.Delivery.Status = "queued"
		result.Delivery.NextCheckAt = time.Now().UTC().Format(time.RFC3339Nano)
		result.Delivery.Reason = ""
		result.Delivery.Reconcile = false
		result.Delivery.FailureCode, result.Delivery.ReadFailures = "", 0
	}
	done := func() {
		result.Delivery.Status = "succeeded"
		result.Delivery.Reason = ""
		result.Delivery.Reconcile = false
	}
	wait := func(reason string) orchestration.GitHubDeliveryResult {
		result.Delivery.Checks++
		if result.Delivery.Checks >= 120 {
			return fail("The finite GitHub check allowance ended.")
		}
		result.Delivery.Status = "waiting"
		result.Delivery.Reason = reason
		result.Delivery.NextCheckAt = time.Now().Add(30 * time.Second).UTC().Format(time.RFC3339Nano)
		return result
	}
	var effectErr error
	switch d.Stage {
	case "prepare":
		if d.SourceSelection != "gitignore-v1" {
			return fail("Review and authorize the source selection before preparing this delivery.")
		}
		if d.Spec.VersionLine != "" {
			tag, err := c.sourceTag(ctx, d)
			if err != nil {
				return failWith("version", err.Error())
			}
			d.Spec.Tag, result.Delivery.Spec.Tag = tag, tag
		}
		if d.Spec.Mode == "release" {
			var tag any
			err := c.call(ctx, token, "GET", base+"/git/ref/tags/"+url.PathEscape(d.Spec.Tag), nil, &tag)
			if err == nil {
				return failWith("version", "The authorized release tag already exists. Review the accepted source version before delivering.")
			}
			if !missing(err) {
				return readFailure(err, "The release version could not be checked.")
			}
		}
		head, parent, e := c.prepare(ctx, token, base, d)
		if e != nil {
			return failWith("source_changed", "The accepted patch could not be prepared: "+e.Error())
		}
		result.Delivery.HeadSHA, result.Delivery.BaseSHA = head, parent
		if d.PreviousHeadSHA != "" {
			next("update_branch")
		} else {
			next("branch")
		}
	case "update_branch":
		path := base + "/git/refs/heads/" + url.PathEscape(d.Branch)
		var ref struct {
			Object struct {
				SHA  string `json:"sha"`
				Type string `json:"type"`
			} `json:"object"`
		}
		if e = c.call(ctx, token, "GET", base+"/git/ref/heads/"+url.PathEscape(d.Branch), nil, &ref); e != nil {
			return readFailure(e, "The delivery branch could not be checked.")
		}
		if !reconcile {
			if ref.Object.SHA != d.PreviousHeadSHA {
				return failWith("source_changed", "The pull request branch changed outside this repair. Review its source.")
			}
			result.Effect = "update_branch"
			effectErr = c.call(ctx, token, "PATCH", path, map[string]any{"sha": d.HeadSHA, "force": false}, &ref)
			if uncertain(effectErr) {
				result.Outcome, reconcile = "unknown", true
				effectErr = c.call(ctx, token, "GET", base+"/git/ref/heads/"+url.PathEscape(d.Branch), nil, &ref)
			}
		}
		if effectErr == nil && ref.Object.SHA == d.HeadSHA && ref.Object.Type == "commit" {
			next("checks")
		} else if effectErr == nil {
			effectErr = errors.New("The repaired branch head is not confirmed")
		}
	case "branch":
		path := base + "/git/ref/heads/" + url.PathEscape(d.Branch)
		var ref struct {
			Object struct {
				SHA  string `json:"sha"`
				Type string `json:"type"`
			} `json:"object"`
		}
		if reconcile {
			effectErr = c.call(ctx, token, "GET", path, nil, &ref)
		} else {
			result.Effect = "branch"
			effectErr = c.call(ctx, token, "POST", base+"/git/refs", map[string]any{"ref": "refs/heads/" + d.Branch, "sha": d.HeadSHA}, &ref)
			if uncertain(effectErr) {
				result.Outcome = "unknown"
				reconcile = true
				effectErr = c.call(ctx, token, "GET", path, nil, &ref)
			}
		}
		if effectErr == nil && ref.Object.SHA == d.HeadSHA && ref.Object.Type == "commit" {
			next("pull_request")
		} else if effectErr == nil {
			effectErr = errors.New("The branch points to a different source version")
		}
	case "pull_request":
		var pr pullRequest
		if reconcile {
			effectErr = c.findPullRequest(ctx, token, base, d, &pr)
		} else {
			result.Effect = "pull_request"
			effectErr = c.call(ctx, token, "POST", base+"/pulls", map[string]any{"title": d.Title, "head": d.Branch, "base": d.Spec.BaseBranch, "body": sourceBody(d), "maintainer_can_modify": false}, &pr)
			if uncertain(effectErr) {
				result.Outcome = "unknown"
				reconcile = true
				effectErr = c.findPullRequest(ctx, token, base, d, &pr)
			}
		}
		if effectErr == nil && !validPR(pr, d) {
			effectErr = errors.New("The pull request does not match the authorized source and destination")
		}
		if effectErr == nil {
			result.Delivery.PullRequest, result.Delivery.PullRequestURL = pr.Number, pr.URL
			if d.Spec.Mode == "pr" {
				done()
			} else {
				next("checks")
			}
		}
	case "checks", "review":
		var pr pullRequest
		if e = c.call(ctx, token, "GET", base+"/pulls/"+strconv.FormatInt(d.PullRequest, 10), nil, &pr); e != nil {
			return readFailure(e, "The pull request could not be checked.")
		}
		if !validPR(pr, d) || pr.State != "open" {
			return failWith("source_changed", "The pull request changed or closed before its checks were accepted.")
		}
		ready, failed, e := c.checks(ctx, token, base, d.HeadSHA, d.CIWorkflowID, "pull_request")
		if e != nil {
			return readFailure(e, "The exact-source CI results could not be read.")
		}
		if failed {
			result.Delivery.Stage = "checks"
			result.Delivery.Diagnostics = c.ciDiagnostics(ctx, token, base, d)
			return failWith("ci_failed", "CI did not pass for the authorized pull request head.")
		}
		if !ready {
			result.Delivery.Stage = "checks"
			return wait("Waiting for CI.")
		}
		feedback, e := c.reviewFeedback(ctx, token, base, d)
		if e != nil {
			return readFailure(e, "Approved review feedback could not be checked.")
		}
		if feedback != "" {
			result.Delivery.Stage = "checks"
			result.Delivery.Diagnostics = feedback
			return failWith("review_changes", "An approved reviewer requested changes on this exact source version.")
		}
		if pr.MergeableState == "blocked" || pr.MergeableState == "unknown" || pr.MergeableState == "unstable" {
			result.Delivery.Stage = "review"
			return wait("Waiting for GitHub repository checks and review requirements.")
		}
		if pr.MergeableState == "behind" || pr.MergeableState == "dirty" {
			return failWith("source_changed", "The target branch moved or has conflicts. Review the source before rebasing this delivery.")
		}
		next("merge")
	case "merge":
		path := base + "/pulls/" + strconv.FormatInt(d.PullRequest, 10)
		var pr pullRequest
		if e = c.call(ctx, token, "GET", path, nil, &pr); e != nil || !validPR(pr, d) {
			return fail("The pull request head or target changed. No merge was authorized.")
		}
		if !reconcile && (pr.MergeableState == "blocked" || pr.MergeableState == "unknown" || pr.MergeableState == "unstable") {
			result.Delivery.Stage = "review"
			return wait("Waiting for GitHub repository checks and review requirements.")
		}
		if reconcile {
			if !pr.Merged {
				effectErr = protocolError{uncertain: true}
			} else {
				result.Delivery.MergeSHA = pr.MergeSHA
			}
		} else {
			// Re-read exact-head checks immediately before the CAS merge. GitHub's own
			// branch protection remains authoritative; no admin bypass is requested.
			ready, failed, e := c.checks(ctx, token, base, d.HeadSHA, d.CIWorkflowID, "pull_request")
			if e != nil {
				return readFailure(e, "Current exact-source CI could not be verified before merging.")
			}
			if failed {
				result.Delivery.Stage = "checks"
				result.Delivery.Diagnostics = c.ciDiagnostics(ctx, token, base, d)
				return failWith("ci_failed", "Current exact-source CI no longer permits merging.")
			}
			if !ready {
				next("checks")
				return result
			}
			feedback, err := c.reviewFeedback(ctx, token, base, d)
			if err != nil {
				return readFailure(err, "Approved review feedback could not be checked.")
			}
			if feedback != "" {
				result.Delivery.Stage = "checks"
				result.Delivery.Diagnostics = feedback
				return failWith("review_changes", "An approved reviewer requested changes on this exact source version.")
			}
			var merged struct {
				Merged bool   `json:"merged"`
				SHA    string `json:"sha"`
			}
			result.Effect = "merge"
			effectErr = c.call(ctx, token, "PUT", path+"/merge", map[string]any{"sha": d.HeadSHA, "merge_method": "squash", "commit_title": d.Title}, &merged)
			if effectErr == nil && !merged.Merged {
				effectErr = errors.New("GitHub repository rules did not allow this merge")
			}
			if effectErr == nil {
				result.Delivery.MergeSHA = merged.SHA
			}
			if uncertain(effectErr) {
				result.Outcome = "unknown"
				reconcile = true
				effectErr = c.call(ctx, token, "GET", path, nil, &pr)
				if effectErr == nil && validPR(pr, d) && pr.Merged {
					result.Delivery.MergeSHA = pr.MergeSHA
				} else {
					effectErr = protocolError{uncertain: true}
				}
			}
		}
		if effectErr == nil {
			if !validSHA(result.Delivery.MergeSHA) {
				return fail("GitHub did not return an exact merged source version.")
			}
			if d.Spec.Mode == "release" {
				next("release")
			} else if d.Spec.WaitForDeployment {
				next("deployment")
			} else {
				done()
			}
		}
	case "release":
		if reconcile {
			effectErr = c.releaseReceipt(ctx, token, base, d, &result.Delivery.ReleaseURL)
		} else {
			var tag struct {
				Object struct {
					SHA  string `json:"sha"`
					Type string `json:"type"`
				} `json:"object"`
			}
			e = c.call(ctx, token, "GET", base+"/git/ref/tags/"+url.PathEscape(d.Spec.Tag), nil, &tag)
			if e != nil && !missing(e) {
				return fail("The release tag could not be verified.")
			}
			if e == nil && (tag.Object.SHA != d.MergeSHA || tag.Object.Type != "commit") {
				return fail("The release tag already points to another version. It was not overwritten.")
			}
			var release struct {
				URL string `json:"html_url"`
			}
			result.Effect = "release"
			effectErr = c.call(ctx, token, "POST", base+"/releases", map[string]any{"tag_name": d.Spec.Tag, "target_commitish": d.MergeSHA, "name": d.Spec.Tag, "body": sourceBody(d) + "\n\nPull request: " + d.PullRequestURL, "draft": false, "prerelease": d.Spec.Prerelease}, &release)
			if effectErr == nil {
				result.Delivery.ReleaseURL = release.URL
				effectErr = c.releaseReceipt(ctx, token, base, d, &result.Delivery.ReleaseURL)
				if effectErr != nil {
					result.Outcome = "unknown"
					reconcile = true
				}
			}
			if uncertain(effectErr) {
				result.Outcome = "unknown"
				reconcile = true
				effectErr = c.releaseReceipt(ctx, token, base, d, &result.Delivery.ReleaseURL)
			}
		}
		if effectErr == nil {
			if d.Spec.WaitForDeployment {
				next("deployment")
			} else {
				done()
			}
		}
	case "deployment":
		ready, failed, e := c.checks(ctx, token, base, d.MergeSHA, d.DeploymentWorkflowID, "push")
		if e != nil {
			return fail("The exact merged-version deployment could not be read.")
		}
		if failed {
			return fail("Deployment failed for the merged version.")
		}
		if !ready {
			return wait("Waiting for deployment.")
		}
		var pages struct {
			URL string `json:"html_url"`
		}
		if e = c.call(ctx, token, "GET", base+"/pages", nil, &pages); e == nil && strings.HasPrefix(pages.URL, "https://") {
			result.Delivery.DeploymentURL = pages.URL
		}
		done()
	default:
		return fail("The recorded GitHub delivery stage is unsupported.")
	}
	if effectErr != nil {
		if reconcile {
			result.Delivery.Status = "unknown"
			result.Delivery.Reconcile = true
			result.Delivery.Reason = "GitHub has not confirmed the recorded action. Check its receipt; Camp will not resend it."
			return result
		}
		return fail("GitHub rejected the authorized " + strings.ReplaceAll(d.Stage, "_", " ") + " action. Repair its permission, source conflict, or repository rule.")
	}
	if result.Effect != "" && result.Outcome == "" {
		result.Outcome = "confirmed"
	}
	return result
}

func validSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return true
}

type pullRequest struct {
	MergeableState string `json:"mergeable_state"`
	Number         int64  `json:"number"`
	URL            string `json:"html_url"`
	Body           string `json:"body"`
	State          string `json:"state"`
	Merged         bool   `json:"merged"`
	MergeSHA       string `json:"merge_commit_sha"`
	User           struct {
		ID int64 `json:"id"`
	} `json:"user"`
	Head struct {
		SHA  string `json:"sha"`
		Ref  string `json:"ref"`
		Repo struct {
			ID int64 `json:"id"`
		} `json:"repo"`
	} `json:"head"`
	Base struct {
		Ref  string `json:"ref"`
		Repo struct {
			ID int64 `json:"id"`
		} `json:"repo"`
	} `json:"base"`
}

func validPR(pr pullRequest, d orchestration.GitHubDelivery) bool {
	return pr.Number > 0 && pr.User.ID == d.ActorID && pr.Head.SHA == d.HeadSHA && pr.Head.Ref == d.Branch && pr.Head.Repo.ID == d.RepositoryID && pr.Base.Repo.ID == d.RepositoryID && pr.Base.Ref == d.Spec.BaseBranch && strings.Contains(pr.Body, marker(d))
}
func (c *Client) findPullRequest(ctx context.Context, token, base string, d orchestration.GitHubDelivery, out *pullRequest) error {
	var pulls []pullRequest
	path := base + "/pulls?state=all&per_page=100&head=" + url.QueryEscape(strings.Split(d.Repository, "/")[0]+":"+d.Branch)
	if e := c.call(ctx, token, "GET", path, nil, &pulls); e != nil {
		return e
	}
	matches := 0
	for _, pr := range pulls {
		if validPR(pr, d) {
			*out = pr
			matches++
		}
	}
	if matches != 1 {
		return protocolError{uncertain: true}
	}
	return nil
}
func (c *Client) releaseReceipt(ctx context.Context, token, base string, d orchestration.GitHubDelivery, out *string) error {
	var release struct {
		URL    string `json:"html_url"`
		Body   string `json:"body"`
		Draft  bool   `json:"draft"`
		Tag    string `json:"tag_name"`
		Author struct {
			ID int64 `json:"id"`
		} `json:"author"`
	}
	if e := c.call(ctx, token, "GET", base+"/releases/tags/"+url.PathEscape(d.Spec.Tag), nil, &release); e != nil {
		return e
	}
	var tag struct {
		Object struct {
			SHA  string `json:"sha"`
			Type string `json:"type"`
		} `json:"object"`
	}
	if e := c.call(ctx, token, "GET", base+"/git/ref/tags/"+url.PathEscape(d.Spec.Tag), nil, &tag); e != nil {
		return e
	}
	if release.Draft || release.Tag != d.Spec.Tag || release.Author.ID != d.ActorID || !strings.Contains(release.Body, marker(d)) || tag.Object.SHA != d.MergeSHA || tag.Object.Type != "commit" {
		return errors.New("The release receipt does not match its exact source")
	}
	*out = release.URL
	return nil
}

func (c *Client) checks(ctx context.Context, token, base, sha string, workflowID int64, event string) (ready, failed bool, err error) {
	if !validSHA(sha) || workflowID < 1 {
		return false, false, evidenceError{"Review the authorized source head and required workflow before accepting CI."}
	}
	var runs struct {
		Total int `json:"total_count"`
		Runs  []struct {
			ID         int64  `json:"id"`
			Workflow   int64  `json:"workflow_id"`
			SHA        string `json:"head_sha"`
			Event      string `json:"event"`
			Status     string `json:"status"`
			Conclusion string `json:"conclusion"`
		} `json:"workflow_runs"`
	}
	path := base + "/actions/runs?per_page=100&head_sha=" + sha + "&event=" + url.QueryEscape(event)
	if err = c.call(ctx, token, "GET", path, nil, &runs); err != nil {
		return
	}
	if runs.Total > 100 {
		return false, false, evidenceError{"The exact-source workflow history exceeds the bounded check reader. Review the CI results."}
	}
	found := false
	ready = true
	latest := map[int64]int64{}
	for _, run := range runs.Runs {
		if run.ID > latest[run.Workflow] {
			latest[run.Workflow] = run.ID
		}
	}
	for _, run := range runs.Runs {
		if run.ID != latest[run.Workflow] {
			continue
		}
		if run.SHA != sha || run.Event != event {
			return false, false, evidenceError{"The CI evidence does not match the authorized source and event."}
		}
		if run.Workflow == workflowID {
			found = true
		}
		if run.Status != "completed" {
			ready = false
		} else if run.Conclusion != "success" {
			failed = true
		}
	}
	if !found {
		ready = false
	}
	return
}
