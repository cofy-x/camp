package storage

import (
	"database/sql"
	"fmt"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func TestOutputChunksMatchTheExistingTailWithoutRewritingFrozenRun(t *testing.T) {
	s, room, _ := roomFixture(t)
	sent := sendRoom(t, s, room, "stream-chunks")
	id := sent.Runs[0].ID
	var before string
	if err := s.db.QueryRow("SELECT snapshot FROM runs WHERE id=?", id).Scan(&before); err != nil {
		t.Fatal(err)
	}
	want := ""
	additions := []string{"a", "é", "你好🙂", strings.Repeat("中🙂\n", 1300), strings.Repeat("q", 4095), strings.Repeat("line\n", 1900)}
	for i := 0; i < 250; i++ {
		addition := additions[i%len(additions)]
		// The independent reference is the pre-split implementation, including its
		// precise replacement-character and truncation-marker behavior.
		text := strings.ToValidUTF8(want+addition, "�")
		if len(text) > MaxRunOutputSize {
			text = runOutputMarker + strings.ToValidUTF8(text[len(text)-(MaxRunOutputSize-len(runOutputMarker)-3):], "�")
		}
		want = text
		stamp := fmt.Sprintf("2026-11-11T14:00:%02d.%09dZ", i%60, i)
		err := s.transaction(ctx, func(tx *sql.Tx) error { return appendRunOutput(ctx, sqlgen.New(tx), id, addition, stamp) })
		if err != nil {
			t.Fatal(i, err)
		}
		got, err := s.GetRun(ctx, id)
		if err != nil || got.Output != want || got.UpdatedAt != stamp {
			t.Fatalf("tail changed at %d: got %d bytes, want %d: %v", i, len(got.Output), len(want), err)
		}
	}
	var after string
	if err := s.db.QueryRow("SELECT snapshot FROM runs WHERE id=?", id).Scan(&after); err != nil || before != after {
		t.Fatal("stream rewrote frozen Run", err)
	}
	var count, bytes, maxChunk int
	if err := s.db.QueryRow("SELECT count(*),coalesce(sum(length(CAST(text AS BLOB))),0),coalesce(max(length(CAST(text AS BLOB))),0) FROM run_output_chunks WHERE run_id=?", id).Scan(&count, &bytes, &maxChunk); err != nil || count > 66 || maxChunk > runOutputChunkBytes {
		t.Fatal("projection grew with retained output", count, maxChunk, err)
	}
	if bytes+len(runOutputMarker) != len(want) {
		t.Fatal("projection size differs from exact output")
	}
}

func TestLocalOutputWritesActivityAndKeepsOriginalEvents(t *testing.T) {
	s, room, _ := roomFixture(t)
	sent := sendRoom(t, s, room, "local-stream")
	before, _ := s.queries().WorkspaceEventCursor(ctx)
	update, err := s.RecordRunOutput(ctx, sent.Runs[0].ID, "assistant", "Original event", "Original visible output")
	if err != nil {
		t.Fatal(err)
	}
	run, err := s.GetRun(ctx, sent.Runs[0].ID)
	if err != nil || run.Output != "Original visible output" || run.UpdatedAt != update.UpdatedAt {
		t.Fatal(run, err)
	}
	events, err := s.ListRunEvents(ctx, run.ID, 0)
	if err != nil || len(events) != 1 || events[0].Text != "Original event" {
		t.Fatal(events, err)
	}
	notices, err := s.ListWorkspaceEvents(ctx, before)
	if err != nil || len(notices) != 1 || notices[0].Type != "run.activity" {
		t.Fatal(notices, err)
	}
}
