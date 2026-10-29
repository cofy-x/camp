package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"unicode/utf8"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func (a *API) write(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(value); err != nil {
		a.logger.Error("write response", "error", err)
	}
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message, "code": code})
}

func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return
	}
	var e *storage.Error
	if errors.As(err, &e) {
		if status := storageErrorStatus(e.Code); status != http.StatusInternalServerError {
			writeError(w, status, e.Code, e.Message)
			return
		}
	}
	a.logger.ErrorContext(r.Context(), "request failed", "method", r.Method, "path", r.URL.Path, "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "The operation could not be saved. Please try again.")
}

// storageErrorStatus keeps protocol choices at the HTTP boundary. Unknown errors
// remain internal failures until their public response has been defined here.
func storageErrorStatus(code string) int {
	switch code {
	case "invalid_task", "invalid_intent", "invalid_review", "invalid_team", "invalid_user", "invalid_password", "invalid_invite", "invalid_draft", "invalid_cursor",
		"invalid_runner", "invalid_import", "invalid_artifact", "invalid_command", "invalid_lease", "invalid_result":
		return http.StatusBadRequest
	case "invalid_credentials", "unauthenticated":
		return http.StatusUnauthorized
	case "forbidden", "runner_forbidden", "owner_disabled":
		return http.StatusForbidden
	case "username_conflict", "setup_complete", "last_admin":
		return http.StatusConflict
	case "invalid_wallpaper", "invalid_message",
		"invalid_client", "invalid_layout", "invalid_run", "invalid_event",
		"invalid_document", "invalid_room", "invalid_agent", "invalid_resource", "invalid_reference", "invalid_upload":
		return http.StatusBadRequest
	case "not_found":
		return http.StatusNotFound
	case "task_finished", "task_busy", "task_budget_exhausted", "question_answered", "review_conflict", "review_finished", "name_conflict", "unavailable", "revision_conflict", "resource_busy",
		"document_preparing", "document_busy", "document_finished", "run_finished", "request_conflict", "run_busy", "agent_unavailable", "resource_conflict",
		"runner_required", "runner_unavailable", "runner_revision_conflict", "runner_storage_changed", "source_unavailable", "runner_mismatch",
		"attempt_conflict", "attempt_expired", "attempt_finished", "event_sequence_gap", "stale_attempt", "stale_connection", "stale_lease", "run_cancelled", "import_expired", "import_finished":
		return http.StatusConflict
	case "execution_unavailable":
		return http.StatusServiceUnavailable
	case "text_too_large", "file_too_large":
		return http.StatusRequestEntityTooLarge
	default:
		return http.StatusInternalServerError
	}
}

func (a *API) respond(w http.ResponseWriter, r *http.Request, value any, err error) {
	if err != nil {
		a.fail(w, r, err)
		return
	}
	a.write(w, http.StatusOK, value)
}

// decode validates the wire format before exposing an application input.
func decode(w http.ResponseWriter, r *http.Request, value any, limit int64) bool {
	contentType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || contentType != "application/json" {
		writeError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "Use application/json.")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, limit)
	data, err := io.ReadAll(r.Body)
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "The request body is too large.")
		} else {
			writeError(w, http.StatusBadRequest, "invalid_json", "The request body could not be read.")
		}
		return false
	}
	if !utf8.Valid(data) {
		writeError(w, http.StatusBadRequest, "invalid_json", "The request body must use valid UTF-8.")
		return false
	}
	data = bytes.TrimSpace(data)
	if len(data) == 0 || data[0] != '{' {
		writeError(w, http.StatusBadRequest, "invalid_json", "Send one JSON object.")
		return false
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "The request body is not valid JSON.")
		return false
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		writeError(w, http.StatusBadRequest, "invalid_json", "Send one JSON object.")
		return false
	}
	return true
}
