package storage

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
)

func TestAutomationActivityRecentFrozenPagesAndSourceFilteringBeforeLimit(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	source := testGitHubSource(t, s, actor, room)
	older := receiveTestGitHub(t, s, source, testGitHubPayload("Older title", "Original body stays behind its source link."))
	newer := receiveTestGitHub(t, s, source, testGitHubPayload("Newer title", "Another original body."))
	task := startTask(t, s, actor, room, writer)
	for index := range 30 {
		if _, err := s.SendMessage(actor, room.ID, SendMessageInput{RequestID: newID(), Content: fmt.Sprintf("Recent Room discussion %d", index), PostOnly: true}, roomBudget); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.AutomationActivity(actor, room.ID, AutomationEventQuery{Direction: "desc"})
	if err != nil || len(first.Items) != 20 || !first.HasMore || first.NextBefore == nil || first.NextAfter != nil {
		t.Fatal(first, err)
	}
	for index, item := range first.Items {
		if index > 0 && item.Seq >= first.Items[index-1].Seq {
			t.Fatal("recent events are not descending", first.Items)
		}
	}
	filtered, err := s.AutomationActivity(actor, room.ID, AutomationEventQuery{Direction: "desc", SourceID: source.ID, Limit: 1})
	if err != nil || len(filtered.Items) != 1 || filtered.Items[0].ExternalEventID != newer.ID || filtered.Items[0].Preview != "Newer title" || filtered.Items[0].Content != "" || !filtered.HasMore || filtered.NextBefore == nil {
		t.Fatal("source filter ran after the global row limit", filtered, err)
	}
	later := receiveTestGitHub(t, s, source, testGitHubPayload("Arrived later", "Outside the frozen page."))
	last, err := s.AutomationActivity(actor, room.ID, AutomationEventQuery{Direction: "desc", SourceID: source.ID, Limit: 1, Before: filtered.NextBefore, Through: &filtered.Through})
	if err != nil || len(last.Items) != 1 || last.Items[0].ExternalEventID != older.ID || last.HasMore || last.NextBefore != nil {
		t.Fatal("new arrivals displaced the frozen source traversal", last, err)
	}
	refresh, err := s.AutomationActivity(actor, room.ID, AutomationEventQuery{Direction: "desc", SourceID: source.ID, Limit: 1})
	if err != nil || refresh.Items[0].ExternalEventID != later.ID {
		t.Fatal(refresh, err)
	}
	seen := map[int64]bool{}
	page := first
	for {
		for _, item := range page.Items {
			if seen[item.Seq] || item.Seq > first.Through {
				t.Fatal("duplicate or new event crossed the frozen frontier", item.Seq)
			}
			seen[item.Seq] = true
		}
		if !page.HasMore {
			break
		}
		page, err = s.AutomationActivity(actor, room.ID, AutomationEventQuery{Direction: "desc", Before: page.NextBefore, Through: &first.Through})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 33 {
		t.Fatal("frozen pages lost an event", len(seen))
	}
	legacy, err := s.AutomationEvents(actor, room.ID, 0, &first.Through, 1)
	if err != nil || len(legacy.Items) != 1 || legacy.Items[0].ExternalEventID != older.ID || legacy.NextAfter == nil || legacy.NextBefore != nil {
		t.Fatal("legacy ascending traversal changed", legacy, err)
	}
	ascendingSource, err := s.AutomationActivity(actor, room.ID, AutomationEventQuery{SourceID: source.ID, Limit: 1, Through: &first.Through})
	if err != nil || ascendingSource.Items[0].ExternalEventID != older.ID || ascendingSource.NextAfter == nil {
		t.Fatal(ascendingSource, err)
	}
	taskEvents, err := s.AutomationActivity(actor, room.ID, AutomationEventQuery{Direction: "desc", SourceID: "task"})
	if err != nil || len(taskEvents.Items) != 1 || taskEvents.Items[0].TaskID != task.ID || taskEvents.Items[0].Preview != task.Goal {
		t.Fatal("task source preview lacks its original visible reference", taskEvents, err)
	}
	if _, err = s.UpdateGitHubSource(actor, source.ID, UpdateGitHubSourceInput{RequestID: newID(), Revision: source.Revision, Enabled: new(false)}, ""); err != nil {
		t.Fatal(err)
	}
	if retained, err := s.AutomationActivity(actor, room.ID, AutomationEventQuery{Direction: "desc", SourceID: source.ID}); err != nil || len(retained.Items) != 3 {
		t.Fatal("disabled source history became unreadable", retained, err)
	}
}

func TestAutomationActivityProjectsBoundedLiveAssessmentWithoutChangingDecision(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	testRoomAutomationPolicy(t, s, actor, room, writer, "observe")
	body := strings.Repeat("建议< ", 800)
	sendAutomationDiscussion(t, s, actor, room, body)
	candidate := claimAutomation(t, s)
	reason := strings.Repeat("原文reason< ", 200)
	if err := s.CompleteAutomationEvaluation(ctx, candidate.ID, orchestration.Assessment{Outcome: "accept", Goal: "A suggested task remains advisory.", Reason: reason}); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		page, err := s.AutomationActivity(actor, room.ID, AutomationEventQuery{Direction: "desc", SourceID: "room"})
		if err != nil || len(page.Items) != 1 {
			t.Fatal(page, err)
		}
		item := page.Items[0]
		if item.Status != "observed" || item.Reason != reason || item.AssessmentOutcome != "accept" || len(item.Preview) > 320 || len(item.AssessmentReason) > 512 || !utf8.ValidString(item.Preview) || !utf8.ValidString(item.AssessmentReason) || !strings.HasSuffix(item.Preview, "...") || item.Content != "" {
			t.Fatal("summary changed authority, source bytes or exceeded its preview bound", item)
		}
	}
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
	assertScalar(t, s, "SELECT count(*) FROM automation_evaluations", 1)
	assertScalar(t, s, "SELECT sum(evaluations) FROM automation_usage", 1)
}

func TestAutomationActivityEncodedByteLimitAndCursorValidation(t *testing.T) {
	s, actor, _, room, writer, _ := taskFixture(t)
	testRoomAutomationPolicy(t, s, actor, room, writer, "observe")
	for index := range 20 {
		sendAutomationDiscussion(t, s, actor, room, fmt.Sprintf("Original %d", index))
	}
	candidate := claimAutomation(t, s)
	if err := s.CompleteAutomationEvaluation(ctx, candidate.ID, orchestration.Assessment{Outcome: "ignore", Reason: strings.Repeat("<", 4096)}); err != nil {
		t.Fatal(err)
	}
	page, err := s.AutomationActivity(actor, room.ID, AutomationEventQuery{Direction: "desc"})
	if err != nil || len(page.Items) >= 20 || len(page.Items) == 0 || !page.HasMore {
		t.Fatal("escaped wire bytes were not counted", len(page.Items), err)
	}
	through, count := page.Through, 0
	for {
		encoded, _ := json.Marshal(page)
		if len(encoded)+1 > 64<<10 {
			t.Fatal("activity page exceeded its byte bound", len(encoded))
		}
		count += len(page.Items)
		if !page.HasMore {
			break
		}
		page, err = s.AutomationActivity(actor, room.ID, AutomationEventQuery{Direction: "desc", Through: &through, Before: page.NextBefore})
		if err != nil {
			t.Fatal(err)
		}
	}
	if count != 20 {
		t.Fatal("byte-limited traversal lost events", count)
	}
	for _, query := range []AutomationEventQuery{{Direction: "other"}, {Direction: "desc", After: 1}, {Before: new(int64(1))}, {Direction: "desc", Before: new(int64(0))}, {Direction: "desc", Before: new(through + 1)}, {SourceID: "missing-source"}, {Limit: 21}, {After: -1}} {
		if _, err := s.AutomationActivity(actor, room.ID, query); err == nil {
			t.Fatal("invalid activity cursor or filter accepted", query)
		}
	}
	otherRoom, err := s.CreateRoomWithResource(actor, Room{Name: "Other Room"}, Resource{ID: newID(), Name: "Other files", CurrentCommit: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	foreign := testGitHubSource(t, s, actor, otherRoom)
	if _, err = s.AutomationActivity(actor, room.ID, AutomationEventQuery{SourceID: foreign.ID}); err == nil {
		t.Fatal("another Room's source was accepted as a local filter")
	}
}
