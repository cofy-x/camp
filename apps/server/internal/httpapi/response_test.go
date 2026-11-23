package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestCancelledBrowserReadDoesNotReportServerFailure(t *testing.T) {
	var logs bytes.Buffer
	api := &API{logger: slog.New(slog.NewTextHandler(&logs, nil))}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	r := httptest.NewRequest("GET", "/api/resources/files", nil).WithContext(ctx)
	w := httptest.NewRecorder()
	api.fail(w, r, fmt.Errorf("Git read stopped: %w", context.Canceled))
	if w.Body.Len() != 0 || logs.Len() != 0 {
		t.Fatalf("cancelled navigation produced a false failure: body=%q logs=%q", w.Body.String(), logs.String())
	}
}

func TestGitHubDeliveryRecoveryReportsCurrentStateConflict(t *testing.T) {
	cases := []struct {
		action string
		code   string
	}{
		{"retry_delivery", "work_expired"},
		{"retry_delivery", "connection"},
		{"retry_delivery", "repository_changed"},
		{"retry_delivery", "project_changed"},
		{"retry_delivery", "rule_changed"},
		{"repair_delivery", "source_changed"},
		{"repair_delivery", "agent_changed"},
		{"repair_delivery", "repair_unavailable"},
		{"repair_delivery", "daily_task_limit"},
	}
	for _, tt := range cases {
		t.Run(tt.code, func(t *testing.T) {
			var logs bytes.Buffer
			api := &API{logger: slog.New(slog.NewTextHandler(&logs, nil))}
			r := httptest.NewRequest(http.MethodPost, "/api/work-items/work/actions/"+tt.action, nil)
			w := httptest.NewRecorder()
			err := &storage.Error{Code: tt.code, Message: "Review the current delivery authorization."}
			api.fail(w, r, err)
			var body map[string]string
			if decodeErr := json.Unmarshal(w.Body.Bytes(), &body); decodeErr != nil {
				t.Fatal(decodeErr)
			}
			if w.Code != http.StatusConflict || body["code"] != err.Code || body["error"] != err.Message || logs.Len() != 0 {
				t.Fatalf("got status %d, body %v, logs %q; want a conflict preserving the recovery details", w.Code, body, logs.String())
			}
		})
	}
}
