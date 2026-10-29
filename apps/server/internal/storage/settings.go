package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func (s *Store) Bootstrap(ctx context.Context, clientID string) (Bootstrap, error) {
	out := Bootstrap{Preferences: Preferences{Wallpaper: "lavender", IconSnap: true, ControlStrip: true}, Layout: json.RawMessage("null")}
	uid := actorID(ctx)
	if uid == "" {
		return out, nil
	}
	err := s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		raw, e := q.GetUserPreferences(ctx, uid)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		if e == nil {
			if e = json.Unmarshal([]byte(raw), &out.Preferences); e != nil {
				return e
			}
		}
		raw, e = q.GetUserLayout(ctx, sqlgen.GetUserLayoutParams{UserID: uid, ClientID: clientID})
		if errors.Is(e, sql.ErrNoRows) {
			return nil
		}
		if e != nil {
			return e
		}
		out.Layout = json.RawMessage(raw)
		return nil
	})
	return out, err
}
func (s *Store) SavePreferences(ctx context.Context, p Preferences) error {
	if p.Wallpaper != "lavender" && p.Wallpaper != "grid" && p.Wallpaper != "solid" {
		return problem("invalid_wallpaper", "Choose an available desktop pattern.")
	}
	uid := actorID(ctx)
	if uid == "" {
		return problem("unauthenticated", "Sign in to Camp.")
	}
	raw, e := json.Marshal(p)
	if e != nil {
		return e
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if err := requireActor(ctx, q, false); err != nil {
			return err
		}
		return q.SaveUserPreferences(ctx, sqlgen.SaveUserPreferencesParams{UserID: uid, Value: string(raw)})
	})
}
func (s *Store) SaveLayout(ctx context.Context, clientID string, layout json.RawMessage) error {
	if len(clientID) == 0 || len(clientID) > 128 {
		return problem("invalid_client", "A browser client identifier is required.")
	}
	var value map[string]json.RawMessage
	if len(layout) > 256<<10 || json.Unmarshal(layout, &value) != nil || value == nil {
		return problem("invalid_layout", "Layout must be a JSON object no larger than 256 KiB.")
	}
	uid := actorID(ctx)
	if uid == "" {
		return problem("unauthenticated", "Sign in to Camp.")
	}
	return s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if err := requireActor(ctx, q, false); err != nil {
			return err
		}
		return q.SaveUserLayout(ctx, sqlgen.SaveUserLayoutParams{UserID: uid, ClientID: clientID, Value: string(layout)})
	})
}
