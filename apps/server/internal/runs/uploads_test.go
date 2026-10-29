package runs

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestDSHIntegrationUploadedEvidenceRemainsReadOnlyAfterQueuedResourceSwitch(t *testing.T) {
	config := nativeConfig(t)
	var calls atomic.Int32
	m, s, _, room := integrationManager(t, config, func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		if calls.Add(1) == 1 {
			if !strings.Contains(string(body), `\"origin\":\"upload\"`) || !strings.Contains(string(body), "/references/1/customer-brief.md") {
				t.Error("uploaded evidence metadata missing from native prompt")
			}
			integrationMessage(t, w, map[string]any{"type": "tool_use", "id": "read-upload", "name": "bash", "input": map[string]string{"description": "Verify the isolated workflow", "command": `test "$(cat /references/1/customer-brief.md)" = 'Owner: Morgan. Deadline: Friday.' || exit 41
test ! -e /workspace/customer-brief.md || exit 42
if printf 'changed' > /references/1/customer-brief.md 2>/dev/null; then exit 43; fi
printf 'Morgan owns the Friday launch.\n' > /workspace/launch.md
printf 'UPLOAD_READ_ONLY_VERIFIED\n'`}}, "tool_use")
			return
		}
		if !strings.Contains(string(body), "UPLOAD_READ_ONLY_VERIFIED") {
			t.Error("native tool did not verify the actual uploaded file")
		}
		integrationMessage(t, w, map[string]any{"type": "text", "text": "Created launch.md from the original uploaded brief."}, "end_turn")
	})
	ctx := context.Background()
	content := []byte("Owner: Morgan. Deadline: Friday.\n")
	uploadID := strings.Repeat("c", 32)
	object, err := m.resources.Upload(ctx, uploadID, "customer-brief.md", content)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(content)
	receipt, err := s.RegisterRoomUpload(ctx, room.ID, "browser-upload", uploadID, object.Commit, "customer-brief.md", len(content), hex.EncodeToString(digest[:]))
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan struct{}, 1)
	m.mu.Lock()
	m.executeRun = func(ctx context.Context, e *execution) {
		m.mu.Lock()
		prompt := e.run.Prompt
		m.mu.Unlock()
		if prompt != "Hold this agent" {
			m.execute(ctx, e)
			return
		}
		started <- struct{}{}
		<-ctx.Done()
		m.finish(e, ctx, nil, "", ctx.Err())
	}
	m.mu.Unlock()
	blocked := sendTo(t, m, room, "Hold this agent").Runs[0]
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("first request did not hold the agent")
	}
	input := storage.SendMessageInput{RecipientAgentIDs: []string{room.DefaultAgentID}, RequestID: "use-upload", Content: "Read the uploaded customer brief and create launch.md.", References: []storage.ReferenceInput{{Kind: "file", ResourceID: uploadID, Commit: object.Commit, Path: receipt.Reference.Path}}}
	sent, err := m.Send(ctx, room.ID, input)
	if err != nil || len(sent.Runs) != 1 {
		t.Fatalf("admission failed: %+v %v", sent, err)
	}
	queued, err := m.Get(ctx, sent.Runs[0].ID)
	if err != nil || queued.Status != "queued" {
		t.Fatalf("expected queued work: %+v %v", queued, err)
	}
	originalResource := room.ResourceID
	replacementID := strings.Repeat("d", 32)
	replacement, err := m.resources.Create(ctx, replacementID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateResource(ctx, storage.Resource{ID: replacementID, Name: "Different working files", CurrentCommit: replacement.Commit}); err != nil {
		t.Fatal(err)
	}
	room.ResourceID = replacementID
	if _, err = s.UpdateRoom(ctx, room); err != nil {
		t.Fatal(err)
	}
	if _, err = m.Cancel(ctx, blocked.ID); err != nil {
		t.Fatal(err)
	}
	result := waitRun(t, m, sent.Runs[0].ID)
	if result.Status != "succeeded" || result.ResourceID != originalResource || !result.Published || calls.Load() != 2 {
		t.Fatalf("uploaded evidence execution failed: %+v calls=%d", result, calls.Load())
	}
	if len(result.References) != 1 || result.References[0].Origin != "upload" || result.References[0].Commit != object.Commit {
		t.Fatalf("frozen upload metadata missing: %+v", result.References)
	}
	unchanged, err := m.resources.ReadFile(ctx, uploadID, object.Commit, "customer-brief.md")
	if err != nil || string(unchanged) != string(content) {
		t.Fatalf("uploaded file changed: %q %v", unchanged, err)
	}
	files, err := m.resources.ListFiles(ctx, replacementID, replacement.Commit)
	if err != nil || len(files) != 0 {
		t.Fatalf("replacement resource was changed: %v %v", files, err)
	}
	if err = m.Shutdown(ctx); err != nil {
		t.Fatal(err)
	}
	leftovers, err := filepath.Glob(filepath.Join(m.dir, result.ID, ".references-*"))
	if err != nil || len(leftovers) != 0 {
		t.Fatalf("reference scratch remained: %v %v", leftovers, err)
	}
	replayed, err := m.Send(ctx, room.ID, input)
	if err != nil || !replayed.Replayed || replayed.Message.ID != sent.Message.ID || calls.Load() != 2 {
		t.Fatalf("exact retry did not preserve completed work: %+v %v", replayed, err)
	}
}
