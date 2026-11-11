package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"slices"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

const MessageWindowLimit = 50
const MessageWindowBytes = 512 << 10

type MessageWindowPosition struct {
	Before int64
	After  int64
	Around string
}

type MessageWindow struct {
	Messages []Message `json:"messages"`
	Through  int64     `json:"through"`
	HasOlder bool      `json:"hasOlder"`
	HasNewer bool      `json:"hasNewer"`
}

// Message windows seek within one indexed conversation. A thread's original
// root is read separately; it must not turn every reply page into a Room scan.
func (s *Store) RoomMessageWindow(ctx context.Context, room, thread string, position MessageWindowPosition) (out MessageWindow, err error) {
	out.Messages = []Message{}
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		meta, err := q.GetRoom(ctx, room)
		if err != nil {
			return missing(err, "The room")
		}
		out.Through = meta.LastMessageSeq
		var root *Message
		if thread != "" {
			row, err := q.GetMessage(ctx, thread)
			if err != nil {
				return missing(err, "The thread")
			}
			if row.RoomID != room || row.ThreadRootID.String != "" {
				return problem("not_found", "The thread does not exist in this room.")
			}
			message, err := messageRow(row)
			if err != nil {
				return err
			}
			root = &message
		}
		before := position.Before
		anchor := int64(0)
		if position.Around != "" {
			row, err := q.GetMessage(ctx, position.Around)
			if err != nil {
				return missing(err, "The message")
			}
			if row.RoomID != room || (row.ThreadRootID.String != thread && row.ID != thread) {
				return problem("not_found", "The message does not belong to this conversation.")
			}
			if row.ID != thread {
				anchor = row.Seq
				before = row.Seq + 1
			}
		}
		limit := MessageWindowLimit
		if anchor != 0 {
			limit = MessageWindowLimit/2 + 1
		}
		var rows []sqlgen.Message
		if position.After > 0 {
			rows, err = q.MessagesAfter(ctx, sqlgen.MessagesAfterParams{RoomID: room, ThreadID: thread, AfterSeq: position.After, PageLimit: MessageWindowLimit})
		} else {
			if before == 0 {
				before = math.MaxInt64
			}
			rows, err = q.MessagesBefore(ctx, sqlgen.MessagesBeforeParams{RoomID: room, ThreadID: thread, BeforeSeq: before, PageLimit: int64(limit)})
			slices.Reverse(rows)
		}
		if err != nil {
			return err
		}
		if anchor != 0 {
			newer, err := q.MessagesAfter(ctx, sqlgen.MessagesAfterParams{RoomID: room, ThreadID: thread, AfterSeq: anchor, PageLimit: int64(MessageWindowLimit - len(rows))})
			if err != nil {
				return err
			}
			rows = append(rows, newer...)
		}
		for _, row := range rows {
			message, err := messageRow(row)
			if err != nil {
				return err
			}
			out.Messages = append(out.Messages, message)
		}
		if err := messageWorkOrigins(ctx, q, out.Messages); err != nil {
			return err
		}
		if root != nil {
			roots := []Message{*root}
			if err := messageWorkOrigins(ctx, q, roots); err != nil {
				return err
			}
			root = &roots[0]
		}
		// Encode each original once. Re-encoding the whole window for every
		// removed row makes a page of long replies needlessly expensive.
		header, _ := json.Marshal(MessageWindow{Messages: []Message{}, Through: out.Through})
		size := len(header) + 1
		sizes := make([]int, len(out.Messages))
		for i, message := range out.Messages {
			encoded, err := json.Marshal(message)
			if err != nil {
				return err
			}
			sizes[i] = len(encoded) + 1
			size += sizes[i]
		}
		if root != nil {
			encoded, err := json.Marshal(root)
			if err != nil {
				return err
			}
			size += len(encoded) + 1
		}
		left, right := 0, len(out.Messages)
		for size > MessageWindowBytes {
			if right-left <= 1 {
				return problem("invalid_page", "This message cannot fit a history window. Open the original message directly.")
			}
			trimFirst := position.After == 0 && anchor == 0
			if anchor != 0 {
				trimFirst = anchor-out.Messages[left].Seq > out.Messages[right-1].Seq-anchor
			}
			if trimFirst {
				size -= sizes[left]
				left++
			} else {
				right--
				size -= sizes[right]
			}
		}
		out.Messages = out.Messages[left:right]

		first, last := int64(math.MaxInt64), int64(0)
		if len(out.Messages) > 0 {
			first = out.Messages[0].Seq
			last = out.Messages[len(out.Messages)-1].Seq
		}
		edges, err := q.MessageWindowEdges(ctx, sqlgen.MessageWindowEdgesParams{RoomID: room, ThreadID: thread, FirstSeq: first, LastSeq: last})
		if err != nil {
			return err
		}
		out.HasOlder, out.HasNewer = edges.HasOlder, edges.HasNewer
		if root != nil {
			out.Messages = append([]Message{*root}, out.Messages...)
		}
		return nil
	})
	return out, err
}
