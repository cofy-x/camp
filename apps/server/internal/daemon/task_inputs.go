package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"

	"github.com/cofy-x/camp/apps/server/internal/nativeexecutor"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

func (d *Daemon) taskInputReader(ctx context.Context, id string, scope p.TaskInputScope) (nativeexecutor.TaskInputReader, error) {
	if !scope.Valid() {
		return nil, errors.New("Unsupported or invalid task input scope")
	}
	var grant p.TaskInputGrant
	if err := requestJSON(ctx, d.client, d.config, http.MethodGet, "/api/daemon/operations/"+id+"/task-input-capability", nil, &grant); err != nil {
		return nil, err
	}
	if grant.Scope != scope || len(grant.Token) != 64 || grant.URL != "/api/daemon/task-inputs/"+id {
		return nil, errors.New("Task input capability does not match the frozen Attempt")
	}
	// Only this Go closure retains the Server capability. DSH and command/check
	// environments see neither the credential nor a selectable Camp origin.
	return func(ctx context.Context, after int64, limit int) (p.TaskInputPage, error) {
		var page p.TaskInputPage
		if after < 0 || after > scope.Through || limit < 1 || limit > 20 {
			return page, errors.New("Invalid task input page")
		}
		target, err := endpoint(d.config.ServerURL, fmt.Sprintf("%s?after=%d&limit=%d", grant.URL, after, limit))
		if err != nil {
			return page, err
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
		if err != nil {
			return page, err
		}
		req.Header.Set("Authorization", "Bearer "+grant.Token)
		response, err := d.client.Do(req)
		if err != nil {
			return page, errors.New("Task history request failed")
		}
		defer response.Body.Close()
		if response.StatusCode != 200 {
			return page, &httpError{Status: response.StatusCode}
		}
		data, err := io.ReadAll(io.LimitReader(response.Body, (64<<10)+1))
		if err != nil || len(data) > 64<<10 || json.Unmarshal(data, &page) != nil || page.Through != scope.Through || len(page.Items) > limit {
			return p.TaskInputPage{}, errors.New("Invalid bounded task history response")
		}
		return page, nil
	}, nil
}
