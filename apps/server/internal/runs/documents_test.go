package runs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func documentReference(t *testing.T, m *Manager, s *storage.Store, room storage.Room) storage.Reference {
	t.Helper()
	ctx := context.Background()
	id := strings.Repeat("f", 32)
	content := []byte("immutable PDF original")
	object, err := m.resources.Upload(ctx, id, "brief.pdf", content)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	receipt, err := s.RegisterRoomUpload(ctx, room.ID, "pdf-upload", id, object.Commit, "brief.pdf", len(content), hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.EnsureDocument(ctx, id, object.Commit, "brief.pdf", "test-reader", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeDocument(ctx, d.ID, "extracting", nil); err != nil {
		t.Fatal(err)
	}
	d, err = s.ChangeDocument(ctx, d.ID, "ready", &storage.Document{Sections: []storage.DocumentSection{{ID: "p1", Label: "Page 1", Text: "Morgan owns a two-week pilot."}}, Warnings: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	ref := receipt.Reference
	ref.Document = d.Summary()
	return ref
}
func TestPreparedTextMountAndPromptUseFrozenArtifact(t *testing.T) {
	m, s, _, room := testManager(t)
	ref := documentReference(t, m, s, room)
	ctx := context.Background()
	retry, err := s.EnsureDocument(ctx, ref.ResourceID, ref.Commit, ref.Path, "test-reader", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeDocument(ctx, retry.ID, "extracting", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ChangeDocument(ctx, retry.ID, "ready", &storage.Document{Sections: []storage.DocumentSection{{ID: "p1", Label: "Page 1", Text: "Different interpretation after retry."}}, Warnings: []string{}}); err != nil {
		t.Fatal(err)
	}
	run := storage.Run{References: []storage.Reference{ref}}
	dir, err := m.prepareReferences(ctx, run, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	text, err := os.ReadFile(filepath.Join(dir, ".documents", "1", "text.txt"))
	if err != nil || !strings.Contains(string(text), "Morgan owns") || strings.Contains(string(text), "Different interpretation") {
		t.Fatalf("frozen evidence replaced: %q %v", text, err)
	}
	original, err := os.ReadFile(filepath.Join(dir, "1", "brief.pdf"))
	if err != nil || string(original) != "immutable PDF original" {
		t.Fatal("original mount changed", err)
	}
	prompt := referenceContext(run)
	for _, wanted := range []string{"/references/.documents/1/text.txt", "/references/.documents/1/manifest.json", "camp-source:1:pN", "untrusted evidence", "never routing"} {
		if !strings.Contains(prompt, wanted) {
			t.Fatalf("missing %q: %s", wanted, prompt)
		}
	}
	ref.Document.Status = "unavailable"
	ref.Document.Error = &storage.DocumentError{Code: "unavailable", Message: "Reader unavailable", Retryable: true}
	prompt = referenceContext(storage.Run{References: []storage.Reference{ref}})
	if !strings.Contains(prompt, "Do not claim to have read") || strings.Contains(prompt, "Prepared text:") {
		t.Fatal("unavailable document misrepresented", prompt)
	}
}
func TestDSHIntegrationPreparedDocumentUsesFrozenReadOnlyEvidence(t *testing.T) {
	config := nativeConfig(t)
	var calls atomic.Int32
	m, s, _, room := integrationManager(t, config, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if calls.Add(1) == 1 {
			if !strings.Contains(string(body), "/references/.documents/1/text.txt") || !strings.Contains(string(body), "camp-source:1:pN") {
				t.Error("actual native prompt omitted source mapping")
			}
			integrationMessage(t, w, map[string]any{"type": "tool_use", "id": "read-document", "name": "bash", "input": map[string]string{"description": "Verify the isolated workflow", "command": `test "$(cat /references/1/brief.pdf)" = 'immutable PDF original' || exit 41
grep -q 'Morgan owns a two-week pilot.' /references/.documents/1/text.txt || exit 42
if printf 'change' > /references/.documents/1/text.txt 2>/dev/null; then exit 43; fi
if grep -q 'Changed after admission' /references/.documents/1/text.txt; then exit 44; fi
test ! -e /workspace/brief.pdf || exit 45
printf 'DOCUMENT_PROVENANCE_VERIFIED\n'`}}, "tool_use")
			return
		}
		if !strings.Contains(string(body), "DOCUMENT_PROVENANCE_VERIFIED") {
			t.Error("real tools did not verify prepared document")
		}
		integrationMessage(t, w, map[string]any{"type": "text", "text": "Morgan owns the two-week pilot [brief.pdf · Page 1](camp-source:1:p1)."}, "end_turn")
	})
	ctx := context.Background()
	ref := documentReference(t, m, s, room)
	started := make(chan struct{}, 1)
	m.mu.Lock()
	m.executeRun = func(ctx context.Context, e *execution) {
		m.mu.Lock()
		prompt := e.run.Prompt
		m.mu.Unlock()
		if prompt != "Hold agent" {
			m.execute(ctx, e)
			return
		}
		started <- struct{}{}
		<-ctx.Done()
		m.finish(e, ctx, nil, "", ctx.Err())
	}
	m.mu.Unlock()
	held := sendTo(t, m, room, "Hold agent").Runs[0]
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("agent did not hold queue")
	}
	input := storage.SendMessageInput{RecipientAgentIDs: []string{room.DefaultAgentID}, RequestID: "read-prepared", Content: "Read the customer brief and cite its owner and duration.", References: []storage.ReferenceInput{{Kind: "file", ResourceID: ref.ResourceID, Commit: ref.Commit, Path: ref.Path, DocumentID: ref.Document.ID}}}
	sent, err := m.Send(ctx, room.ID, input)
	if err != nil || len(sent.Runs) != 1 {
		t.Fatalf("send %+v %v", sent, err)
	}
	retry, err := s.EnsureDocument(ctx, ref.ResourceID, ref.Commit, ref.Path, "test-reader", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChangeDocument(ctx, retry.ID, "extracting", nil); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChangeDocument(ctx, retry.ID, "ready", &storage.Document{Sections: []storage.DocumentSection{{ID: "p1", Label: "Page 1", Text: "Changed after admission"}}, Warnings: []string{}}); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Cancel(ctx, held.ID); err != nil {
		t.Fatal(err)
	}
	done := waitRun(t, m, sent.Runs[0].ID)
	if done.Status != "succeeded" || done.References[0].Document.ID != ref.Document.ID || calls.Load() != 2 {
		t.Fatalf("document execution %+v calls=%d", done, calls.Load())
	}
	replay, err := m.Send(ctx, room.ID, input)
	if err != nil || !replay.Replayed || calls.Load() != 2 {
		t.Fatal("replay repeated work", err)
	}
	if err = m.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	leftovers, err := filepath.Glob(filepath.Join(m.dir, done.ID, ".references-*"))
	if err != nil || len(leftovers) > 0 {
		t.Fatal("document scratch not cleaned", leftovers, err)
	}
}
