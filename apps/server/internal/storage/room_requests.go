package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

type RoomCreateInput struct {
	RequestID   string `json:"requestId,omitempty"`
	Name        string `json:"name"`
	Description string `json:"description"`
}

type RoomRenameInput struct {
	RequestID         string
	Name              string
	ExpectedUpdatedAt string
}

func roomRequestHash(kind, roomID string, input any) string {
	raw, _ := json.Marshal([]any{kind, roomID, input})
	return remoteHash(raw)
}

func findRoomRequest(ctx context.Context, q *sqlgen.Queries, requestID, hash string) (room Room, found bool, err error) {
	if actorID(ctx) == "" {
		return room, false, problem("unauthenticated", "Sign in to manage Room requests.")
	}
	if err = requireActor(ctx, q, false); err != nil {
		return room, false, err
	}
	if !validText(requestID, 128, false) {
		return room, false, problem("invalid_room", "Provide a stable Room request ID of at most 128 bytes.")
	}
	row, err := q.GetRoomRequest(ctx, sqlgen.GetRoomRequestParams{UserID: actorID(ctx), RequestID: requestID})
	if errors.Is(err, sql.ErrNoRows) {
		return room, false, nil
	}
	if err != nil {
		return room, false, err
	}
	if hash != "" && hash != row.RequestHash {
		return room, false, problem("request_conflict", "This request ID belongs to a different Room operation.")
	}
	err = json.Unmarshal([]byte(row.Result), &room)
	return room, true, err
}

func saveRoomRequest(ctx context.Context, q *sqlgen.Queries, requestID, hash string, room Room) error {
	raw, err := json.Marshal(room)
	if err != nil {
		return err
	}
	return q.SaveRoomRequest(ctx, sqlgen.SaveRoomRequestParams{UserID: actorID(ctx), RequestID: requestID, RequestHash: hash, Result: string(raw)})
}

func (s *Store) readRoomRequest(ctx context.Context, requestID, hash string) (room Room, found bool, err error) {
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		var readErr error
		room, found, readErr = findRoomRequest(ctx, sqlgen.New(tx), requestID, hash)
		return readErr
	})
	return
}

func (s *Store) RoomRequest(ctx context.Context, requestID string) (Room, error) {
	room, found, err := s.readRoomRequest(ctx, requestID, "")
	if err == nil && !found {
		return room, problem("not_found", "The Room request has no saved receipt.")
	}
	return room, err
}

// FindCreateRoomRequest avoids preparing another resource for a recorded create.
func (s *Store) FindCreateRoomRequest(ctx context.Context, input RoomCreateInput) (Room, bool, error) {
	return s.readRoomRequest(ctx, input.RequestID, roomRequestHash("create", "", input))
}

func (s *Store) RenameRoom(ctx context.Context, id string, input RoomRenameInput) (room Room, err error) {
	hash := roomRequestHash("rename", id, input)
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		previous, found, err := findRoomRequest(ctx, q, input.RequestID, hash)
		if err != nil || found {
			room = previous
			return err
		}
		if _, err = time.Parse(time.RFC3339Nano, input.ExpectedUpdatedAt); err != nil {
			return problem("invalid_room", "Provide the saved Room version before renaming.")
		}
		current, err := q.GetRoom(ctx, id)
		if err != nil {
			return missing(err, "The room")
		}
		if current.UpdatedAt != input.ExpectedUpdatedAt {
			return problem("revision_conflict", "The Room changed. Reload it before renaming.")
		}
		room = roomRow(current)
		room.Name = input.Name
		room.UpdatedAt = now()
		if err = updateRoom(ctx, q, &room); err != nil {
			return err
		}
		return saveRoomRequest(ctx, q, input.RequestID, hash, room)
	})
	return
}
