package storage

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/testfixture"
)

func historicalDocument(t *testing.T, s *Store, resource, commit, id, status, text string) Document {
	t.Helper()
	d := Document{ID: id, ResourceID: resource, Commit: commit, Path: "brief.pdf", Format: "pdf", Status: status, ExtractorVersion: "retired-reader", Sections: []DocumentSection{}, Warnings: []string{}, CreatedAt: "2020-01-01T00:00:00Z", UpdatedAt: "2020-01-01T00:00:00Z"}
	if status == "ready" {
		d.Sections = []DocumentSection{{ID: "p1", Label: "Page 1", Text: text}}
	} else if status != "queued" && status != "extracting" {
		d.Error = &DocumentError{Code: "reader_failed", Message: "Previous reader failed.", Retryable: true}
	}
	testfixture.Document(t, s.db, d)
	return d
}

func TestHistoricalDocumentsAreReadOnlyAndPreferReadyEvidence(t *testing.T) {
	s, room, _ := roomFixture(t)
	commit := strings.Repeat("a", 40)
	ready := historicalDocument(t, s, room.ResourceID, commit, "legacy-ready", "ready", "Retained source text")
	for _, status := range []string{"queued", "extracting", "failed", "unavailable"} {
		historicalDocument(t, s, room.ResourceID, commit, "legacy-"+status, status, "")
	}
	before := map[string]string{}
	rows, err := s.db.Query(`SELECT id,snapshot FROM documents`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var id, body string
		if err := rows.Scan(&id, &body); err != nil {
			t.Fatal(err)
		}
		before[id] = body
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	latest, err := s.LatestDocument(ctx, room.ResourceID, commit, "brief.pdf")
	if err != nil || latest == nil || !reflect.DeepEqual(*latest, ready) {
		t.Fatalf("lost saved text behind newer pending/failed attempts: %+v %v", latest, err)
	}
	for id, body := range before {
		got, err := s.GetDocument(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if id == ready.ID {
			if !reflect.DeepEqual(got, ready) {
				t.Fatal("changed immutable READY artifact")
			}
		} else if got.Error == nil || got.Error.Retryable || !got.Terminal() {
			t.Fatalf("retired parser advertised active/retryable work: %+v", got)
		}
		var after string
		if err := s.db.QueryRow(`SELECT snapshot FROM documents WHERE id=?`, id).Scan(&after); err != nil || after != body {
			t.Fatal("reading changed historical row", id, err)
		}
	}
	assertScalar(t, s, "SELECT count(*) FROM documents", 5)
}

func TestOriginalOnlyDocumentsDoNotBlockAgentAdmission(t *testing.T) {
	s, room, _ := roomFixture(t)
	commit := strings.Repeat("a", 40)
	queued := historicalDocument(t, s, room.ResourceID, commit, "legacy-queued", "queued", "")
	input := SendMessageInput{RequestID: "original-only", Content: "Discuss this original honestly", References: []ReferenceInput{{Kind: "file", ResourceID: room.ResourceID, Commit: commit, Path: "brief.pdf"}}}
	sent, err := sendToAssistant(s, ctx, room.ID, input, roomBudget)
	if err != nil || len(sent.Runs) != 1 {
		t.Fatalf("original-only submission blocked: %+v %v", sent, err)
	}
	view := sent.Runs[0].References[0].Document
	if view == nil || view.ID != queued.ID || view.Status != "unavailable" || view.Error == nil || view.Error.Retryable {
		t.Fatalf("dishonest pending snapshot: %+v", view)
	}
	input.RequestID = "explicit-pending"
	input.References[0].DocumentID = queued.ID
	explicit, err := sendToAssistant(s, ctx, room.ID, input, roomBudget)
	if err != nil || explicit.Runs[0].References[0].Document.ID != queued.ID {
		t.Fatal("lost explicit retained ID", err)
	}
	input.RequestID = "unparsed-docx"
	input.References[0].Path = "new.docx"
	input.References[0].DocumentID = ""
	original, err := sendToAssistant(s, ctx, room.ID, input, roomBudget)
	if err != nil || original.Runs[0].References[0].Document != nil {
		t.Fatalf("unparsed DOCX blocked or invented text: %+v %v", original, err)
	}
	var status string
	if err := s.db.QueryRow(`SELECT status FROM documents WHERE id=?`, queued.ID).Scan(&status); err != nil || status != "queued" {
		t.Fatal("replayed/rewrote pending parser work", status, err)
	}
	assertScalar(t, s, "SELECT count(*) FROM documents", 1)
}

func TestDocumentAdmissionPreservesFrozenIDsAndVersionBoundaries(t *testing.T) {
	s, room, _ := roomFixture(t)
	commit := strings.Repeat("a", 40)
	first := historicalDocument(t, s, room.ResourceID, commit, "legacy-first", "ready", "Original evidence")
	input := SendMessageInput{RequestID: "review", Content: "Read saved evidence", References: []ReferenceInput{{Kind: "file", ResourceID: room.ResourceID, Commit: commit, Path: first.Path, DocumentID: first.ID}}}
	accepted, err := sendToAssistant(s, ctx, room.ID, input, roomBudget)
	if err != nil || accepted.Runs[0].References[0].Document.ID != first.ID {
		t.Fatal("lost exact artifact", err)
	}
	newer := historicalDocument(t, s, room.ResourceID, commit, "legacy-newer", "ready", "Different saved evidence")
	replay, err := sendToAssistant(s, ctx, room.ID, input, roomBudget)
	if err != nil || !replay.Replayed || replay.Runs[0].References[0].Document.ID != first.ID {
		t.Fatal("replay substituted a newer artifact", err)
	}
	input.References[0].DocumentID = newer.ID
	_, err = sendToAssistant(s, ctx, room.ID, input, roomBudget)
	requireCode(t, err, "request_conflict")
	input.RequestID = "wrong-file"
	input.References[0].Path = "other.pdf"
	_, err = sendToAssistant(s, ctx, room.ID, input, roomBudget)
	requireCode(t, err, "invalid_reference")
	stored, err := s.GetDocument(ctx, first.ID)
	a, _ := json.Marshal(stored)
	b, _ := json.Marshal(first)
	if err != nil || string(a) != string(b) {
		t.Fatal("frozen READY artifact changed", err)
	}
}
