package httpapi

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"net/http/httptest"
	"testing"
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
