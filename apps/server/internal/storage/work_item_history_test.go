package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func historyWorkItem(t *testing.T, s *Store, actor context.Context, room Room) orchestration.WorkItem {
	t.Helper()
	w, err := s.CreateWorkItem(actor, room.ID, orchestration.WorkItemInput{RequestID: newID(), Goal: "Follow the request", CompletionCriteria: "The requester confirms the result"})
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestWorkItemHistoryRetainsAllEvidenceWithStableBoundCursor(t *testing.T) {
	s, actor, member, room, _, _ := taskFixture(t)
	w := historyWorkItem(t, s, actor, room)
	for i := range 25 {
		m := sendAutomationDiscussion(t, s, actor, room, fmt.Sprintf("Evidence %d", i)).Message
		if _, err := s.queries().InsertWorkEvidence(ctx, sqlgen.InsertWorkEvidenceParams{WorkItemID: w.ID, EventID: "room:" + m.ID, Kind: "room.message", CreatedAt: now()}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.WorkItemHistory(member, w.ID, "evidence", "", 7)
	if err != nil || len(first.Items) != 7 || first.NextCursor == "" {
		t.Fatal(first, err)
	}
	late := sendAutomationDiscussion(t, s, actor, room, "Late evidence").Message
	if _, err = s.queries().InsertWorkEvidence(ctx, sqlgen.InsertWorkEvidenceParams{WorkItemID: w.ID, EventID: "room:" + late.ID, Kind: "room.message", CreatedAt: now()}); err != nil {
		t.Fatal(err)
	}
	items := append([]json.RawMessage{}, first.Items...)
	cursor := first.NextCursor
	for cursor != "" {
		page, e := s.WorkItemHistory(member, w.ID, "evidence", cursor, 7)
		if e != nil || len(page.Items) == 0 {
			t.Fatal(page, e)
		}
		items = append(items, page.Items...)
		cursor = page.NextCursor
	}
	if len(items) != 25 {
		t.Fatal("history lost evidence or crossed frontier", len(items))
	}
	seen := map[string]bool{}
	for _, raw := range items {
		var evidence orchestration.WorkEvidence
		if err = json.Unmarshal(raw, &evidence); err != nil {
			t.Fatal(err)
		}
		if seen[evidence.EventID] || evidence.EventID == "room:"+late.ID {
			t.Fatal("duplicate or late evidence", evidence)
		}
		seen[evidence.EventID] = true
	}
	other := historyWorkItem(t, s, actor, room)
	_, err = s.WorkItemHistory(member, other.ID, "evidence", first.NextCursor, 7)
	requireCode(t, err, "invalid_page")
	_, err = s.WorkItemHistory(member, w.ID, "decisions", first.NextCursor, 7)
	requireCode(t, err, "invalid_page")
	_, err = s.WorkItemHistory(member, w.ID, "evidence", "invalid", 7)
	requireCode(t, err, "invalid_page")
	user, _ := UserFromContext(member)
	disabled := false
	if _, err = s.UpdateUserAccess(actor, user.ID, nil, &disabled); err != nil {
		t.Fatal(err)
	}
	_, err = s.WorkItemHistory(member, w.ID, "evidence", first.NextCursor, 7)
	requireCode(t, err, "unauthenticated")
}

func TestWorkItemHistoryWholeRecordsAndMutableStatusProjection(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	w := historyWorkItem(t, s, actor, room)
	body := strings.Repeat("Original action body. ", 1100)
	for range 20 {
		if err := s.queries().InsertWorkAction(ctx, sqlgen.InsertWorkActionParams{ID: newID(), WorkItemID: w.ID, Kind: "inbox", Status: "delivered", Body: body, CreatedAt: now()}); err != nil {
			t.Fatal(err)
		}
	}
	count, cursor := 0, ""
	for {
		page, err := s.WorkItemHistory(actor, w.ID, "actions", cursor, 20)
		if err != nil || len(page.Items) == 0 {
			t.Fatal(page, err)
		}
		encoded, _ := json.Marshal(page)
		if len(encoded)+1 > workHistoryPageBytes {
			t.Fatal("unbounded page", len(encoded))
		}
		for _, raw := range page.Items {
			var action orchestration.WorkAction
			if err = json.Unmarshal(raw, &action); err != nil || action.Body != body {
				t.Fatal("truncated action evidence", action, err)
			}
		}
		count += len(page.Items)
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if count != 20 {
		t.Fatal(count)
	}
	for _, id := range []string{"first-timer", "second-timer"} {
		if err := s.queries().InsertWorkTimer(ctx, sqlgen.InsertWorkTimerParams{ID: id, WorkItemID: w.ID, DueAt: time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano), Condition: "Wait for reply", CreatedAt: now()}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.WorkItemHistory(actor, w.ID, "timers", "", 1)
	if err != nil || first.NextCursor == "" {
		t.Fatal(first, err)
	}
	if err = s.queries().FireWorkTimer(ctx, sqlgen.FireWorkTimerParams{ID: "second-timer", EventID: "fired-event"}); err != nil {
		t.Fatal(err)
	}
	next, err := s.WorkItemHistory(actor, w.ID, "timers", first.NextCursor, 1)
	if err != nil || len(next.Items) != 1 || !strings.Contains(string(next.Items[0]), `"status":"fired"`) {
		t.Fatal(next, err)
	}
	task := startTask(t, s, actor, room, writer)
	if err = s.queries().InsertWorkTask(ctx, sqlgen.InsertWorkTaskParams{WorkItemID: w.ID, TaskID: task.ID, ReservedRequests: 24, CreatedAt: now()}); err != nil {
		t.Fatal(err)
	}
	tasks, err := s.WorkItemHistory(actor, w.ID, "tasks", "", 20)
	if err != nil || len(tasks.Items) != 1 || !strings.Contains(string(tasks.Items[0]), task.ID) {
		t.Fatal(tasks, err)
	}
	if err = s.transaction(ctx, func(tx *sql.Tx) error {
		return appendWorkDecision(ctx, sqlgen.New(tx), w, orchestration.WorkDecision{Kind: "ignore", Reason: "Recorded for audit", ActorKind: "user", ActorID: actorID(actor)})
	}); err != nil {
		t.Fatal(err)
	}
	decisions, err := s.WorkItemHistory(actor, w.ID, "decisions", "", 20)
	if err != nil || len(decisions.Items) != 2 {
		t.Fatal(decisions, err)
	}
	for _, raw := range decisions.Items {
		var decision orchestration.WorkDecision
		if err = json.Unmarshal(raw, &decision); err != nil || decision.Seq <= 0 {
			t.Fatal(decision, err)
		}
	}
}

func TestWorkTimelineRetainsRecentFirstPagesAndOriginals(t *testing.T) {
	s, actor, member, room, _, _ := taskFixture(t)
	w := historyWorkItem(t, s, actor, room)
	reason := strings.Repeat("Recorded progress. ", 1700)
	appendProgress := func(text string) {
		t.Helper()
		if err := s.transaction(ctx, func(tx *sql.Tx) error {
			return appendWorkDecision(ctx, sqlgen.New(tx), w, orchestration.WorkDecision{Kind: "wait", Reason: text, ActorKind: "user", ActorID: actorID(actor)})
		}); err != nil {
			t.Fatal(err)
		}
	}
	for range 30 {
		appendProgress(reason)
	}
	first, err := s.WorkItemHistory(member, w.ID, "timeline", "", 20)
	if err != nil || len(first.Items) == 0 || len(first.Items) >= 20 || first.NextCursor == "" {
		t.Fatal("byte bound did not retain a navigable page", first, err)
	}
	appendProgress("New progress after the retained frontier")
	cursor, items := first.NextCursor, append([]json.RawMessage{}, first.Items...)
	for cursor != "" {
		page, err := s.WorkItemHistory(member, w.ID, "timeline", cursor, 20)
		if err != nil || len(page.Items) == 0 {
			t.Fatal(page, err)
		}
		encoded, _ := json.Marshal(page)
		if len(encoded)+1 > workHistoryPageBytes {
			t.Fatal("unbounded timeline", len(encoded))
		}
		items = append(items, page.Items...)
		cursor = page.NextCursor
	}
	if len(items) != 31 {
		t.Fatal("timeline lost or crossed its original frontier", len(items))
	}
	previous := int64(1 << 62)
	for index, raw := range items {
		var entry struct {
			Seq     int64
			Content string
		}
		if err := json.Unmarshal(raw, &entry); err != nil || entry.Seq >= previous {
			t.Fatal("reordered or duplicate progress", entry, err)
		}
		if index < 30 && entry.Content != reason {
			t.Fatal("truncated authored progress")
		}
		previous = entry.Seq
	}
	latest, err := s.WorkItemHistory(member, w.ID, "timeline", "", 1)
	if err != nil || !strings.Contains(string(latest.Items[0]), "New progress after") {
		t.Fatal(latest, err)
	}
	_, err = s.WorkItemHistory(member, w.ID, "decisions", first.NextCursor, 20)
	requireCode(t, err, "invalid_page")
	// The display projection does not replace or rewrite the original audit.
	originals, err := s.WorkItemHistory(member, w.ID, "decisions", "", 20)
	if err != nil || !strings.Contains(string(originals.Items[1]), reason) {
		t.Fatal("original decisions changed", err)
	}
}
