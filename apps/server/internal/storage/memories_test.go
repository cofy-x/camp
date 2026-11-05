package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func memoryNote(request, content string) orchestration.MemoryInput {
	return orchestration.MemoryInput{RequestID: request, Content: content, Provenance: orchestration.MemoryProvenance{Kind: "human_note"}}
}

func TestMemoryImmutableSupersessionRetractionAndFrozenEvidence(t *testing.T) {
	s, actor, member, room, _, _ := taskFixture(t)
	input := memoryNote("remember", "The launch language is English.")
	first, err := s.SaveMemory(actor, room.ID, "", input)
	if err != nil || first.Revision != 1 || first.State != "active" || !first.Current {
		t.Fatal(first, err)
	}
	if _, err = s.SaveMemory(member, "", first.ID, orchestration.MemoryInput{RequestID: "not-owner", Revision: 1, Content: "Changed", Provenance: input.Provenance}); err == nil {
		t.Fatal("another member changed the author's memory")
	}
	var frozen []orchestration.Memory
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		var e error
		frozen, e = SnapshotMemories(ctx, sqlgen.New(tx), room.ID, "launch", 10)
		return e
	})
	if err != nil || len(frozen) != 1 {
		t.Fatal(frozen, err)
	}
	second, err := s.SaveMemory(actor, "", first.ID, orchestration.MemoryInput{RequestID: "supersede", Revision: 1, Content: "The launch language is Chinese.", Provenance: input.Provenance})
	if err != nil || second.Revision != 2 || second.SupersedesRevision != 1 {
		t.Fatal(second, err)
	}
	historical, err := s.GetMemory(member, first.ID, 1)
	if err != nil || historical.Current || historical.Content != first.Content || frozen[0].Content != first.Content || frozen[0].Revision != 1 {
		t.Fatal("historical/frozen evidence changed", historical, frozen, err)
	}
	retracted, err := s.RetractMemory(actor, first.ID, orchestration.MemoryRetraction{RequestID: "retract", Revision: 2})
	if err != nil || retracted.Revision != 3 || retracted.State != "retracted" || retracted.SupersedesRevision != 2 {
		t.Fatal(retracted, err)
	}
	items, err := s.Memories(member, room.ID, "", 20)
	if err != nil || len(items) != 0 {
		t.Fatal("retracted evidence was retrieved", items, err)
	}
	replayed, err := s.SaveMemory(actor, room.ID, "", input)
	if err != nil || replayed != first {
		t.Fatal("replay did not retain original response", replayed, err)
	}
	input.Content = "Changed intent"
	_, err = s.SaveMemory(actor, room.ID, "", input)
	requireCode(t, err, "request_conflict")
	if _, err = s.db.Exec("UPDATE memory_revisions SET content='rewritten' WHERE memory_id=? AND revision=1", first.ID); err == nil {
		t.Fatal("immutable revision permitted UPDATE")
	}
	if _, err = s.db.Exec("DELETE FROM memory_revisions WHERE memory_id=? AND revision=1", first.ID); err == nil {
		t.Fatal("immutable revision permitted DELETE")
	}
}

func TestMemoryProvenanceMustResolveWithinRoom(t *testing.T) {
	s, actor, _, room, _, _ := taskFixture(t)
	other, err := s.CreateRoom(actor, Room{Name: "Other", ResourceID: room.ResourceID})
	if err != nil {
		t.Fatal(err)
	}
	message := sendAutomationDiscussion(t, s, actor, room, "Launch date: Friday.").Message
	otherMessage := sendAutomationDiscussion(t, s, actor, other, "Private scope.").Message
	for _, provenance := range []orchestration.MemoryProvenance{
		{Kind: "message", MessageID: message.ID},
		{Kind: "source_event", SourceEventID: "room:" + message.ID},
	} {
		_, err = s.SaveMemory(actor, room.ID, "", orchestration.MemoryInput{RequestID: newID(), Content: "Friday launch", Provenance: provenance})
		if err != nil {
			t.Fatal(provenance, err)
		}
	}
	for _, provenance := range []orchestration.MemoryProvenance{
		{Kind: "message", MessageID: otherMessage.ID},
		{Kind: "source_event", SourceEventID: "room:" + otherMessage.ID},
		{Kind: "human_note", MessageID: message.ID},
		{Kind: "message", MessageID: message.ID, SourceEventID: "room:" + message.ID},
		{Kind: "source_event", SourceEventID: "missing"},
		{Kind: "file", ResourceID: room.ResourceID, Commit: "HEAD", Path: "readme.md"},
		{Kind: "file", ResourceID: room.ResourceID, Commit: strings.Repeat("f", 40), Path: "readme.md"},
		{Kind: "file", ResourceID: room.ResourceID, Commit: strings.Repeat("a", 40), Path: "../secret"},
	} {
		if _, err = s.SaveMemory(actor, room.ID, "", orchestration.MemoryInput{RequestID: newID(), Content: "Invalid evidence", Provenance: provenance}); err == nil {
			t.Fatal("accepted invalid provenance", provenance)
		}
	}
	unrelated, err := s.CreateResource(actor, Resource{Name: "Unrelated", CurrentCommit: strings.Repeat("d", 40), Environment: json.RawMessage(`{"kind":"command","checks":[]}`)})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.SaveMemory(actor, room.ID, "", orchestration.MemoryInput{RequestID: newID(), Content: "Cross-room file", Provenance: orchestration.MemoryProvenance{Kind: "file", ResourceID: unrelated.ID, Commit: unrelated.CurrentCommit, Path: "readme.md"}})
	requireCode(t, err, "invalid_memory")
}

func TestMemoryCurrentAccessBoundsAndCancelledRequest(t *testing.T) {
	s, actor, member, room, _, _ := taskFixture(t)
	created, err := s.SaveMemory(member, room.ID, "", memoryNote("member-note", "Room context"))
	if err != nil {
		t.Fatal(err)
	}
	user, _ := UserFromContext(member)
	disabled := false
	if _, err = s.UpdateUserAccess(actor, user.ID, nil, &disabled); err != nil {
		t.Fatal(err)
	}
	_, err = s.Memories(member, room.ID, "", 10)
	requireCode(t, err, "unauthenticated")
	_, err = s.GetMemory(member, created.ID, 1)
	requireCode(t, err, "unauthenticated")
	_, err = s.MemoryRequest(member, "member-note")
	requireCode(t, err, "unauthenticated")
	_, err = s.SaveMemory(member, room.ID, "", memoryNote("late", "Denied"))
	requireCode(t, err, "unauthenticated")
	if _, err = s.CancelMemoryRequest(actor, "cancelled"); err != nil {
		t.Fatal(err)
	}
	_, err = s.SaveMemory(actor, room.ID, "", memoryNote("cancelled", "Must not be saved"))
	requireCode(t, err, "request_cancelled")
	for _, query := range []string{strings.Repeat("q", MaxMemoryQueryBytes+1), "invalid\x00text"} {
		_, err = s.Memories(actor, room.ID, query, 10)
		requireCode(t, err, "invalid_memory")
	}
	_, err = s.Memories(actor, room.ID, "", MaxMemoryResults+1)
	requireCode(t, err, "invalid_memory")
	for range 12 {
		if _, err = s.SaveMemory(actor, room.ID, "", memoryNote(newID(), strings.Repeat("x", MaxMemoryContentBytes))); err != nil {
			t.Fatal(err)
		}
	}
	items, err := s.Memories(actor, room.ID, "XXX", 20)
	if err != nil || len(items) == 0 || len(items) >= 12 {
		t.Fatal(items, err)
	}
	encoded, _ := json.Marshal(items)
	if len(encoded)+1 > memoryResultBytes {
		t.Fatal("unbounded memory response", len(encoded))
	}
}

func TestMemoryConcurrentSupersessionUsesOneRevision(t *testing.T) {
	s, actor, _, room, _, _ := taskFixture(t)
	m, err := s.SaveMemory(actor, room.ID, "", memoryNote("initial", "Original"))
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 2)
	for _, text := range []string{"First", "Second"} {
		wg.Go(func() {
			in := memoryNote(text, text)
			in.Revision = 1
			_, err := s.SaveMemory(actor, "", m.ID, in)
			results <- err
		})
	}
	wg.Wait()
	close(results)
	successes := 0
	for err := range results {
		if err == nil {
			successes++
		} else {
			requireCode(t, err, "revision_conflict")
		}
	}
	if successes != 1 {
		t.Fatal("concurrent edits did not converge", successes)
	}
	assertScalar(t, s, "SELECT count(*) FROM memory_revisions", 2)
}

func TestMemoryPersistsWithoutNativeSession(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "team.sqlite")
	s, err := Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	actor, _ := teamUsers(t, s)
	resource, err := s.CreateResource(actor, Resource{Name: "Files", CurrentCommit: strings.Repeat("a", 40), Environment: json.RawMessage(`{"kind":"command","checks":[]}`)})
	if err != nil {
		t.Fatal(err)
	}
	room, err := s.CreateRoom(actor, Room{Name: "Room", ResourceID: resource.ID})
	if err != nil {
		t.Fatal(err)
	}
	memory, err := s.SaveMemory(actor, room.ID, "", memoryNote("persistent", "Independent durable memory"))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(filename)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	read, err := s.GetMemory(actor, memory.ID, 1)
	if err != nil || read != memory {
		t.Fatal(read, err)
	}
	var snapshots []orchestration.Memory
	err = s.readTransaction(context.Background(), func(tx *sql.Tx) error {
		var e error
		snapshots, e = SnapshotMemories(ctx, sqlgen.New(tx), room.ID, "durable", 10)
		return e
	})
	if err != nil || len(snapshots) != 1 || snapshots[0].ID != memory.ID {
		t.Fatal(snapshots, err)
	}
}

func TestMemoryFileProvenanceRetainsSupersededRetractedVersions(t *testing.T) {
	s, actor, _, room, _, _ := taskFixture(t)
	resource, err := s.GetResource(actor, room.ResourceID)
	if err != nil {
		t.Fatal(err)
	}
	m, err := s.SaveMemory(actor, room.ID, "", orchestration.MemoryInput{RequestID: "file-memory", Content: "Summary of original bytes", Provenance: orchestration.MemoryProvenance{Kind: "file", ResourceID: resource.ID, Commit: resource.CurrentCommit, Path: "report.md"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec("UPDATE resources SET current_commit=? WHERE id=?", strings.Repeat("f", 40), resource.ID); err != nil {
		t.Fatal(err)
	}
	edit := memoryNote("replace-source", "A human-authored replacement")
	edit.Revision = 1
	if _, err = s.SaveMemory(actor, "", m.ID, edit); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RetractMemory(actor, m.ID, orchestration.MemoryRetraction{RequestID: "remove", Revision: 2}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ValidateResourceRevision(actor, resource.ID, resource.CurrentCommit); err != nil {
		t.Fatal("superseded memory lost exact revision", err)
	}
	rows, err := s.queries().InspectionRevisions(actor)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, row := range rows {
		if row.ResourceID == resource.ID && row.CommitID == resource.CurrentCommit {
			found = true
		}
	}
	if !found {
		t.Fatal("stopped retention omitted historical memory provenance", rows)
	}
}

func TestMemoryRunFreezesOnceAndRejectsCallerReplacement(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	m, err := s.SaveMemory(actor, room.ID, "", memoryNote("first-memory", "Original frozen memory"))
	if err != nil {
		t.Fatal(err)
	}
	task := startTask(t, s, actor, room, writer)
	run, err := s.GetRun(ctx, task.ActiveRunID)
	if err != nil {
		t.Fatal(err)
	}
	run, attempt := prepareTaskTestAttempt(t, s, run)
	if !run.MemoriesFrozen || len(run.Memories) != 1 || run.Memories[0].Revision != 1 {
		t.Fatal(run.Memories)
	}
	edit := memoryNote("later-memory", "Later replacement memory")
	edit.Revision = 1
	if _, err = s.SaveMemory(actor, "", m.ID, edit); err != nil {
		t.Fatal(err)
	}
	read, err := s.GetRun(ctx, run.ID)
	if err != nil || read.Memories[0].Content != m.Content || read.Memories[0].Revision != 1 {
		t.Fatal(read.Memories, err)
	}
	read.Status = "running"
	read.Memories = nil
	read.MemoriesFrozen = false
	updated, err := s.UpdateRun(ctx, read)
	if err != nil || !updated.MemoriesFrozen || len(updated.Memories) != 1 {
		t.Fatal("caller replaced frozen evidence", updated.Memories, err)
	}
	updated.Status = "succeeded"
	updated.Memories = []orchestration.Memory{{ID: "forged"}}
	updated.MemoriesFrozen = false
	updated.Intent = &CoordinationIntent{CallID: "finish", Kind: "finish", Summary: "Done"}
	finished, err := s.FinishRemoteRun(ctx, attempt.ID, run.RunnerID, "finish-memory", strings.Repeat("a", 64), updated, "Done", true, true)
	if err != nil || !finished.MemoriesFrozen || len(finished.Memories) != 1 || finished.Memories[0].ID != m.ID {
		t.Fatal("completion replaced frozen evidence", finished.Memories, err)
	}
}
