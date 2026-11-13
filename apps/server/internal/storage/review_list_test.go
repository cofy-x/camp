package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"
)

func seedReviewList(t *testing.T, f reviewFixture, count int, file func(int) string) []Review {
	t.Helper()
	out := make([]Review, 0, count)
	for i := range count {
		input := f.input
		input.RequestID = fmt.Sprintf("history-%04d", i)
		input.Title = fmt.Sprintf("Plan review %04d", i)
		input.Path = file(i)
		created, err := f.store.CreateReview(f.owner, f.room.ID, input)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, created.Review)
	}
	return out
}

func TestReviewListSeeksLargeFileHistoryAndKeepsCreationOrder(t *testing.T) {
	f := newReviewFixture(t)
	originals := seedReviewList(t, f, 1000, func(i int) string {
		if i%4 == 0 {
			return "proposal.md"
		}
		return "context.md"
	})
	// Retained history can have tied timestamps. IDs must break those ties
	// without skipped or repeated records on a later page.
	if _, err := f.store.db.Exec("UPDATE reviews SET created_at=?", "2025-01-01T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	expected := []string{}
	for _, original := range originals {
		if original.Path == "proposal.md" {
			expected = append(expected, original.ID)
		}
	}
	slices.Sort(expected)
	slices.Reverse(expected)
	other, err := f.store.CreateRoomWithResource(f.owner, Room{Name: "Other review Room"}, Resource{ID: "other-review-files", Name: "Other review files", CurrentCommit: f.input.Commit})
	if err != nil {
		t.Fatal(err)
	}
	frontier, err := f.store.queries().WorkspaceEventCursor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	options := ReviewListOptions{ResourceID: f.room.ResourceID, Path: "proposal.md"}
	start := time.Now()
	first, err := f.store.ListReviews(f.owner, options)
	if err != nil || len(first.Reviews) != 20 || first.NextCursor == "" {
		t.Fatalf("first page: %+v %v", first, err)
	}
	seen := []string{}
	page := first
	for {
		body, err := json.Marshal(page)
		if err != nil || len(body)+1 > reviewListPageBytes || len(page.Reviews) > 20 {
			t.Fatalf("unbounded page: %d bytes, %v", len(body), err)
		}
		for _, review := range page.Reviews {
			if review.Path != "proposal.md" || review.ResourceID != f.room.ResourceID {
				t.Fatalf("file scope was applied after paging: %+v", review)
			}
			seen = append(seen, review.ID)
		}
		if page.NextCursor == "" {
			break
		}
		options.Cursor = page.NextCursor
		page, err = f.store.ListReviews(f.owner, options)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !slices.Equal(seen, expected) {
		t.Fatalf("history traversal: got %d distinct candidates, want %d", len(seen), len(expected))
	}
	for _, scope := range []ReviewListOptions{
		{},
		{RoomID: f.room.ID},
		{ResourceID: f.room.ResourceID},
		{RoomID: f.room.ID, ResourceID: f.room.ResourceID},
		{RoomID: f.room.ID, ResourceID: f.room.ResourceID, Path: "proposal.md"},
	} {
		page, err := f.store.ListReviews(f.owner, scope)
		if err != nil || len(page.Reviews) != 20 || page.NextCursor == "" {
			t.Fatal("a valid scope lost its page", scope, err)
		}
		if scope.Path != "" && page.Reviews[0].ID != first.Reviews[0].ID {
			t.Fatal("combined Room and file filters changed creation order")
		}
	}
	for _, path := range []string{"", "proposal.md"} {
		page, err := f.store.ListReviews(f.owner, ReviewListOptions{RoomID: other.ID, ResourceID: f.room.ResourceID, Path: path})
		if err != nil || len(page.Reviews) != 0 || page.NextCursor != "" {
			t.Fatal("combined filters returned another Room's rows", err)
		}
	}
	after, err := f.store.queries().WorkspaceEventCursor(ctx)
	if err != nil || after != frontier {
		t.Fatal("reading history emitted a business event", after, frontier, err)
	}
	for _, table := range []string{"reviews", "review_events", "review_requests", "messages"} {
		assertScalar(t, f.store, "SELECT count(*) FROM "+table, 1000)
	}
	assertScalar(t, f.store, "SELECT count(*) FROM runs", 0)
	t.Logf("1,000 retained reviews / 250 file reviews: complete bounded traversal in %s", time.Since(start))

	record, err := f.store.GetReview(f.owner, expected[len(expected)-1])
	if err != nil {
		t.Fatal(err)
	}
	reviewAction(t, f, f.reviewer, record, "approve", "The original is accepted.", "")
	refreshed, err := f.store.ListReviews(f.owner, ReviewListOptions{ResourceID: f.room.ResourceID, Path: "proposal.md"})
	if err != nil || refreshed.Reviews[0].ID != first.Reviews[0].ID {
		t.Fatal("a decision moved an old review ahead of the reading position", err)
	}
	added := requestReview(t, f)
	older, err := f.store.ListReviews(f.owner, ReviewListOptions{ResourceID: f.room.ResourceID, Path: "proposal.md", Cursor: first.NextCursor})
	if err != nil || older.Reviews[0].ID != expected[20] {
		t.Fatal("new review or changed state shifted the older page", err)
	}
	latest, err := f.store.ListReviews(f.owner, ReviewListOptions{ResourceID: f.room.ResourceID, Path: "proposal.md"})
	if err != nil || latest.Reviews[0].ID != added.Review.ID {
		t.Fatal("refresh did not return to the latest reviews", err)
	}
	retained, err := f.store.GetReview(f.owner, record.Review.ID)
	if err != nil || retained.Review.SubmittedCommit != f.input.Commit || len(retained.Events) != 2 {
		t.Fatal("paging changed the original or audit", err)
	}
}

func TestReviewListBoundsWholeRowsAndRevalidatesCursorAuthority(t *testing.T) {
	f := newReviewFixture(t)
	longPath := strings.Repeat(strings.Repeat("<", 250)+"/", 15) + strings.Repeat("<", 240) + ".md"
	originals := seedReviewList(t, f, 30, func(int) string { return longPath })
	options := ReviewListOptions{RoomID: f.room.ID}
	first, err := f.store.ListReviews(f.reviewer, options)
	if err != nil || len(first.Reviews) == 0 || len(first.Reviews) >= 20 || first.NextCursor == "" {
		t.Fatalf("encoded bytes did not limit the page: %d %v", len(first.Reviews), err)
	}
	seen := map[string]bool{}
	page := first
	for {
		raw, err := json.Marshal(page)
		if err != nil || len(raw)+1 > reviewListPageBytes {
			t.Fatal("encoded response exceeds its bound", len(raw), err)
		}
		for _, review := range page.Reviews {
			if review.Path != longPath || seen[review.ID] {
				t.Fatal("original was shortened or repeated", review.ID)
			}
			seen[review.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		options.Cursor = page.NextCursor
		page, err = f.store.ListReviews(f.reviewer, options)
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(seen) != len(originals) {
		t.Fatal("byte boundary skipped retained reviews", len(seen))
	}
	for _, changed := range []ReviewListOptions{
		{Cursor: first.NextCursor},
		{RoomID: f.room.ID, ResourceID: f.room.ResourceID, Cursor: first.NextCursor},
		{RoomID: f.room.ID, ResourceID: f.room.ResourceID, Path: longPath, Cursor: first.NextCursor},
		{RoomID: f.room.ID, Cursor: "invalid"},
		{RoomID: f.room.ID, Limit: 21},
		{RoomID: f.room.ID, Limit: -1},
		{Path: longPath},
		{ResourceID: f.room.ResourceID, Path: "../private"},
	} {
		_, err := f.store.ListReviews(f.reviewer, changed)
		requireCode(t, err, "invalid_page")
	}
	_, err = f.store.ListReviews(f.owner, ReviewListOptions{RoomID: f.room.ID, Cursor: first.NextCursor})
	requireCode(t, err, "invalid_page")
	_, err = f.store.ListReviews(context.Background(), ReviewListOptions{})
	requireCode(t, err, "unauthenticated")
	disabled := false
	if _, err := f.store.UpdateUserAccess(f.owner, actorID(f.reviewer), nil, &disabled); err != nil {
		t.Fatal(err)
	}
	denied, err := f.store.ListReviews(f.reviewer, ReviewListOptions{RoomID: f.room.ID, Cursor: first.NextCursor})
	requireCode(t, err, "unauthenticated")
	if len(denied.Reviews) != 0 {
		t.Fatal("revoked account retained newly read review rows")
	}
}

func TestReviewListIndexMigrationPreservesDeliveryAndReceipts(t *testing.T) {
	s := unmigratedStore(t)
	if err := s.applyMigrations(ctx, migrationSourcesThrough(t, 34)); err != nil {
		t.Fatal(err)
	}
	owner, reviewer := teamUsers(t, s)
	room, err := s.SeedRoom(owner, Resource{ID: "legacy-review-files", Name: "Legacy review files", CurrentCommit: strings.Repeat("a", 40)})
	if err != nil {
		t.Fatal(err)
	}
	f := reviewFixture{s, room, owner, reviewer, CreateReviewInput{RequestID: "original-request", Title: "Retained proposal", ResourceID: room.ResourceID, Path: "proposal.md", Commit: strings.Repeat("a", 40), ReviewerUserID: actorID(reviewer)}}
	created := requestReview(t, f)
	approved := reviewAction(t, f, reviewer, created, "approve", "Reviewed original bytes.", "")
	delivered := reviewAction(t, f, owner, approved, "deliver", "Keep this document version.", "")
	var receipt string
	if err := s.db.QueryRow("SELECT result FROM review_requests WHERE user_id=? AND request_id=?", actorID(owner), f.input.RequestID).Scan(&receipt); err != nil {
		t.Fatal(err)
	}
	messages, err := s.ListMessages(owner, room.ID, created.Review.ThreadRootID, 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.migrate(ctx); err != nil {
		t.Fatal(err)
	}
	after, err := s.GetReview(owner, created.Review.ID)
	if err != nil || !reflect.DeepEqual(after, delivered) {
		t.Fatal("index migration changed the delivered original or audit", err)
	}
	var afterReceipt string
	if err := s.db.QueryRow("SELECT result FROM review_requests WHERE user_id=? AND request_id=?", actorID(owner), f.input.RequestID).Scan(&afterReceipt); err != nil || receipt != afterReceipt {
		t.Fatal("index migration changed a frozen receipt", err)
	}
	afterMessages, err := s.ListMessages(owner, room.ID, created.Review.ThreadRootID, 0)
	if err != nil || !reflect.DeepEqual(messages, afterMessages) {
		t.Fatal("index migration changed original messages", err)
	}
	page, err := s.ListReviews(owner, ReviewListOptions{ResourceID: room.ResourceID, Path: "proposal.md"})
	if err != nil || len(page.Reviews) != 1 || page.Reviews[0].DeliveredCommit != f.input.Commit {
		t.Fatal("migrated original is not discoverable", err)
	}
	replayed, found, err := s.FindCreateReview(owner, room.ID, f.input)
	if err != nil || !found || !replayed.Replayed || replayed.Review.ID != created.Review.ID {
		t.Fatal("original request no longer recovers through its receipt", err)
	}
}
