package storage

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/cofy-x/camp/apps/server/internal/storage/internal/sqlgen"
)

func (s *Store) Bootstrap(ctx context.Context, clientID string) (Bootstrap, error) {
	out := Bootstrap{Layout: json.RawMessage("null")}
	uid := actorID(ctx)
	err := s.readTransaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		var e error
		// Domain reads mount only after bootstrap. Start notifications at this
		// frontier, before any initial snapshot, so none of those reads can miss
		// a change committed while the browser is starting.
		out.WorkspaceEventCursor, e = q.WorkspaceEventCursor(ctx)
		if e != nil {
			return e
		}
		out.Preferences, e = getPreferences(ctx, q, uid)
		if e != nil || uid == "" {
			return e
		}
		raw, e := q.GetUserLayout(ctx, sqlgen.GetUserLayoutParams{UserID: uid, ClientID: clientID})
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

func getPreferences(ctx context.Context, q *sqlgen.Queries, uid string) (Preferences, error) {
	p := Preferences{Wallpaper: "lavender", IconSnap: true, ControlStrip: true}
	if uid == "" {
		return p, nil
	}
	raw, err := q.GetUserPreferences(ctx, uid)
	if errors.Is(err, sql.ErrNoRows) {
		return p, nil
	}
	if err != nil {
		return p, err
	}
	err = json.Unmarshal([]byte(raw), &p)
	return p, err
}

func validatePreferences(p Preferences) error {
	if p.Wallpaper != "lavender" && p.Wallpaper != "grid" && p.Wallpaper != "solid" {
		return problem("invalid_wallpaper", "Choose an available desktop pattern.")
	}
	return nil
}

func savePreferences(ctx context.Context, q *sqlgen.Queries, uid string, p Preferences) error {
	raw, err := json.Marshal(p)
	if err != nil {
		return err
	}
	return q.SaveUserPreferences(ctx, sqlgen.SaveUserPreferencesParams{UserID: uid, Value: string(raw)})
}

func (s *Store) SavePreferences(ctx context.Context, p Preferences) error {
	if err := validatePreferences(p); err != nil {
		return err
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
		return savePreferences(ctx, q, uid, p)
	})
}

func (s *Store) PatchPreferences(ctx context.Context, patch PreferencesPatch) (out Preferences, err error) {
	uid := actorID(ctx)
	if uid == "" {
		return out, problem("unauthenticated", "Sign in to Camp.")
	}
	err = s.transaction(ctx, func(tx *sql.Tx) error {
		q := sqlgen.New(tx)
		if err := requireActor(ctx, q, false); err != nil {
			return err
		}
		current, err := getPreferences(ctx, q, uid)
		if err != nil {
			return err
		}
		out = current
		if patch.Wallpaper != nil {
			out.Wallpaper = *patch.Wallpaper
		}
		if patch.IconSnap != nil {
			out.IconSnap = *patch.IconSnap
		}
		if patch.ControlStrip != nil {
			out.ControlStrip = *patch.ControlStrip
		}
		if err := validatePreferences(out); err != nil {
			return err
		}
		if out == current {
			return nil
		}
		return savePreferences(ctx, q, uid, out)
	})
	return
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
