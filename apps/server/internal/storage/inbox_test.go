package storage

import (
	"context"
	"database/sql"
	"fmt"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func TestInboxUnreadCountAndEarlierPagesIncludeNoticesBeyondOldLimit(t *testing.T) {
	s, room, _ := roomFixture(t)
	owner, member := teamUsers(t, s)
	const createdAt = "2026-11-25T08:00:00Z"
	// Give all rows the same time so advancing by time alone would skip items.
	err := s.transaction(owner, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		for index := range 201 {
			id := fmt.Sprintf("notice-%03d", index)
			message := Message{ID: id, RoomID: room.ID, Seq: int64(index + 1), AuthorKind: "user", Content: id, CreatedAt: createdAt, UpdatedAt: createdAt}
			if err := insertMessage(owner, q, message, ""); err != nil {
				return err
			}
			if err := q.CreateInboxItem(owner, sqlgen.CreateInboxItemParams{
				ID: id, UserID: actorID(member), Kind: "mention", RoomID: room.ID, MessageID: message.ID,
				CreatedAt: createdAt,
			}); err != nil {
				return err
			}
		}
		_, err := tx.Exec("UPDATE inbox SET read_at=? WHERE id<>'notice-000'", createdAt)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if count, err := s.CountUnreadInbox(member); err != nil || count != 1 {
		t.Fatal("oldest unread item missing from count", count, err)
	}
	if count, err := s.CountUnreadInbox(owner); err != nil || count != 0 {
		t.Fatal("another account's unread item leaked", count, err)
	}
	cursor := ""
	want := 200
	for {
		page, err := s.Inbox(member, cursor, InboxPageLimit)
		if err != nil || len(page.Items) == 0 || len(page.Items) > InboxPageLimit {
			t.Fatal(len(page.Items), err)
		}
		for _, item := range page.Items {
			if item.ID != fmt.Sprintf("notice-%03d", want) || (item.ReadAt == "") != (want == 0) {
				t.Fatal("page skipped, duplicated or misread an item", want, item)
			}
			want--
		}
		if !page.HasMore {
			if page.NextCursor != "" {
				t.Fatal("final page advertises another cursor")
			}
			break
		}
		if cursor == "" {
			_, err = s.Inbox(owner, page.NextCursor, InboxPageLimit)
			requireCode(t, err, "invalid_page")
		}
		cursor = page.NextCursor
	}
	if want != -1 {
		t.Fatal("rows missing from Activity", want)
	}
	if err = s.ReadInboxItem(member, "notice-000"); err != nil {
		t.Fatal(err)
	}
	if count, err := s.CountUnreadInbox(member); err != nil || count != 0 {
		t.Fatal("reading the oldest item did not update the count", count, err)
	}
	_, err = s.CountUnreadInbox(context.Background())
	requireCode(t, err, "unauthenticated")
}
