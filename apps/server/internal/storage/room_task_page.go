package storage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

type RoomTaskPage struct {
	Tasks      []json.RawMessage `json:"tasks"`
	NextCursor string            `json:"nextCursor"`
}
type roomTaskPosition struct {
	Room      string `json:"room"`
	Attention bool   `json:"attention"`
	At        string `json:"at"`
	ID        string `json:"id"`
}

func (s *Store) RoomTasksPage(ctx context.Context, room, cursor string, attention bool) (RoomTaskPage, error) {
	page := RoomTaskPage{Tasks: []json.RawMessage{}}
	if _, err := s.GetRoom(ctx, room); err != nil {
		return page, err
	}
	position := roomTaskPosition{Room: room, Attention: attention, At: "~", ID: "~"}
	if cursor != "" {
		raw, err := base64.RawURLEncoding.DecodeString(cursor)
		if err != nil || len(raw) > 512 || json.Unmarshal(raw, &position) != nil || position.Room != room || position.Attention != attention || !validText(position.ID, 128, false) {
			return page, problem("invalid_page", "The collaboration cursor is invalid.")
		}
		if _, err = time.Parse(time.RFC3339Nano, position.At); err != nil {
			return page, problem("invalid_page", "The collaboration cursor is invalid.")
		}
	}
	type row struct{ value, at, id string }
	rows := []row{}
	if attention {
		values, err := s.queries().RoomTaskAttentionPage(ctx, sqlgen.RoomTaskAttentionPageParams{RoomID: room, BeforeAt: position.At, BeforeID: position.ID, PageLimit: 21})
		if err != nil {
			return page, err
		}
		for _, v := range values {
			rows = append(rows, row{v.Value, v.CreatedAt, v.ID})
		}
	} else {
		values, err := s.queries().RoomTaskListPage(ctx, sqlgen.RoomTaskListPageParams{RoomID: room, BeforeAt: position.At, BeforeID: position.ID, PageLimit: 21})
		if err != nil {
			return page, err
		}
		for _, v := range values {
			rows = append(rows, row{v.Value, v.CreatedAt, v.ID})
		}
	}
	size := 0
	for i, v := range rows {
		if i == 20 || size+len(v.value) > RoomRunTimelineBytes-2048 {
			if len(page.Tasks) == 0 {
				return page, problem("invalid_page", "This collaboration cannot fit a bounded page. Open its details directly.")
			}
			break
		}
		page.Tasks = append(page.Tasks, json.RawMessage(v.value))
		size += len(v.value) + 1
		if i < len(rows)-1 {
			position.At, position.ID = v.at, v.id
			raw, _ := json.Marshal(position)
			page.NextCursor = base64.RawURLEncoding.EncodeToString(raw)
		} else {
			page.NextCursor = ""
		}
	}
	return page, nil
}
