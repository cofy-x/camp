package storage

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

const automationDeliveryPageLimit = 20
const automationDeliveryPageBytes = 320 << 10

type AutomationDeliveryPage struct {
	Deliveries []orchestration.Delivery `json:"deliveries"`
	NextCursor string                   `json:"nextCursor"`
}

type automationDeliveryCursor struct {
	Task string `json:"task"`
	At   string `json:"at"`
	ID   string `json:"id"`
}

// TaskAutomationDeliveriesPage retains complete immutable bodies while bounding
// both encoded response bytes and rows. New arrivals cannot displace older pages.
func (s *Store) TaskAutomationDeliveriesPage(ctx context.Context, id, cursor string, limit int) (page AutomationDeliveryPage, err error) {
	page.Deliveries = []orchestration.Delivery{}
	position := automationDeliveryCursor{Task: id}
	if cursor != "" {
		if len(cursor) > 684 {
			return page, problem("invalid_page", "The delivery cursor is too large.")
		}
		data, e := base64.RawURLEncoding.DecodeString(cursor)
		if e != nil || len(data) > 512 || json.Unmarshal(data, &position) != nil || position.Task != id || !validText(position.ID, 128, false) {
			return page, problem("invalid_page", "Choose a valid delivery cursor for this collaboration.")
		}
		if _, e = time.Parse(time.RFC3339Nano, position.At); e != nil {
			return page, problem("invalid_page", "The delivery cursor has an invalid timestamp.")
		}
	}
	if limit < 0 || limit > automationDeliveryPageLimit {
		return page, problem("invalid_page", "Read at most 20 comment deliveries at a time.")
	}
	if limit == 0 {
		limit = automationDeliveryPageLimit
	}
	err = s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if e := requireActor(ctx, q, false); e != nil {
			return e
		}
		if _, e := getTask(ctx, q, id); e != nil {
			return e
		}
		rows, e := q.AutomationDeliveriesPage(ctx, sqlgen.AutomationDeliveriesPageParams{TaskID: id, BeforeAt: position.At, BeforeID: position.ID, PageLimit: int64(limit + 1)})
		if e != nil {
			return e
		}
		for index, row := range rows {
			if index == limit {
				break
			}
			delivery, e := automationDelivery(row)
			if e != nil {
				return e
			}
			previous := page
			page.Deliveries = append(page.Deliveries, delivery)
			page.NextCursor = ""
			if index+1 < len(rows) {
				next, _ := json.Marshal(automationDeliveryCursor{Task: id, At: row.CreatedAt, ID: row.ID})
				page.NextCursor = base64.RawURLEncoding.EncodeToString(next)
			}
			encoded, e := json.Marshal(page)
			if e != nil {
				return e
			}
			if len(encoded)+1 > automationDeliveryPageBytes {
				page = previous
				if len(page.Deliveries) == 0 {
					return problem("invalid_page", "The complete stored comment cannot fit in a bounded delivery page.")
				}
				break
			}
		}
		return nil
	})
	return page, err
}
