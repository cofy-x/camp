package storage

import (
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestRoomRunTimelineProjectsBoundedMetadataAndPagesCompleteHistory(t *testing.T) {
	s, room, _ := roomFixture(t)
	ids := []string{}
	for index := 0; index < 23; index++ {
		sent := sendRoom(t, s, room, fmt.Sprintf("timeline-%d", index))
		ids = append(ids, sent.Runs[0].ID)
	}
	run, err := s.GetRun(ctx, ids[0])
	if err != nil {
		t.Fatal(err)
	}
	run.Output = "PRIVATE_RAW_OUTPUT"
	run.SessionMode, run.SessionGeneration = "continued", 7
	run.AgentSnapshot.Instructions = "PRIVATE_INSTRUCTIONS"
	run.Checks = json.RawMessage(`[{"name":"unit","status":"passed","exitCode":0,"output":"PRIVATE_CHECK_OUTPUT"}]`)
	run.Environment = json.RawMessage(`{"kind":"command","checks":[{"name":"unit","arguments":["PRIVATE_COMMAND"]}]}`)
	run.TestExitCode = new(int)
	for index := 0; index < 25; index++ {
		run.Files = append(run.Files, RunFile{Path: fmt.Sprintf("src/file-%d.txt", index), Status: "added", Before: "PRIVATE_BEFORE", After: "PRIVATE_AFTER"})
	}
	if err = saveRun(ctx, s.queries(), run); err != nil {
		t.Fatal(err)
	}
	first, err := s.RoomRunTimeline(ctx, room.ID, "", 3)
	if err != nil || len(first.Items) != 3 || first.NextCursor == "" {
		t.Fatal(first, err)
	}
	body, _ := json.Marshal(first)
	for _, secret := range []string{"PRIVATE_RAW_OUTPUT", "PRIVATE_INSTRUCTIONS", "PRIVATE_CHECK_OUTPUT", "PRIVATE_COMMAND", "PRIVATE_BEFORE", "PRIVATE_AFTER"} {
		if strings.Contains(string(body), secret) {
			t.Fatal("execution content escaped summary", secret)
		}
	}
	item := first.Items[0]
	summary, err := s.GetRunSummary(ctx, run.ID)
	if err != nil || !reflect.DeepEqual(summary, item) || summary.SessionMode != "continued" || summary.SessionGeneration != 7 {
		t.Fatal("single summary diverged from the timeline", summary, err)
	}
	if item.FileCount != 25 || len(item.Files) != 20 || !item.FilesLimited || item.Environment == nil || len(item.Environment.Checks) != 1 || len(item.Checks) != 1 || item.Checks[0].Status != "passed" {
		t.Fatal("facts lost", item)
	}
	sent := sendRoom(t, s, room, "after-frozen-frontier")
	seen := map[string]bool{}
	for _, item := range first.Items {
		seen[item.ID] = true
	}
	cursor := first.NextCursor
	for cursor != "" {
		page, err := s.RoomRunTimeline(ctx, room.ID, cursor, 3)
		if err != nil {
			t.Fatal(err)
		}
		encoded, _ := json.Marshal(page)
		if len(encoded)+1 > RoomRunTimelineBytes {
			t.Fatal("page exceeded bound")
		}
		for _, item := range page.Items {
			if seen[item.ID] {
				t.Fatal("duplicate row")
			}
			seen[item.ID] = true
		}
		cursor = page.NextCursor
	}
	if len(seen) != 23 || seen[sent.Runs[0].ID] {
		t.Fatal("frozen traversal incomplete or included later work", len(seen))
	}
	other, err := s.CreateRoom(ctx, Room{Name: "Other", ResourceID: room.ResourceID})
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		room, cursor string
		limit        int
	}{{room.ID, "bad", 3}, {other.ID, first.NextCursor, 3}, {room.ID, "", 21}} {
		_, err := s.RoomRunTimeline(ctx, test.room, test.cursor, test.limit)
		requireCode(t, err, "invalid_page")
	}
	full, err := s.GetRun(ctx, run.ID)
	if err != nil || full.Output != run.Output || len(full.Files) != 25 {
		t.Fatal("summary modified full original", err)
	}
}

func TestRunSummaryKeepsHeavyOriginalsLazyAndEventsUseOnlyIdentity(t *testing.T) {
	var reads []string
	s := observedReadStore(t, func(query string) { reads = append(reads, query) })
	room, err := s.SeedRoom(ctx, Resource{ID: "summary-files", Name: "Files", CurrentCommit: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	sent := sendRoom(t, s, room, "large-summary")
	run, err := s.GetRun(ctx, sent.Runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	run.Output = strings.Repeat("PRIVATE_OUTPUT", 18000)
	run.AgentSnapshot.Instructions = strings.Repeat("PRIVATE_INSTRUCTIONS", 4000)
	run.Files = []RunFile{{Path: "app.txt", Status: "added", After: strings.Repeat("PRIVATE_FILE", 50000)}}
	if err = saveRun(ctx, s.queries(), run); err != nil {
		t.Fatal(err)
	}
	before, _ := s.queries().GetRun(ctx, run.ID)
	reads = nil
	summary, err := s.GetRunSummary(ctx, run.ID)
	if err != nil || summary.FileCount != 1 || summary.Files[0].Path != "app.txt" {
		t.Fatal(summary, err)
	}
	if _, err = s.ListRunEvents(ctx, run.ID, 0); err != nil {
		t.Fatal(err)
	}
	for _, query := range reads {
		if strings.Contains(query, "-- name: GetRun :one") {
			t.Fatal("display read materialized the original Run")
		}
	}
	raw, _ := json.Marshal(summary)
	if len(raw) > 2048 || strings.Contains(string(raw), "PRIVATE_") {
		t.Fatal("heavy original escaped summary", len(raw))
	}
	after, _ := s.queries().GetRun(ctx, run.ID)
	if before != after {
		t.Fatal("summary modified the original")
	}
	_, err = s.GetRunSummary(ctx, "missing")
	requireCode(t, err, "not_found")
	_, err = s.ListRunEvents(ctx, "missing", 0)
	requireCode(t, err, "not_found")
}
func TestRoomRunTimelineBoundsQueueReasonsWithoutSkippingRows(t *testing.T) {
	s, room, _ := roomFixture(t)
	sendRoom(t, s, room, "first-queued")
	sendRoom(t, s, room, "second-queued")
	page, err := s.RoomRunTimeline(ctx, room.ID, "", 2)
	if err != nil || len(page.Items) != 2 || page.NextCursor != "" {
		t.Fatal("expected both queued runs on one page", err)
	}
	secondID := page.Items[1].ID
	// Each queue reason fits on its own, but together they force a page break.
	for index := range page.Items {
		page.Items[index].WaitingReason = strings.Repeat("w", RoomRunTimelineBytes/2)
	}
	if err = page.Bound(); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(page)
	if err != nil || len(encoded)+1 > RoomRunTimelineBytes || len(page.Items) != 1 || page.NextCursor == "" {
		t.Fatal("queue reasons exceeded the page bound", len(page.Items), err)
	}
	next, err := s.RoomRunTimeline(ctx, room.ID, page.NextCursor, 2)
	if err != nil || len(next.Items) != 1 || next.Items[0].ID != secondID || next.NextCursor != "" {
		t.Fatal("the cursor skipped the second queued run", err)
	}
}

func TestRoomRunTimelineMalformedChecksAndOversizedPreviewStaySafe(t *testing.T) {
	s, room, _ := roomFixture(t)
	sent := sendRoom(t, s, room, "malformed-metadata")
	run, err := s.GetRun(ctx, sent.Runs[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	run.Checks = json.RawMessage(`["opaque",{"name":3,"status":"passed","exitCode":0}]`)
	run.Environment = json.RawMessage(`{"checks":["opaque",{"name":3}]}`)
	run.Files = []RunFile{{Path: strings.Repeat("p", RoomRunTimelineBytes+1), Status: "added", After: "retained"}}
	if err = saveRun(ctx, s.queries(), run); err != nil {
		t.Fatal(err)
	}
	page, err := s.RoomRunTimeline(ctx, room.ID, "", 20)
	if err != nil || len(page.Items) != 1 {
		t.Fatal("legacy metadata blocked timeline", err)
	}
	item := page.Items[0]
	if len(item.Checks) != 2 || item.Checks[0].Status != "incomplete" || item.Checks[1].Status != "incomplete" || item.FileCount != 1 || len(item.Files) != 0 || !item.FilesLimited {
		t.Fatal("malformed facts became a success or a truncated path", item)
	}
	data, _ := json.Marshal(page)
	if len(data)+1 > RoomRunTimelineBytes {
		t.Fatal("oversized file preview escaped bound")
	}
	summary, err := s.GetRunSummary(ctx, run.ID)
	if err != nil || !reflect.DeepEqual(summary, item) {
		t.Fatal("single summary lost the timeline's malformed-check or file-preview bounds", summary, err)
	}
}
