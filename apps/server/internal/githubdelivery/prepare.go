package githubdelivery

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/url"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
)

func (c *Client) prepare(ctx context.Context, token, base string, d orchestration.GitHubDelivery) (head, parent string, err error) {
	var ref struct {
		Object struct {
			SHA string `json:"sha"`
		} `json:"object"`
	}
	if err = c.call(ctx, token, "GET", base+"/git/ref/heads/"+url.PathEscape(d.Spec.BaseBranch), nil, &ref); err != nil {
		return "", "", errors.New("the target branch is unavailable")
	}
	parent = ref.Object.SHA
	if d.PreviousHeadSHA != "" {
		if err = c.call(ctx, token, "GET", base+"/git/ref/heads/"+url.PathEscape(d.Branch), nil, &ref); err != nil || ref.Object.SHA != d.PreviousHeadSHA {
			return "", "", errors.New("the previously accepted pull request head changed")
		}
		parent = d.PreviousHeadSHA
	}
	if !validSHA(parent) {
		return "", "", errors.New("the target source version is invalid")
	}
	var commit struct {
		Tree struct {
			SHA string `json:"sha"`
		} `json:"tree"`
	}
	if err = c.call(ctx, token, "GET", base+"/git/commits/"+parent, nil, &commit); err != nil {
		return "", "", err
	}
	var tree struct {
		Truncated bool `json:"truncated"`
		Entries   []struct {
			Path string `json:"path"`
			SHA  string `json:"sha"`
			Mode string `json:"mode"`
			Type string `json:"type"`
		} `json:"tree"`
	}
	if err = c.call(ctx, token, "GET", base+"/git/trees/"+commit.Tree.SHA+"?recursive=1", nil, &tree); err != nil || tree.Truncated {
		return "", "", errors.New("the complete remote source tree exceeds this delivery's bounded reader")
	}
	remote := map[string]string{}
	modes := map[string]string{}
	for _, entry := range tree.Entries {
		remote[entry.Path] = entry.SHA
		modes[entry.Path] = entry.Mode
	}
	trackedPaths := make([]string, 0, len(remote))
	for name := range remote {
		trackedPaths = append(trackedPaths, name)
	}
	changes, err := c.files.Changes(ctx, d.ResourceID, d.BaselineCommit, d.ResultCommit, trackedPaths)
	if err != nil {
		return "", "", err
	}
	entries := []map[string]any{}
	for _, change := range changes {
		if remote[change.Path] != change.OldSHA || modes[change.Path] != change.OldMode {
			return "", "", fmt.Errorf("%s changed outside the authorized baseline", change.Path)
		}
		var sha any
		if change.SHA != "" {
			var blob struct {
				SHA string `json:"sha"`
			}
			if err = c.call(ctx, token, "POST", base+"/git/blobs", map[string]any{"content": base64.StdEncoding.EncodeToString(change.Content), "encoding": "base64"}, &blob); err != nil {
				return "", "", err
			}
			if blob.SHA != change.SHA {
				return "", "", errors.New("the remote blob differs from the accepted file")
			}
			sha = blob.SHA
		}
		entries = append(entries, map[string]any{"path": change.Path, "mode": change.Mode, "type": "blob", "sha": sha})
	}
	var nextTree struct {
		SHA string `json:"sha"`
	}
	if err = c.call(ctx, token, "POST", base+"/git/trees", map[string]any{"base_tree": commit.Tree.SHA, "tree": entries}, &nextTree); err != nil {
		return "", "", err
	}
	date, err := time.Parse(time.RFC3339Nano, d.CreatedAt)
	if err != nil {
		return "", "", err
	}
	identity := map[string]string{"name": "Camp", "email": "camp@users.noreply.github.com", "date": date.UTC().Format(time.RFC3339)}
	var result struct {
		SHA string `json:"sha"`
	}
	if err = c.call(ctx, token, "POST", base+"/git/commits", map[string]any{"message": d.CommitTitle() + "\n\n" + sourceBody(d), "tree": nextTree.SHA, "parents": []string{parent}, "author": identity, "committer": identity}, &result); err != nil {
		return "", "", err
	}
	if !validSHA(result.SHA) {
		return "", "", errors.New("the prepared source commit is invalid")
	}
	return result.SHA, parent, nil
}
