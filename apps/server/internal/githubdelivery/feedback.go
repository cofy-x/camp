package githubdelivery

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
)

type evidenceError struct{ reason string }

func (e evidenceError) Error() string { return e.reason }

var sourceVersion = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$`)

func (c *Client) sourceTag(ctx context.Context, d orchestration.GitHubDelivery) (string, error) {
	raw, err := c.files.ReadFile(ctx, d.ResourceID, d.ResultCommit, "package.json")
	var p struct {
		Version string `json:"version"`
	}
	if err != nil || len(raw) > 64<<10 || json.Unmarshal(raw, &p) != nil || len(p.Version) > 100 || !sourceVersion.MatchString(p.Version) || !strings.HasPrefix(p.Version, d.Spec.VersionLine+".") {
		return "", errors.New("The accepted package.json needs a valid version within the approved " + d.Spec.VersionLine + " release line.")
	}
	if strings.Contains(p.Version, "-") && !d.Spec.Prerelease {
		return "", errors.New("The accepted version is a prerelease; review its release authorization.")
	}
	return "v" + p.Version, nil
}

func (c *Client) reviewFeedback(ctx context.Context, token, base string, d orchestration.GitHubDelivery) (string, error) {
	if len(d.ReviewerIDs) == 0 {
		return "", nil
	}
	var reviews []struct {
		ID     int64  `json:"id"`
		State  string `json:"state"`
		Body   string `json:"body"`
		Commit string `json:"commit_id"`
		User   struct {
			ID int64 `json:"id"`
		} `json:"user"`
	}
	if err := c.call(ctx, token, "GET", base+"/pulls/"+strconv.FormatInt(d.PullRequest, 10)+"/reviews?per_page=100", nil, &reviews); err != nil {
		return "", err
	}
	if len(reviews) >= 100 {
		return "", evidenceError{"The review history exceeds the bounded reader. Review the current PR feedback."}
	}
	latest := map[int64]int64{}
	for _, r := range reviews {
		if r.State != "COMMENTED" && r.State != "PENDING" && r.ID > latest[r.User.ID] {
			latest[r.User.ID] = r.ID
		}
	}
	var evidence strings.Builder
	requested := map[int64]int64{}
	for _, r := range reviews {
		allowed := false
		for _, actor := range d.ReviewerIDs {
			allowed = allowed || r.User.ID == actor
		}
		if allowed && r.ID == latest[r.User.ID] && r.State == "CHANGES_REQUESTED" && r.Commit == d.HeadSHA {
			requested[r.ID] = r.User.ID
			fmt.Fprintf(&evidence, "Review %d by approved actor %d on %s:\n%s\n", r.ID, r.User.ID, r.Commit, excerpt(r.Body, 4096))
		}
	}
	if len(requested) > 0 {
		var comments []struct {
			ReviewID int64  `json:"pull_request_review_id"`
			Commit   string `json:"commit_id"`
			Path     string `json:"path"`
			Line     int    `json:"line"`
			Body     string `json:"body"`
			User     struct {
				ID int64 `json:"id"`
			} `json:"user"`
		}
		if err := c.call(ctx, token, "GET", base+"/pulls/"+strconv.FormatInt(d.PullRequest, 10)+"/comments?per_page=100", nil, &comments); err != nil {
			return "", err
		}
		if len(comments) >= 100 {
			return "", evidenceError{"The inline review history exceeds the bounded reader. Review the current PR feedback."}
		}
		for _, comment := range comments {
			if actor := requested[comment.ReviewID]; actor > 0 && actor == comment.User.ID && comment.Commit == d.HeadSHA {
				fmt.Fprintf(&evidence, "Inline review %d, %s:%d on %s:\n%s\n", comment.ReviewID, comment.Path, comment.Line, comment.Commit, excerpt(comment.Body, 4096))
			}
		}
	}
	return excerpt(evidence.String(), 12<<10), nil
}

func excerpt(s string, limit int) string {
	if len(s) > limit {
		s = s[:limit]
	}
	return strings.ToValidUTF8(s, "�")
}

func (c *Client) jobLog(ctx context.Context, token, base string, id int64) (string, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", c.origin+base+"/actions/jobs/"+strconv.FormatInt(id, 10)+"/logs", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	response, err := c.http.Do(req)
	if err != nil {
		return "", protocolError{}
	}
	response.Body.Close()
	if response.StatusCode != http.StatusFound {
		return "", protocolError{status: response.StatusCode, headers: response.Header}
	}
	target, err := url.Parse(response.Header.Get("Location"))
	if err != nil || target.Scheme != "https" || target.User != nil || target.Hostname() == "" {
		return "", errors.New("Invalid log download destination")
	}
	// This request deliberately has no GitHub authorization headers.
	req, err = http.NewRequestWithContext(ctx, "GET", target.String(), nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Range", "bytes=-8192")
	response, err = c.http.Do(req)
	if err != nil {
		return "", protocolError{}
	}
	defer response.Body.Close()
	if response.StatusCode != 200 && response.StatusCode != 206 {
		return "", protocolError{status: response.StatusCode, headers: response.Header}
	}
	raw, err := io.ReadAll(io.LimitReader(response.Body, 8<<20+1))
	if err != nil {
		return "", protocolError{}
	}
	if len(raw) > 8<<20 {
		return "", errors.New("Job logs could not be read within their limit")
	}
	if len(raw) > 8192 {
		raw = raw[len(raw)-8192:]
	}
	return strings.ToValidUTF8(string(raw), "�"), nil
}
