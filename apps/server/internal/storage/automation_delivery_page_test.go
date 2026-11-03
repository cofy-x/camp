package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
)

func deliveryPageFixture(t *testing.T) (*Store, context.Context, CollaborationTask, orchestration.Policy) {
	t.Helper()
	s, actor, _, room, writer, _ := taskFixture(t)
	owner, _ := UserFromContext(actor)
	policy, err := s.SaveAutomationPolicy(actor, room.ID, "room", orchestration.PolicyInput{RequestID: newID(), Mode: "observe", Rules: "Review requests.", AllowedActorIDs: []string{owner.ID}, ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID})
	if err != nil {
		t.Fatal(err)
	}
	return s, actor, startTask(t, s, actor, room, writer), policy
}

func seedDeliveryPageEntry(t *testing.T, s *Store, task CollaborationTask, policy orchestration.Policy, id, stamp, body string) {
	t.Helper()
	delivery := orchestration.Delivery{ID: id, TaskID: task.ID, PolicyID: policy.ID, SourceID: "room", Revision: 1, Body: body, Marker: "<!-- camp-effect:" + id + " -->", Status: "pending", CreatedAt: stamp}
	raw, _ := json.Marshal(delivery)
	if _, err := s.db.Exec(`INSERT INTO automation_deliveries(id,task_id,source_id,policy_id,revision,status,value,credential_ref,created_at) VALUES(?,?,?,?,1,'pending',?,'private-reference',?)`, id, task.ID, "room", policy.ID, string(raw), stamp); err != nil {
		t.Fatal(err)
	}
}

func TestAutomationDeliveryPagesDescendWithTaskScopedCursorAndRetainBodies(t *testing.T) {
	s, actor, task, policy := deliveryPageFixture(t)
	stamp := "2026-11-03T00:00:00Z"
	for index := range 25 {
		seedDeliveryPageEntry(t, s, task, policy, fmt.Sprintf("effect-%02d", index), stamp, fmt.Sprintf("Complete body %d", index))
	}
	first, err := s.TaskAutomationDeliveriesPage(actor, task.ID, "", 0)
	if err != nil || len(first.Deliveries) != 20 || first.Deliveries[0].ID != "effect-24" || first.Deliveries[19].ID != "effect-05" || first.NextCursor == "" {
		t.Fatal(first, err)
	}
	seedDeliveryPageEntry(t, s, task, policy, "effect-25", "2026-11-03T00:01:00Z", "Arrived after the first page.")
	last, err := s.TaskAutomationDeliveriesPage(actor, task.ID, first.NextCursor, 0)
	if err != nil || len(last.Deliveries) != 5 || last.Deliveries[0].ID != "effect-04" || last.Deliveries[4].ID != "effect-00" || last.NextCursor != "" {
		t.Fatal("new arrivals displaced or duplicated older deliveries", last, err)
	}
	if last.Deliveries[0].Body != "Complete body 4" {
		t.Fatal("delivery body was truncated")
	}
	encoded, _ := json.Marshal(first)
	if strings.Contains(string(encoded), "private-reference") {
		t.Fatal("private credential reference leaked through pagination")
	}
	for _, cursor := range []string{"not+base64!", base64.RawURLEncoding.EncodeToString([]byte(`{"task":"another-task","at":"2026-11-03T00:00:00Z","id":"effect-01"}`)), base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"task":%q,"at":"invalid","id":"effect-01"}`, task.ID)))} {
		if _, err := s.TaskAutomationDeliveriesPage(actor, task.ID, cursor, 20); err == nil {
			t.Fatal("accepted malformed or cross-task cursor", cursor)
		}
	}
	for _, limit := range []int{-1, 21} {
		if _, err := s.TaskAutomationDeliveriesPage(actor, task.ID, "", limit); err == nil {
			t.Fatal("accepted invalid page limit", limit)
		}
	}
}

func TestAutomationDeliveryPageCountsEscapingCursorAndFinalWireShape(t *testing.T) {
	s, actor, task, policy := deliveryPageFixture(t)
	body := strings.Repeat("<", 40<<10)
	for index := range 3 {
		seedDeliveryPageEntry(t, s, task, policy, fmt.Sprintf("large-%d", index), "2026-11-03T00:00:00Z", body)
	}
	cursor := ""
	for index := range 3 {
		page, err := s.TaskAutomationDeliveriesPage(actor, task.ID, cursor, 20)
		if err != nil || len(page.Deliveries) != 1 || page.Deliveries[0].Body != body {
			t.Fatal("page truncated a complete body or failed to count escaping", len(page.Deliveries), err)
		}
		encoded, _ := json.Marshal(page)
		if len(encoded)+1 > automationDeliveryPageBytes {
			t.Fatal("encoded delivery page exceeds its bound", len(encoded))
		}
		cursor = page.NextCursor
		if (cursor == "") != (index == 2) {
			t.Fatal("cursor does not describe the remaining complete bodies", index)
		}
	}
	if _, err := s.db.Exec("UPDATE users SET enabled=0 WHERE id=?", actorID(actor)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.TaskAutomationDeliveriesPage(actor, task.ID, "", 20); err == nil {
		t.Fatal("disabled member read delivery history")
	}
}

func TestAutomationDeliveryPageFitsLargestBodyWithCompleteBoundedHistory(t *testing.T) {
	s, actor, task, policy := deliveryPageFixture(t)
	stamp := "2026-11-03T00:00:00.123456789Z"
	body := strings.Repeat("<", 48<<10)
	delivery := orchestration.Delivery{ID: strings.Repeat("a", 128), TaskID: task.ID, SourceID: strings.Repeat("s", 128), PolicyID: policy.ID, Repository: strings.Repeat("o", 39) + "/" + strings.Repeat("r", 100), RepositoryID: math.MaxInt64, Number: math.MaxInt64, ActorID: math.MaxInt64, Body: body, CreatedAt: stamp, Status: "unknown"}
	delivery.Marker = "<!-- camp-effect:" + delivery.ID + " -->"
	for index := range maxAutomationDeliveryAttempts {
		delivery.Attempts = append(delivery.Attempts, orchestration.DeliveryAttempt{Revision: math.MaxInt64, ActorID: math.MaxInt64, StartedAt: stamp, Outcome: "unknown", CredentialRef: strings.Repeat("c", 48)})
		commentID := int64(math.MaxInt64 - index)
		delivery.Receipts = append(delivery.Receipts, orchestration.DeliveryReceipt{CommentID: commentID, ActorID: math.MaxInt64, URL: automationCommentURL(delivery, commentID)})
	}
	stored := storedAutomationDelivery{Delivery: delivery}
	for _, attempt := range delivery.Attempts {
		stored.AttemptCredentials = append(stored.AttemptCredentials, attempt.CredentialRef)
	}
	raw, _ := json.Marshal(stored)
	if _, err := s.db.Exec(`INSERT INTO automation_deliveries(id,task_id,source_id,policy_id,revision,status,value,credential_ref,created_at) VALUES(?,?,?,?,1,'unknown',?,'private-reference',?)`, delivery.ID, task.ID, delivery.SourceID, policy.ID, string(raw), stamp); err != nil {
		t.Fatal(err)
	}
	page, err := s.TaskAutomationDeliveriesPage(actor, task.ID, "", 20)
	if err != nil || len(page.Deliveries) != 1 || page.Deliveries[0].Body != body || len(page.Deliveries[0].Attempts) != maxAutomationDeliveryAttempts || len(page.Deliveries[0].Receipts) != maxAutomationDeliveryAttempts {
		t.Fatal("a legal comment with complete bounded evidence became unreadable", len(page.Deliveries), err)
	}
	encoded, _ := json.Marshal(page)
	if len(encoded)+1 > automationDeliveryPageBytes || strings.Contains(string(encoded), "attemptCredentials") || strings.Contains(string(encoded), strings.Repeat("c", 48)) {
		t.Fatal("full evidence escaped its byte or privacy bounds", len(encoded))
	}
}
