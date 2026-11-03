package httpapi

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/collaboration"
	"github.com/cofy-x/camp/apps/server/internal/runs"
	"github.com/cofy-x/camp/apps/server/internal/storage"
	"github.com/cofy-x/camp/apps/server/internal/testfixture"
)

func historicalDocumentAPI(t *testing.T) (http.Handler, *storage.Store, storage.Room, *sql.DB) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	store, err := storage.Open(filepath.Join(dir, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { store.Close() })
	manager, err := runs.New(ctx, store, dir, runs.Config{MaxTokens: 32, MaxSteps: 1, Timeout: time.Second}, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { manager.Shutdown(ctx) })
	service, err := collaboration.New(ctx, store, manager, dir)
	if err != nil {
		t.Fatal(err)
	}
	rooms, err := store.ListRooms(ctx)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", filepath.Join(dir, "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return New(store, nil, service), store, rooms[0], db
}
func loadDocumentResponse(t *testing.T, w *httptest.ResponseRecorder) storage.Document {
	t.Helper()
	var value struct {
		Document storage.Document `json:"document"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &value); err != nil || w.Code != 200 {
		t.Fatalf("document response %d %s %v", w.Code, w.Body, err)
	}
	return value.Document
}
func historicalUpload(t *testing.T, h http.Handler, room storage.Room, requestID, name string, content []byte) storage.Reference {
	t.Helper()
	w := performUpload(t, h, multipartUpload(t, "/api/rooms/"+room.ID+"/uploads", requestID, name, content, nil), 200)
	var result struct {
		Reference storage.Reference `json:"reference"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result.Reference
}
func documentRows(t *testing.T, db *sql.DB) map[string]string {
	t.Helper()
	rows, err := db.Query(`SELECT id,snapshot FROM documents ORDER BY seq`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var id, body string
		if err := rows.Scan(&id, &body); err != nil {
			t.Fatal(err)
		}
		result[id] = body
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}
func TestDocumentAPIPreservesOriginalsWithoutNewPreparation(t *testing.T) {
	h, _, room, db := historicalDocumentAPI(t)
	for _, name := range []string{"Customer brief.pdf", "Customer brief.docx"} {
		original := []byte("immutable original " + name)
		requestID := strings.TrimPrefix(filepath.Ext(name), ".")
		ref := historicalUpload(t, h, room, requestID, name, original)
		if ref.Document != nil {
			t.Fatal("new upload invented a parser result", ref.Document)
		}
		download := roomRequest(t, h, "GET", "/api/resources/"+ref.ResourceID+"/file?commit="+ref.Commit+"&path="+strings.ReplaceAll(name, " ", "+"), "")
		if download.Code != 200 || download.Body.String() != string(original) {
			t.Fatal("original bytes changed", download.Body)
		}
		for _, retry := range []bool{false, true} {
			body, _ := json.Marshal(map[string]any{"commit": ref.Commit, "path": ref.Path, "retry": retry})
			w := roomRequest(t, h, "POST", "/api/resources/"+ref.ResourceID+"/document", string(body))
			if w.Code != 410 || !strings.Contains(w.Body.String(), storage.DocumentPreparationDisabledCode) {
				t.Fatalf("retired operation %d %s", w.Code, w.Body)
			}
		}
		receipt := roomRequest(t, h, "GET", "/api/rooms/"+room.ID+"/uploads/"+requestID, "")
		if receipt.Code != 200 {
			t.Fatal(receipt.Body)
		}
	}
	if len(documentRows(t, db)) != 0 {
		t.Fatal("upload or prepare created parser jobs")
	}
}
func TestDocumentAPIKeepsReadyHistoryAndProjectsDisabledPendingViews(t *testing.T) {
	h, store, room, db := historicalDocumentAPI(t)
	ref := historicalUpload(t, h, room, "legacy-pdf", "Customer brief.pdf", []byte("original PDF file bytes"))
	ready := storage.Document{ID: "legacy-ready", ResourceID: ref.ResourceID, Commit: ref.Commit, Path: ref.Path, Format: "pdf", Status: "ready", ExtractorVersion: "retired-reader-version", Sections: []storage.DocumentSection{{ID: "p1", Label: "Page 1", Text: "Customer brief source evidence"}}, Warnings: []string{"Historical extraction note"}, CreatedAt: "legacy-created", UpdatedAt: "legacy-finished"}
	testfixture.Document(t, db, ready)
	for _, status := range []string{"queued", "extracting", "failed"} {
		d := ready
		d.ID = "legacy-" + status
		d.Status = status
		d.Sections = []storage.DocumentSection{}
		d.ExtractorVersion = "different-retired-reader"
		if status == "failed" {
			d.Error = &storage.DocumentError{Code: "reader_failed", Message: "Original reader failure", Retryable: true}
		}
		testfixture.Document(t, db, d)
	}
	before := documentRows(t, db)
	exact := loadDocumentResponse(t, roomRequest(t, h, "GET", "/api/documents/"+ready.ID, ""))
	if !reflect.DeepEqual(exact, ready) {
		t.Fatal("READY text, warning, version or source locations changed", exact)
	}
	latest := loadDocumentResponse(t, roomRequest(t, h, "GET", "/api/resources/"+ref.ResourceID+"/document?commit="+ref.Commit+"&path=Customer+brief.pdf", ""))
	if latest.ID != ready.ID {
		t.Fatal("newer pending/failed attempt hid stored READY text", latest)
	}
	for _, status := range []string{"queued", "extracting", "failed"} {
		d := loadDocumentResponse(t, roomRequest(t, h, "GET", "/api/documents/legacy-"+status, ""))
		if !d.Terminal() || d.Error == nil || d.Error.Retryable {
			t.Fatal("reader advertised active work or retry", d)
		}
	}
	body, _ := json.Marshal(storage.SendMessageInput{RequestID: "historical-reference", Content: "Discuss stored evidence", PostOnly: true, References: []storage.ReferenceInput{{Kind: "file", ResourceID: ref.ResourceID, Commit: ref.Commit, Path: ref.Path, DocumentID: ready.ID}}})
	posted := roomRequest(t, h, "POST", "/api/rooms/"+room.ID+"/messages", string(body))
	if posted.Code != 200 || !strings.Contains(posted.Body.String(), `"document":{"id":"legacy-ready"`) {
		t.Fatalf("frozen artifact ID lost: %d %s", posted.Code, posted.Body)
	}
	messages, err := store.ListMessages(context.Background(), room.ID, "", 0)
	if err != nil || len(messages) != 1 || messages[0].References[0].Document.ID != ready.ID {
		t.Fatal("history reference changed", messages, err)
	}
	if !reflect.DeepEqual(before, documentRows(t, db)) {
		t.Fatal("GET, upload receipt or message admission replayed/rewrote legacy parser rows")
	}
	receipt := roomRequest(t, h, "GET", "/api/rooms/"+room.ID+"/uploads/legacy-pdf", "")
	if receipt.Code != 200 || !strings.Contains(receipt.Body.String(), `"id":"legacy-ready"`) {
		t.Fatal("upload receipt hid saved text", receipt.Body)
	}
}
func TestDocumentAPIValidationAndLocalBoundary(t *testing.T) {
	h, _, room, _ := historicalDocumentAPI(t)
	base := "/api/resources/" + room.ResourceID + "/document"
	for _, path := range []string{base, base + "?commit=HEAD&path=brief.pdf", base + "?commit=" + strings.Repeat("a", 40) + "&path=../secret.pdf", base + "?commit=x&commit=y&path=a.pdf"} {
		if w := roomRequest(t, h, "GET", path, ""); w.Code != 400 {
			t.Fatalf("invalid selector %s: %d %s", path, w.Code, w.Body)
		}
	}
	for _, body := range []string{`{"commit":"HEAD","path":"brief.pdf"}`, `{"commit":"` + strings.Repeat("a", 40) + `","path":"../brief.pdf"}`, `{"commit":"` + strings.Repeat("a", 40) + `","path":"brief.pdf","extra":true}`} {
		if w := roomRequest(t, h, "POST", base, body); w.Code != 400 {
			t.Fatalf("invalid disabled request %d %s", w.Code, w.Body)
		}
	}
	req := httptest.NewRequest("GET", "http://localhost:3001/api/documents/missing", nil)
	req.RemoteAddr = "198.51.100.4:4000"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != 403 {
		t.Fatal("document exposed outside local boundary")
	}
	if w := roomRequest(t, h, "GET", "/api/documents/missing", ""); w.Code != 404 {
		t.Fatal(w.Body)
	}
}
