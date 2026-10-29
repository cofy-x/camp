package remoteruns

import (
	"archive/tar"
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/nativeexecutor"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/runs"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func TestSourcePublicationConflictSealsFailureAndReplaysReceipt(t *testing.T) {
	h := newRemoteHarness(t, runs.Config{})
	config := h.join(t, "source-collision", "fixture", nil, nativeexecutor.Config{})
	_, err := h.store.ConnectRunner(h.ctx, config.RunnerID, storage.RunnerConnection{StorageID: config.StorageID, Runtime: "fixture", Slots: 1, Capabilities: json.RawMessage(`{"executor":"fixture","ready":true}`), Sources: []storage.RunnerSource{{ID: "project", Alias: "Project", Revision: "source-v1", Available: true}}})
	if err != nil {
		t.Fatal(err)
	}
	input := storage.SourceImportInput{RequestID: "capture-before-collision", RunnerID: config.RunnerID, SourceID: "project", ExpectedSourceRevision: "source-v1", RoomName: "Imported work"}
	item, err := h.store.CreateSourceImport(h.actor, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.store.ClaimSourceImport(h.ctx, item.ID, config.RunnerID, 5*time.Minute); err != nil {
		t.Fatal(err)
	}
	h.room(t, input.RoomName, config.RunnerID)
	input.RequestID = "name-already-taken"
	if _, err = h.store.CreateSourceImport(h.actor, input); errorCode(err) != "name_conflict" {
		t.Fatal("An existing Room name was not rejected before capture", err)
	}
	var archive bytes.Buffer
	writer := tar.NewWriter(&archive)
	if err = writer.WriteHeader(&tar.Header{Name: "brief.txt", Mode: 0644, Size: 5}); err != nil {
		t.Fatal(err)
	}
	if _, err = writer.Write([]byte("brief")); err != nil {
		t.Fatal(err)
	}
	if err = writer.Close(); err != nil {
		t.Fatal(err)
	}
	base := "/api/daemon/operations/" + item.ID
	hash := digest(archive.Bytes())
	if receipt := h.request(t, config, "PUT", base+"/artifact", archive.Bytes(), map[string]string{"X-Camp-SHA256": hash, "Content-Type": "application/x-tar"}); receipt.Disposition != "accepted" {
		t.Fatal(receipt)
	}
	result := p.Result{CompletionID: "captured-once", Status: "succeeded", CleanupConfirmed: true, Checks: []p.CheckResult{}, Archive: &p.Artifact{SHA256: hash, Bytes: int64(archive.Len())}}
	data, _ := json.Marshal(result)
	for range 2 {
		receipt := h.request(t, config, "POST", base+"/complete", data, nil)
		if receipt.Disposition != "committed" || receipt.Status != "failed" || !strings.Contains(receipt.Reason, "name") {
			t.Fatal("Publication conflict did not replay a recorded failed outcome", receipt)
		}
	}
	item, err = h.store.GetSourceImport(h.ctx, item.ID)
	if err != nil || item.Status != "failed" || item.CompletionID != result.CompletionID || item.ResultDigest == "" || item.ArtifactDigest != hash || item.RoomID != "" {
		t.Fatal("Rejected publication lost its durable receipt", item, err)
	}
	if _, err = h.store.GetResource(h.ctx, item.ResourceID); errorCode(err) != "not_found" {
		t.Fatal("Rejected publication created a shared resource", err)
	}
	input.RequestID, input.RoomName = "after-collision", "Another imported snapshot"
	next, err := h.store.CreateSourceImport(h.actor, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = h.store.ClaimSourceImport(h.ctx, next.ID, config.RunnerID, time.Minute); err != nil {
		t.Fatal("Rejected publication retained the Runner slot", err)
	}
}
