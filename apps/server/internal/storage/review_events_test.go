package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"
)

func growReviewAudit(t *testing.T, f reviewFixture, current ReviewResult, cycles int, comment string) ReviewResult {
	t.Helper()
	for range cycles {
		var err error
		current, err = f.store.ActOnReview(f.reviewer, current.Review.ID, ReviewActionInput{RequestID: fmt.Sprintf("changes-%d", current.Review.Revision), Revision: current.Review.Revision, Action: "request_changes", Comment: comment}, ReviewSummary)
		if err != nil {
			t.Fatal(err)
		}
		current, err = f.store.ActOnReview(f.owner, current.Review.ID, ReviewActionInput{RequestID: fmt.Sprintf("resubmit-%d", current.Review.Revision), Revision: current.Review.Revision, Action: "resubmit", Commit: f.input.Commit}, ReviewSummary)
		if err != nil {
			t.Fatal(err)
		}
	}
	return current
}

func TestReviewSummaryKeepsLargeAuditAndFrozenReceiptsIndependent(t *testing.T) {
	f := newReviewFixture(t)
	original := requestReview(t, f)
	var receipt string
	if err := f.store.db.QueryRow("SELECT result FROM review_requests WHERE request_id=?", f.input.RequestID).Scan(&receipt); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	current := growReviewAudit(t, f, original, 500, "Clarify the owner.")
	summary, err := f.store.GetReview(f.owner, current.Review.ID, ReviewSummary)
	if err != nil || summary.Review.Revision != 1001 || len(summary.Events) != 0 || summary.Feedback != nil {
		t.Fatal("current read included audit", err)
	}
	raw, _ := json.Marshal(summary.Review)
	if len(raw) > 2048 {
		t.Fatal("current state grew with audit", len(raw))
	}
	first, err := f.store.ReviewEvents(f.owner, current.Review.ID, ReviewEventOptions{})
	if err != nil || len(first.Events) != 20 || first.Events[0].Revision != 1001 || first.Cursor == "" || first.NextCursor == "" {
		t.Fatal("bounded newest history missing", err)
	}
	current = growReviewAudit(t, f, current, 1, "New information outside the frozen range.")
	same, err := f.store.ReviewEvents(f.owner, current.Review.ID, ReviewEventOptions{Cursor: first.Cursor})
	if err != nil || !reflect.DeepEqual(first, same) {
		t.Fatal("a new decision moved the first historical page", err)
	}
	options := ReviewEventOptions{Through: first.Through}
	page, seen := first, map[int64]bool{}
	for {
		encoded, _ := json.Marshal(page)
		if len(encoded)+1 > reviewEventPageBytes || len(page.Events) > 20 {
			t.Fatal("unbounded history")
		}
		for _, event := range page.Events {
			if seen[event.Revision] || event.Revision > 1001 || event.Commit != f.input.Commit {
				t.Fatal("audit original changed or repeated")
			}
			seen[event.Revision] = true
		}
		if page.NextCursor == "" {
			break
		}
		options.Cursor = page.NextCursor
		page, err = f.store.ReviewEvents(f.owner, current.Review.ID, options)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != 1001 {
		t.Fatal("lost audit facts", len(seen))
	}
	replay, found, err := f.store.FindCreateReview(f.owner, f.room.ID, f.input, ReviewSummary)
	if err != nil || !found || !replay.Replayed || replay.Review.Revision != 1 || len(replay.Events) != 0 {
		t.Fatal("small original receipt lost", err)
	}
	legacy, found, err := f.store.FindCreateReview(f.owner, f.room.ID, f.input)
	if err != nil || !found || len(legacy.Events) != 1 || !reflect.DeepEqual(legacy.Review, original.Review) {
		t.Fatal("legacy receipt expanded beyond its original revision", err)
	}
	var after string
	if err = f.store.db.QueryRow("SELECT result FROM review_requests WHERE request_id=?", f.input.RequestID).Scan(&after); err != nil || after != receipt {
		t.Fatal("rewrote original receipt", err)
	}
	var plan string
	rows, err := f.store.db.Query("EXPLAIN QUERY PLAN SELECT * FROM review_events WHERE review_id=? AND revision<=? AND revision<=? ORDER BY revision DESC LIMIT 21", current.Review.ID, first.Through, first.Through)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var a, b, c int
		var detail string
		if err = rows.Scan(&a, &b, &c, &detail); err != nil {
			t.Fatal(err)
		}
		plan += detail
	}
	if !strings.Contains(plan, "SEARCH review_events USING INDEX") || strings.Contains(plan, "TEMP B-TREE") {
		t.Fatal("history lost its indexed seek", plan)
	}
	t.Logf("1,003 retained facts: bounded traversal, constant current read and original receipt in %s", time.Since(started))
}

func TestReviewEventsBoundsWholeCommentsAndRechecksAuthority(t *testing.T) {
	f := newReviewFixture(t)
	original := requestReview(t, f)
	comment := strings.Repeat("<", 8192)
	current := growReviewAudit(t, f, original, 20, comment)
	input := ReviewActionInput{RequestID: "last-feedback", Revision: current.Review.Revision, Action: "request_changes", Comment: comment}
	changed, err := f.store.ActOnReview(f.reviewer, current.Review.ID, input, ReviewSummary)
	if err != nil || changed.Feedback == nil || changed.Feedback.Comment != comment || len(changed.Events) != 0 {
		t.Fatal("current feedback was truncated or required audit", err)
	}
	page, err := f.store.ReviewEvents(f.reviewer, current.Review.ID, ReviewEventOptions{})
	encoded, _ := json.Marshal(page)
	if err != nil || len(page.Events) >= 20 || len(encoded)+1 > reviewEventPageBytes || page.NextCursor == "" {
		t.Fatal("encoded-byte limit not enforced", len(page.Events), len(encoded), err)
	}
	for _, fact := range page.Events {
		if fact.Action == "request_changes" && fact.Comment != comment {
			t.Fatal("truncated original comment")
		}
	}
	_, err = f.store.ReviewEvents(f.owner, current.Review.ID, ReviewEventOptions{Cursor: page.Cursor})
	requireCode(t, err, "invalid_page")
	f.input.RequestID = "other-review"
	other := requestReview(t, f)
	_, err = f.store.ReviewEvents(f.reviewer, other.Review.ID, ReviewEventOptions{Cursor: page.Cursor})
	requireCode(t, err, "invalid_page")
	for _, options := range []ReviewEventOptions{{Through: current.Review.Revision + 100}, {Through: 1, Cursor: page.Cursor}, {Cursor: "invalid"}, {Limit: 21}, {Through: -1}} {
		_, err = f.store.ReviewEvents(f.reviewer, current.Review.ID, options)
		requireCode(t, err, "invalid_page")
	}
	_, err = f.store.ReviewEvents(context.Background(), current.Review.ID, ReviewEventOptions{})
	requireCode(t, err, "unauthenticated")
	if _, err = f.store.UpdateUserAccess(f.owner, actorID(f.reviewer), nil, ptr(false)); err != nil {
		t.Fatal(err)
	}
	_, err = f.store.ReviewEvents(f.reviewer, current.Review.ID, ReviewEventOptions{Cursor: page.Cursor})
	requireCode(t, err, "unauthenticated")
	_, err = f.store.GetReview(f.reviewer, current.Review.ID, ReviewSummary)
	requireCode(t, err, "unauthenticated")
	_, _, err = f.store.FindReviewAction(f.reviewer, current.Review.ID, input, ReviewSummary)
	requireCode(t, err, "unauthenticated")
}
