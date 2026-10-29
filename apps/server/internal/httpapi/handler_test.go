package httpapi

import (
	"bytes"
	"encoding/json"
	"github.com/cofy-x/camp/apps/server/internal/storage"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func testAPI(t *testing.T) http.Handler {
	t.Helper()
	s, err := storage.Open(filepath.Join(t.TempDir(), "api.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return New(s, nil)
}

func request(t *testing.T, h http.Handler, method, path string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		reader = bytes.NewReader(data)
	}
	r := httptest.NewRequest(method, path, reader)
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestHTTPInternalErrorsArePrivate(t *testing.T) {
	s, err := storage.Open(filepath.Join(t.TempDir(), "closed.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	h := New(s, nil)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	w := request(t, h, "GET", "/api/health", nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("want HTTP 500, got %d", w.Code)
	}
	var body struct {
		Code  string `json:"code"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Code != "internal_error" || body.Error != "The operation could not be saved. Please try again." {
		t.Fatalf("internal database failure exposed: %+v", body)
	}
}
