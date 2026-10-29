package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/collaboration"
	"github.com/cofy-x/camp/apps/server/internal/documents"
	"github.com/cofy-x/camp/apps/server/internal/documenttext"
	"github.com/cofy-x/camp/apps/server/internal/resources"
	"github.com/cofy-x/camp/apps/server/internal/runs"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type documentReader struct{ calls atomic.Int32 }

func (d *documentReader) Extract(context.Context, string, []byte) (documenttext.Result, error) {
	d.calls.Add(1)
	return documenttext.Result{Format: "pdf", Sections: []documenttext.Section{{ID: "p1", Label: "Page 1", Text: "Customer brief source evidence"}}}, nil
}
func preparedAPI(t *testing.T) (http.Handler, *storage.Store, storage.Room, *documentReader) {
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
	reader := &documentReader{}
	preparer, err := documents.New(ctx, store, resources.New(filepath.Join(dir, "resources")), reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { preparer.Shutdown(ctx) })
	service, err := collaboration.New(ctx, store, manager, dir, preparer)
	if err != nil {
		t.Fatal(err)
	}
	rooms, err := store.ListRooms(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return New(store, nil, service), store, rooms[0], reader
}
func loadDocumentResponse(t *testing.T, w *httptest.ResponseRecorder) storage.Document {
	t.Helper()
	var result struct {
		Document storage.Document `json:"document"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil || w.Code != 200 {
		t.Fatalf("document response %d %s %v", w.Code, w.Body, err)
	}
	return result.Document
}
func TestDocumentAPIAutomaticPreparationPinnedHistoryAndDownload(t *testing.T) {
	h, store, room, reader := preparedAPI(t)
	uploadPath := "/api/rooms/" + room.ID + "/uploads"
	content := []byte("original PDF file bytes")
	response := performUpload(t, h, multipartUpload(t, uploadPath, "pdf", "Customer brief.pdf", content, nil), 200)
	var result struct {
		Reference storage.Reference `json:"reference"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Reference.Document == nil {
		t.Fatalf("upload preparation missing %s %v", response.Body, err)
	}
	ref := result.Reference
	deadline := time.Now().Add(3 * time.Second)
	var original storage.Document
	for time.Now().Before(deadline) {
		original = loadDocumentResponse(t, roomRequest(t, h, "GET", "/api/documents/"+ref.Document.ID, ""))
		if original.Status == "ready" {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if original.Status != "ready" || original.Sections[0].Label != "Page 1" || reader.calls.Load() != 1 {
		t.Fatalf("preparation %+v calls=%d", original, reader.calls.Load())
	}
	latestPath := "/api/resources/" + ref.ResourceID + "/document?commit=" + ref.Commit + "&path=Customer+brief.pdf"
	latest := loadDocumentResponse(t, roomRequest(t, h, "GET", latestPath, ""))
	if latest.ID != original.ID {
		t.Fatal("latest points elsewhere")
	}
	preparePath := "/api/resources/" + ref.ResourceID + "/document"
	body := `{"commit":"` + ref.Commit + `","path":"Customer brief.pdf"}`
	same := loadDocumentResponse(t, roomRequest(t, h, "POST", preparePath, body))
	if same.ID != original.ID || reader.calls.Load() != 1 {
		t.Fatal("prepare repeated terminal job")
	}
	retry := loadDocumentResponse(t, roomRequest(t, h, "POST", preparePath, strings.TrimSuffix(body, "}")+`,"retry":true}`))
	if retry.ID == original.ID {
		t.Fatal("retry did not append")
	}
	previous := loadDocumentResponse(t, roomRequest(t, h, "GET", "/api/documents/"+original.ID, ""))
	if previous.Status != "ready" || previous.Sections[0].Text != original.Sections[0].Text {
		t.Fatal("retry changed earlier artifact")
	}
	download := roomRequest(t, h, "GET", "/api/resources/"+ref.ResourceID+"/file?commit="+ref.Commit+"&path=Customer+brief.pdf", "")
	if download.Code != 200 || download.Body.String() != string(content) {
		t.Fatal("original download changed", download.Body)
	}
	input := storage.SendMessageInput{RequestID: "post-with-source", Content: "Discuss this original", PostOnly: true, References: []storage.ReferenceInput{{Kind: "file", ResourceID: ref.ResourceID, Commit: ref.Commit, Path: ref.Path, DocumentID: original.ID}}}
	encoded, _ := json.Marshal(input)
	post := roomRequest(t, h, "POST", "/api/rooms/"+room.ID+"/messages", string(encoded))
	if post.Code != 200 || !strings.Contains(post.Body.String(), `"document":{"id":"`+original.ID+`"`) {
		t.Fatalf("canonical snapshot %d %s", post.Code, post.Body)
	}
	receipt := roomRequest(t, h, "GET", uploadPath+"/pdf", "")
	if receipt.Code != 200 || !strings.Contains(receipt.Body.String(), `"document":`) {
		t.Fatal("receipt lost preparation", receipt.Body)
	}
	library := roomRequest(t, h, "GET", uploadPath, "")
	if library.Code != 200 || !strings.Contains(library.Body.String(), `"document":`) {
		t.Fatal("library lost preparation", library.Body)
	}
	messages, err := store.ListMessages(context.Background(), room.ID, "", 0)
	if err != nil || len(messages) != 1 {
		t.Fatal("preparation started conversation", messages, err)
	}
}
func TestDocumentAPIValidationAndLocalBoundary(t *testing.T) {
	h, _, room, _ := preparedAPI(t)
	base := "/api/resources/" + room.ResourceID + "/document"
	for _, path := range []string{base, base + "?commit=HEAD&path=brief.pdf", base + "?commit=" + strings.Repeat("a", 40) + "&path=../secret.pdf", base + "?commit=x&commit=y&path=a.pdf"} {
		w := roomRequest(t, h, "GET", path, "")
		if w.Code != 400 {
			t.Fatalf("invalid selector %s: %d %s", path, w.Code, w.Body)
		}
	}
	for _, body := range []string{`{"commit":"HEAD","path":"brief.pdf"}`, `{"commit":"` + strings.Repeat("a", 40) + `","path":"../brief.pdf"}`, `{"commit":"` + strings.Repeat("a", 40) + `","path":"brief.pdf","extra":true}`} {
		w := roomRequest(t, h, "POST", base, body)
		if w.Code != 400 {
			t.Fatalf("bad preparation %d %s", w.Code, w.Body)
		}
	}
	request := httptest.NewRequest("GET", "http://localhost:3001/api/documents/missing", nil)
	request.RemoteAddr = "198.51.100.4:4000"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request)
	if w.Code != 403 {
		t.Fatal("document exposed outside local boundary")
	}
	if w := roomRequest(t, h, "GET", "/api/documents/missing", ""); w.Code != 404 {
		t.Fatal(w.Body)
	}
}
