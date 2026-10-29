package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/collaboration"
	"github.com/cofy-x/camp/apps/server/internal/resources"
	"github.com/cofy-x/camp/apps/server/internal/runs"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func uploadAPI(t *testing.T) (http.Handler, *storage.Store, storage.Room) {
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
	return New(store, nil, service), store, rooms[0]
}

func multipartUpload(t *testing.T, path, requestID, filename string, content []byte, extra func(*multipart.Writer)) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("requestId", requestID); err != nil {
		t.Fatal(err)
	}
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatal(err)
	}
	if extra != nil {
		extra(writer)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest("POST", "http://localhost:3001"+path, bytes.NewReader(body.Bytes()))
	r.RemoteAddr = "127.0.0.1:40000"
	r.Header.Set("Content-Type", writer.FormDataContentType())
	r.Header.Set("Origin", "http://localhost:3001")
	return r
}

func performUpload(t *testing.T, h http.Handler, r *http.Request, want int) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("%s: %d want %d: %s", r.URL.Path, w.Code, want, w.Body)
	}
	return w
}

func TestUploadAPIReceiptRetryDownloadAndPostOnly(t *testing.T) {
	h, store, room := uploadAPI(t)
	path := "/api/rooms/" + room.ID + "/uploads"
	data := []byte("# An onboarding brief\nA useful customer document.\n")
	// Discard the initial response as a client would after a connection loss.
	performUpload(t, h, multipartUpload(t, path, "stable-upload", "Customer brief.md", data, nil), 200)
	receipt := roomRequest(t, h, "GET", path+"/stable-upload", "")
	var saved struct {
		Reference storage.Reference `json:"reference"`
	}
	if err := json.Unmarshal(receipt.Body.Bytes(), &saved); err != nil || receipt.Code != 200 || saved.Reference.Origin != "upload" || saved.Reference.Size == nil || *saved.Reference.Size != len(data) {
		t.Fatalf("receipt %d %s %v", receipt.Code, receipt.Body, err)
	}
	replayed := performUpload(t, h, multipartUpload(t, path, "stable-upload", "Customer brief.md", data, nil), 200)
	if replayed.Body.String() != receipt.Body.String() {
		t.Fatalf("retry changed receipt: %s / %s", replayed.Body, receipt.Body)
	}
	performUpload(t, h, multipartUpload(t, path, "stable-upload", "Customer brief.md", []byte("changed"), nil), 409)
	ref := saved.Reference
	file := roomRequest(t, h, "GET", fmt.Sprintf("/api/resources/%s/file?path=Customer+brief.md&commit=%s", ref.ResourceID, ref.Commit), "")
	if file.Code != 200 || !bytes.Equal(file.Body.Bytes(), data) || file.Header().Get("Content-Type") != "application/octet-stream" || !strings.Contains(file.Header().Get("Content-Disposition"), "attachment") {
		t.Fatalf("download %d %s", file.Code, file.Body)
	}
	list := roomRequest(t, h, "GET", path, "")
	var library struct {
		References []storage.Reference `json:"references"`
		NextCursor string              `json:"nextCursor"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &library); err != nil || list.Code != 200 || len(library.References) != 1 || library.NextCursor != "" {
		t.Fatalf("library %d %s %v", list.Code, list.Body, err)
	}
	input := storage.SendMessageInput{RequestID: "discussion", Content: "Review this brief later.", PostOnly: true, References: []storage.ReferenceInput{{Kind: "file", ResourceID: ref.ResourceID, Commit: ref.Commit, Path: ref.Path}}}
	encoded, _ := json.Marshal(input)
	post := roomRequest(t, h, "POST", "/api/rooms/"+room.ID+"/messages", string(encoded))
	var sent storage.SendResult
	if err := json.Unmarshal(post.Body.Bytes(), &sent); err != nil || post.Code != 200 || len(sent.Runs) != 0 || len(sent.Message.References) != 1 || sent.Message.References[0].Origin != "upload" {
		t.Fatalf("post %d %s %v", post.Code, post.Body, err)
	}
	other, err := store.CreateRoom(context.Background(), storage.Room{Name: "Other discussion", ResourceID: room.ResourceID})
	if err != nil {
		t.Fatal(err)
	}
	if response := roomRequest(t, h, "GET", "/api/rooms/"+other.ID+"/uploads/stable-upload", ""); response.Code != 404 {
		t.Fatal(response.Body)
	}
	if response := roomRequest(t, h, "GET", "/api/rooms/"+other.ID+"/uploads?before="+ref.ResourceID, ""); response.Code != 400 {
		t.Fatal(response.Body)
	}
	if response := roomRequest(t, h, "PATCH", "/api/rooms/"+room.ID, `{"resourceId":"`+ref.ResourceID+`"}`); response.Code != 400 {
		t.Fatal(response.Body)
	}
	if response := roomRequest(t, h, "PATCH", "/api/resources/"+ref.ResourceID, `{"name":"Editable upload"}`); response.Code != 400 {
		t.Fatal(response.Body)
	}
	if response := roomRequest(t, h, "GET", "/api/resources", ""); strings.Contains(response.Body.String(), ref.ResourceID) {
		t.Fatal("uploaded evidence exposed as working resource")
	}
}

func TestUploadAPIRejectsUnsafeNamesMalformedPartsAndLimits(t *testing.T) {
	h, store, room := uploadAPI(t)
	path := "/api/rooms/" + room.ID + "/uploads"
	for _, filename := range []string{"../brief.md", "/brief.md", `folder\brief.md`, "folder/brief.md", ".env", ".env.example", "private.pem", "credentials.json", "bad\xff", strings.Repeat("x", 256)} {
		t.Run(filename, func(t *testing.T) {
			performUpload(t, h, multipartUpload(t, path, "invalid-name", filename, []byte("data"), nil), 400)
		})
	}
	for _, id := range []string{"", " ", ".", "..", "path/id", "bad\x00id", strings.Repeat("a", 129)} {
		performUpload(t, h, multipartUpload(t, path, id, "brief.md", []byte("data"), nil), 400)
	}
	for _, extra := range []func(*multipart.Writer){
		func(w *multipart.Writer) { _ = w.WriteField("unexpected", "value") },
		func(w *multipart.Writer) { _ = w.WriteField("requestId", "duplicate") },
		func(w *multipart.Writer) {
			p, _ := w.CreateFormFile("file", "second.md")
			_, _ = p.Write([]byte("second"))
		},
	} {
		performUpload(t, h, multipartUpload(t, path, "invalid-parts", "brief.md", []byte("first"), extra), 400)
	}
	large := multipartUpload(t, path, "large", "large.bin", make([]byte, resources.MaxFileBytes+1), nil)
	large.ContentLength = -1
	performUpload(t, h, large, 413)
	oversized := multipartUpload(t, path, "large-body", "brief.md", []byte("brief"), nil)
	oversized.ContentLength = resources.MaxFileBytes + 64<<10
	performUpload(t, h, oversized, 413)
	malformed := multipartUpload(t, path, "partial", "brief.md", []byte("brief"), nil)
	body, err := io.ReadAll(malformed.Body)
	if err != nil {
		t.Fatal(err)
	}
	malformed.Body = io.NopCloser(bytes.NewReader(body[:len(body)-40]))
	performUpload(t, h, malformed, 400)
	refs, _, err := store.ListRoomUploads(context.Background(), room.ID, "")
	if err != nil || len(refs) != 0 {
		t.Fatalf("invalid uploads left receipts: %+v %v", refs, err)
	}
}

func TestUploadAPIBinaryAndEmptyFilesRemainOriginalDownloads(t *testing.T) {
	h, _, room := uploadAPI(t)
	for i, item := range []struct {
		name, mime string
		data       []byte
	}{
		{"empty.txt", "text/plain", nil},
		{"report.html", "text/html", []byte("<script>window.pwned=true</script>")},
		{"archive.zip", "application/zip", []byte{'P', 'K', 0, 1, 255}},
	} {
		var body bytes.Buffer
		writer := multipart.NewWriter(&body)
		_ = writer.WriteField("requestId", fmt.Sprintf("type-%d", i))
		part, err := writer.CreatePart(textproto.MIMEHeader{"Content-Disposition": {mime.FormatMediaType("form-data", map[string]string{"name": "file", "filename": item.name})}, "Content-Type": {item.mime}})
		if err != nil {
			t.Fatal(err)
		}
		_, _ = part.Write(item.data)
		_ = writer.Close()
		r := httptest.NewRequest("POST", "http://localhost:3001/api/rooms/"+room.ID+"/uploads", &body)
		r.RemoteAddr = "127.0.0.1:40000"
		r.Header.Set("Content-Type", writer.FormDataContentType())
		response := performUpload(t, h, r, 200)
		var saved struct{ Reference storage.Reference }
		if err := json.Unmarshal(response.Body.Bytes(), &saved); err != nil {
			t.Fatal(err)
		}
		if saved.Reference.Size == nil || *saved.Reference.Size != len(item.data) {
			t.Fatal(saved.Reference)
		}
		file := roomRequest(t, h, "GET", fmt.Sprintf("/api/resources/%s/file?path=%s&commit=%s", saved.Reference.ResourceID, item.name, saved.Reference.Commit), "")
		if file.Code != 200 || !bytes.Equal(file.Body.Bytes(), item.data) || file.Header().Get("Content-Type") != "application/octet-stream" || file.Header().Get("Content-Disposition") != "attachment" {
			t.Fatalf("original download %d %s", file.Code, file.Body)
		}
	}
}

func TestUploadAPILocalBoundaryAndCancelledRequest(t *testing.T) {
	h, store, room := uploadAPI(t)
	path := "/api/rooms/" + room.ID + "/uploads"
	for _, change := range []func(*http.Request){
		func(r *http.Request) { r.RemoteAddr = "192.0.2.1:40000" },
		func(r *http.Request) { r.Host = "evil.test" },
		func(r *http.Request) { r.Header.Set("Origin", "http://evil.test") },
	} {
		r := multipartUpload(t, path, "blocked", "brief.md", []byte("brief"), nil)
		change(r)
		performUpload(t, h, r, 403)
	}
	r := multipartUpload(t, path, "cancelled", "brief.md", []byte("brief"), nil)
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r.WithContext(ctx))
	if w.Body.Len() != 0 {
		t.Fatal("cancelled request wrote a response")
	}
	refs, _, err := store.ListRoomUploads(context.Background(), room.ID, "")
	if err != nil || len(refs) != 0 {
		t.Fatalf("blocked or cancelled upload saved: %+v %v", refs, err)
	}
}
