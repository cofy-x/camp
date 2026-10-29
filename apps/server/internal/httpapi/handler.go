package httpapi

import (
	"context"
	"encoding/json"
	"github.com/cofy-x/camp/apps/server/internal/configuration"
	"github.com/cofy-x/camp/apps/server/internal/library"
	"github.com/cofy-x/camp/apps/server/internal/remoteruns"
	"log/slog"
	"net/http"

	"github.com/cofy-x/camp/apps/server/internal/storage"
	"github.com/cofy-x/camp/apps/server/internal/team"
)

type API struct {
	settings     SettingsService
	importPolicy configuration.ImportPolicy
	library      *library.Service
	team         *team.Service
	authOrigins  map[string]bool
	publicOrigin string
	authLimiter  authLimiter
	store        *storage.Store
	logger       *slog.Logger
	rooms        RoomService
	remote       *remoteruns.Manager
	handler      http.Handler
	streams      context.Context
	stopStreams  context.CancelFunc
}

func New(store *storage.Store, logger *slog.Logger, managers ...RoomService) *API {
	if logger == nil {
		logger = slog.Default()
	}
	a := &API{store: store, logger: logger}
	a.streams, a.stopStreams = context.WithCancel(context.Background())
	if len(managers) > 0 {
		a.rooms = managers[0]
	}
	mux := http.NewServeMux()
	a.roomRoutes(mux)
	a.taskRoutes(mux)
	a.runnerInstallationRoutes(mux)
	a.authRoutes(mux)
	a.avatarRoutes(mux)
	a.teamRoutes(mux)
	a.libraryRoutes(mux)
	a.reviewRoutes(mux)
	a.settingsRoutes(mux)
	a.remoteRoutes(mux)
	mux.HandleFunc("GET /api/health", func(w http.ResponseWriter, r *http.Request) {
		if err := store.Ping(r.Context()); err != nil {
			a.fail(w, r, err)
			return
		}
		a.write(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /api/bootstrap", func(w http.ResponseWriter, r *http.Request) {
		b, err := store.Bootstrap(r.Context(), r.URL.Query().Get("clientId"))
		a.respond(w, r, b, err)
	})
	mux.HandleFunc("PUT /api/preferences", func(w http.ResponseWriter, r *http.Request) {
		var p storage.Preferences
		if !decode(w, r, &p, 4<<10) {
			return
		}
		a.respond(w, r, p, store.SavePreferences(r.Context(), p))
	})
	mux.HandleFunc("PUT /api/layout", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ClientID string          `json:"clientId"`
			Layout   json.RawMessage `json:"layout"`
		}
		if !decode(w, r, &in, 2<<20) {
			return
		}
		a.respond(w, r, map[string]bool{"ok": true}, store.SaveLayout(r.Context(), in.ClientID, in.Layout))
	})
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeError(w, http.StatusNotFound, "not_found", "Unknown API endpoint.")
	})
	a.handler = mux
	return a
}

func (a *API) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	a.handler.ServeHTTP(w, r)
}

// StopStreams ends long-lived event subscriptions before the HTTP server drains
// ordinary requests. It is safe to call repeatedly and does not cancel writes.
func (a *API) StopStreams() { a.stopStreams() }
