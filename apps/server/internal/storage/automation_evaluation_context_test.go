package storage

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
)

func TestAutomationEvaluationContextRetainsFrozenMemoryAndWorkState(t *testing.T) {
	s, actor, member, room, writer, _ := taskFixture(t)
	policy := testRoomAutomationPolicy(t, s, actor, room, writer, "observe")
	memory, err := s.SaveMemory(actor, room.ID, "", memoryNote("snapshot-memory", "The original launch note."))
	if err != nil {
		t.Fatal(err)
	}
	message := sendAutomationDiscussion(t, s, actor, room, "Original source bytes remain at the source endpoint.").Message
	w, err := s.CreateWorkItem(actor, room.ID, orchestration.WorkItemInput{RequestID: newID(), Goal: "Follow the launch", CompletionCriteria: "The launch is confirmed", PolicyID: policy.ID, EvidenceIDs: []string{"room:" + message.ID}})
	if err != nil {
		t.Fatal(err)
	}
	candidate := claimAutomation(t, s)
	if len(candidate.WorkItems) != 1 || len(candidate.Memories) != 1 {
		t.Fatal(candidate)
	}
	edit := memoryNote("replace-memory", "A later replacement must not alter the assessment.")
	edit.Revision = 1
	if _, err = s.SaveMemory(actor, "", memory.ID, edit); err != nil {
		t.Fatal(err)
	}
	w, err = s.GetWorkItem(actor, w.ID)
	if err != nil {
		t.Fatal(err)
	}
	decideTestWork(t, s, actor, w, orchestration.WorkItemOperation{Kind: "wait", Reason: "A later decision", WaitCondition: "Wait for another response"})
	view, err := s.GetAutomationEvaluation(member, candidate.ID)
	if err != nil || len(view.WorkItems) != 1 || len(view.Memories) != 1 || view.WorkItemsOmitted || view.MemoriesOmitted || view.TotalWorkItems != 1 || view.TotalMemories != 1 {
		t.Fatal(view, err)
	}
	viewWork, _ := json.Marshal(view.WorkItems)
	frozenWork, _ := json.Marshal(candidate.WorkItems)
	viewMemory, _ := json.Marshal(view.Memories)
	frozenMemory, _ := json.Marshal(candidate.Memories)
	if string(viewWork) != string(frozenWork) || string(viewMemory) != string(frozenMemory) {
		t.Fatal("view substituted mutable current state", view, candidate)
	}
	for _, kind := range []string{"work-items", "memories"} {
		page, e := s.AutomationEvaluationContext(member, candidate.ID, kind, 0, 10)
		if e != nil || len(page.Items) != 1 || page.NextAfter != 1 || page.HasMore || page.Total != 1 {
			t.Fatal(page, e)
		}
		var expected []byte
		if kind == "work-items" {
			expected, _ = json.Marshal(candidate.WorkItems[0])
		} else {
			expected, _ = json.Marshal(candidate.Memories[0])
		}
		if string(page.Items[0]) != string(expected) {
			t.Fatal("paged context changed the frozen revision", string(page.Items[0]), string(expected))
		}
		last, e := s.AutomationEvaluationContext(member, candidate.ID, kind, 1, 10)
		if e != nil || len(last.Items) != 0 || last.NextAfter != 1 || last.HasMore {
			t.Fatal(last, e)
		}
	}
	encoded, _ := json.Marshal(view)
	for _, excluded := range []string{"Original source bytes", "runtimeKey", "credentialRef", "taskFrontier", "A later replacement"} {
		if strings.Contains(string(encoded), excluded) {
			t.Fatal("audit view leaked unrelated/private/current context", excluded)
		}
	}
	assertScalar(t, s, "SELECT sum(evaluations) FROM automation_usage", 1)
	assertScalar(t, s, "SELECT count(*) FROM collaboration_tasks", 0)
	disabled := false
	if _, err = s.UpdateUserAccess(actor, actorID(member), nil, &disabled); err != nil {
		t.Fatal(err)
	}
	_, err = s.AutomationEvaluationContext(member, candidate.ID, "memories", 0, 10)
	requireCode(t, err, "unauthenticated")
}

func TestAutomationEvaluationContextPagesWholeSnapshotsWithinWireBudget(t *testing.T) {
	s, actor, member, room, writer, _ := taskFixture(t)
	testRoomAutomationPolicy(t, s, actor, room, writer, "observe")
	sendAutomationDiscussion(t, s, actor, room, "Review this request")
	candidate := claimAutomation(t, s)
	for index := range 7 {
		candidate.WorkItems = append(candidate.WorkItems, orchestration.WorkItemSummary{ID: fmt.Sprintf("work-%d", index), Revision: 3, Status: "waiting", Goal: strings.Repeat("<", 8192)})
		candidate.Memories = append(candidate.Memories, orchestration.Memory{ID: fmt.Sprintf("memory-%d", index), RoomID: room.ID, Revision: 4, State: "active", Content: strings.Repeat("<", 8192), Provenance: orchestration.MemoryProvenance{Kind: "human_note"}})
	}
	raw, _ := json.Marshal(candidate)
	if _, err := s.db.Exec("UPDATE automation_evaluations SET candidate=? WHERE id=?", string(raw), candidate.ID); err != nil {
		t.Fatal(err)
	}
	view, err := s.GetAutomationEvaluation(member, candidate.ID)
	encoded, _ := json.Marshal(view)
	if err != nil || len(encoded)+1 > automationEvaluationViewBytes || !view.WorkItemsOmitted || !view.MemoriesOmitted || view.TotalWorkItems != 7 || view.TotalMemories != 7 {
		t.Fatal(len(encoded), view, err)
	}
	for _, kind := range []string{"work-items", "memories"} {
		after, count := int64(0), 0
		for {
			page, e := s.AutomationEvaluationContext(member, candidate.ID, kind, after, 10)
			encoded, _ = json.Marshal(page)
			if e != nil || len(page.Items) == 0 || len(encoded)+1 > automationEvaluationViewBytes {
				t.Fatal(len(encoded), page, e)
			}
			for _, item := range page.Items {
				var content string
				if kind == "work-items" {
					var w orchestration.WorkItemSummary
					_ = json.Unmarshal(item, &w)
					content = w.Goal
				} else {
					var m orchestration.Memory
					_ = json.Unmarshal(item, &m)
					content = m.Content
				}
				if content != strings.Repeat("<", 8192) {
					t.Fatal("truncated a frozen record")
				}
			}
			count += len(page.Items)
			if page.NextAfter <= after {
				t.Fatal("context page did not advance", page)
			}
			after = page.NextAfter
			if !page.HasMore {
				break
			}
		}
		if count != 7 {
			t.Fatal("paged context lost evidence", kind, count)
		}
	}
	for _, input := range []struct {
		kind  string
		after int64
		limit int
	}{{"unknown", 0, 10}, {"memories", -1, 10}, {"memories", 8, 10}, {"work-items", 0, 11}} {
		_, err = s.AutomationEvaluationContext(member, candidate.ID, input.kind, input.after, input.limit)
		requireCode(t, err, "invalid_page")
	}
}
