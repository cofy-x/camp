package storage

import (
	"context"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
)

func TestWorkResultReviewsFollowTheExactPublication(t *testing.T) {
	s, owner, reviewer, room, writer, _ := taskFixture(t)
	work, err := s.CreateWorkItem(owner, room.ID, orchestration.WorkItemInput{RequestID: newID(), Goal: "Prepare the handover", CompletionMode: "human"})
	if err != nil {
		t.Fatal(err)
	}
	work = decideTestWork(t, s, owner, work, orchestration.WorkItemOperation{Kind: "execute", ParticipantAgentIDs: []string{writer.ID}, FirstAgentID: writer.ID})
	task, err := s.GetTaskState(owner, work.ActiveTaskID)
	if err != nil {
		t.Fatal(err)
	}
	enableTaskInputs(t, s, task.Participants[0].RunnerID)
	const path = "handover.md"
	const secondPath = "notes.md"
	version := strings.Repeat("b", 40)
	newer := strings.Repeat("c", 40)
	get := func(actor context.Context) WorkResultReviews {
		t.Helper()
		out, err := s.ReadWorkResultReviews(actor, room.ID, work.ID, room.ResourceID, version)
		if err != nil {
			t.Fatal(err)
		}
		return out
	}
	_, err = s.ReadWorkResultReviews(owner, room.ID, work.ID, room.ResourceID, version)
	requireCode(t, err, "revision_conflict")
	finishTaskTestRound(t, s, task, version, CoordinationIntent{CallID: newID(), Kind: "finish", Summary: "Ready.", ArtifactPaths: []string{path, secondPath}})
	if err := s.ReconcileTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if out := get(owner); !out.CanRequest || !out.CurrentVersion || len(out.Reviews) != 0 {
		t.Fatal("first publication should be eligible for review", out)
	}
	_, err = s.ReadWorkResultReviews(owner, "different-room", work.ID, room.ResourceID, version)
	requireCode(t, err, "not_found")
	_, err = s.ReadWorkResultReviews(owner, room.ID, work.ID, room.ResourceID, newer)
	requireCode(t, err, "revision_conflict")
	person, _ := UserFromContext(reviewer)
	create := func(file, commit string) ReviewResult {
		t.Helper()
		return requestReview(t, reviewFixture{s, room, owner, reviewer, CreateReviewInput{RequestID: newID(), Title: file, ResourceID: room.ResourceID, Path: file, Commit: commit, ReviewerUserID: person.ID}})
	}
	f := reviewFixture{store: s, room: room, owner: owner, reviewer: reviewer}
	selected := create(path, version)
	cancelled := create(secondPath, version)
	reviewAction(t, f, owner, cancelled, "cancel", "", "")
	create("other.md", version)
	if out := get(reviewer); out.Reviews[path] != (ResultFileReview{ID: selected.Review.ID, Status: "in_review", Revision: 1, NextAction: "review"}) || len(out.Reviews) != 1 {
		t.Fatal("reviewer needs only the exact file's current decision", out)
	}
	if out := get(owner); out.Reviews[path].NextAction != "" {
		t.Fatal("the requester was offered the reviewer's action", out)
	}
	approved := reviewAction(t, f, reviewer, selected, "approve", "", "")
	if out := get(owner); out.Reviews[path].NextAction != "deliver" {
		t.Fatal("the requester should see delivery", out)
	}
	if out := get(reviewer); out.Reviews[path].NextAction != "" {
		t.Fatal("the reviewer was offered the requester's action", out)
	}
	reviewAction(t, f, owner, approved, "deliver", "", "")
	create(path, version)
	if out := get(owner); out.Reviews[path] != (ResultFileReview{ID: selected.Review.ID, Status: "delivered", Revision: 3}) {
		t.Fatal("a later pending review hid the recorded delivery", out)
	}
	create(secondPath, version)
	publishReviewVersion(t, f, newer)
	create(secondPath, newer)
	out := get(reviewer)
	if out.CanRequest || out.CurrentVersion || out.Reviews[path].Status != "delivered" || out.Reviews[secondPath].Status != "in_review" || out.Reviews[secondPath].NextAction != "" {
		t.Fatal("an older Work result acquired the newer review or action", out)
	}
}
