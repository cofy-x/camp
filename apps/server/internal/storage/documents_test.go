package storage

import (
	"reflect"
	"strings"
	"sync"
	"testing"
)

func readyDocument(t *testing.T, s *Store, d Document, text string) Document {
	t.Helper()
	if _, err := s.ChangeDocument(ctx, d.ID, "extracting", nil); err != nil {
		t.Fatal(err)
	}
	d, err := s.ChangeDocument(ctx, d.ID, "ready", &Document{Sections: []DocumentSection{{ID: "p1", Label: "Page 1", Text: text}}, Warnings: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	return d
}
func TestDocumentsConvergeAndTerminalArtifactsAreImmutable(t *testing.T) {
	s, room, _ := roomFixture(t)
	var wg sync.WaitGroup
	ids := make(chan string, 8)
	for range 8 {
		wg.Go(func() {
			d, err := s.EnsureDocument(ctx, room.ResourceID, strings.Repeat("a", 40), "brief.pdf", "test-v1", false)
			if err != nil {
				t.Error(err)
				return
			}
			ids <- d.ID
		})
	}
	wg.Wait()
	close(ids)
	var first string
	for id := range ids {
		if first != "" && first != id {
			t.Fatal("concurrent preparation duplicated")
		}
		first = id
	}
	d, err := s.GetDocument(ctx, first)
	if err != nil {
		t.Fatal(err)
	}
	d = readyDocument(t, s, d, "Original extracted evidence")
	_, err = s.ChangeDocument(ctx, d.ID, "queued", nil)
	requireCode(t, err, "document_finished")
	next, err := s.EnsureDocument(ctx, d.ResourceID, d.Commit, d.Path, "test-v1", true)
	if err != nil || next.ID == d.ID {
		t.Fatalf("retry did not append: %+v %v", next, err)
	}
	pending, err := s.EnsureDocument(ctx, d.ResourceID, d.Commit, d.Path, "test-v1", true)
	if err != nil || pending.ID != next.ID {
		t.Fatal("concurrent retry duplicated queued attempt")
	}
	previous, err := s.GetDocument(ctx, d.ID)
	if err != nil || !reflect.DeepEqual(previous, d) {
		t.Fatal("retry mutated terminal artifact")
	}
	upgraded, err := s.EnsureDocument(ctx, d.ResourceID, d.Commit, d.Path, "test-v2", false)
	if err != nil || upgraded.ID == next.ID {
		t.Fatal("parser upgrade did not create artifact")
	}
	events, err := s.ListWorkspaceEvents(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, event := range events {
		if event.Type == "document.updated" {
			count++
			if event.RoomID != room.ID {
				t.Fatal("event has wrong room")
			}
		}
	}
	if count != 5 {
		t.Fatalf("events %d", count)
	}
}
func TestDocumentAdmissionFreezesTerminalArtifactAndRetryIdentity(t *testing.T) {
	s, room, _ := roomFixture(t)
	commit := strings.Repeat("a", 40)
	d, err := s.EnsureDocument(ctx, room.ResourceID, commit, "brief.pdf", "v1", false)
	if err != nil {
		t.Fatal(err)
	}
	input := SendMessageInput{RequestID: "review", Content: "Review this document", References: []ReferenceInput{{Kind: "file", ResourceID: room.ResourceID, Commit: commit, Path: "brief.pdf"}}}
	_, err = sendToAssistant(s, ctx, room.ID, input, roomBudget)
	requireCode(t, err, "document_preparing")
	assertScalar(t, s, "SELECT count(*) FROM messages", 0)
	post := input
	post.RequestID = "discussion"
	post.PostOnly = true
	posted, err := sendToAssistant(s, ctx, room.ID, post, roomBudget)
	if err != nil || len(posted.Runs) != 0 || posted.Message.References[0].Document != nil {
		t.Fatalf("post while pending %+v %v", posted, err)
	}
	d = readyDocument(t, s, d, "First version")
	accepted, err := sendToAssistant(s, ctx, room.ID, input, roomBudget)
	if err != nil || accepted.Runs[0].References[0].Document.ID != d.ID {
		t.Fatalf("freeze %+v %v", accepted, err)
	}
	next, err := s.EnsureDocument(ctx, d.ResourceID, d.Commit, d.Path, "v1", true)
	if err != nil {
		t.Fatal(err)
	}
	replay, err := sendToAssistant(s, ctx, room.ID, input, roomBudget)
	if err != nil || !replay.Replayed || replay.Runs[0].References[0].Document.ID != d.ID {
		t.Fatalf("replay %+v %v", replay, err)
	}
	input.RequestID = "new-during-retry"
	_, err = sendToAssistant(s, ctx, room.ID, input, roomBudget)
	requireCode(t, err, "document_preparing")
	input.References[0].DocumentID = d.ID
	explicit, err := sendToAssistant(s, ctx, room.ID, input, roomBudget)
	if err != nil || explicit.Runs[0].References[0].Document.ID != d.ID {
		t.Fatal("explicit old immutable artifact rejected", err)
	}
	next = readyDocument(t, s, next, "New version")
	input.References[0].DocumentID = next.ID
	_, err = sendToAssistant(s, ctx, room.ID, input, roomBudget)
	requireCode(t, err, "request_conflict")
	input.RequestID = "mismatch"
	input.References[0].Path = "other.pdf"
	_, err = sendToAssistant(s, ctx, room.ID, input, roomBudget)
	requireCode(t, err, "invalid_reference")
	input.References[0].Path = d.Path
	input.References = append(input.References, ReferenceInput{Kind: "file", ResourceID: d.ResourceID, Commit: d.Commit, Path: d.Path, DocumentID: d.ID})
	_, err = sendToAssistant(s, ctx, room.ID, input, roomBudget)
	requireCode(t, err, "invalid_reference")
}
func TestDocumentFailuresAreHonestAndResultsAtomic(t *testing.T) {
	s, room, _ := roomFixture(t)
	d, err := s.EnsureDocument(ctx, room.ResourceID, strings.Repeat("a", 40), "scan.pdf", "v1", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ChangeDocument(ctx, d.ID, "extracting", nil); err != nil {
		t.Fatal(err)
	}
	_, err = s.ChangeDocument(ctx, d.ID, "ready", &Document{Sections: []DocumentSection{{ID: "p20", Label: "Page 20", Text: "invalid location"}}})
	requireCode(t, err, "invalid_document")
	current, _ := s.GetDocument(ctx, d.ID)
	if current.Status != "extracting" {
		t.Fatal("failed result partially saved")
	}
	failure := &DocumentError{Code: "no_text", Message: "No selectable text was found. OCR is not available.", Retryable: false}
	failed, err := s.ChangeDocument(ctx, d.ID, "unreadable", &Document{Error: failure, Sections: []DocumentSection{}, Warnings: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	sent, err := sendToAssistant(s, ctx, room.ID, SendMessageInput{RequestID: "original-only", Content: "Discuss this limitation", References: []ReferenceInput{{Kind: "file", ResourceID: d.ResourceID, Commit: d.Commit, Path: d.Path}}}, roomBudget)
	if err != nil || sent.Runs[0].References[0].Document.Error.Code != "no_text" || failed.Status != "unreadable" {
		t.Fatal("failure snapshot missing", err)
	}
}
