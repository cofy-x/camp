package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type userRunsCanceller interface {
	CancelUser(context.Context, string) error
}

func (a *API) teamRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/team", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.store.GetTeam(r.Context())
		a.respond(w, r, map[string]any{"team": v}, e)
	})
	mux.HandleFunc("PATCH /api/team", a.admin(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Name string `json:"name"`
		}
		if !decode(w, r, &in, 1024) {
			return
		}
		v, e := a.store.RenameTeam(r.Context(), in.Name)
		a.respond(w, r, map[string]any{"team": v}, e)
	}))
	mux.HandleFunc("GET /api/me/drafts", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.store.ListDrafts(r.Context(), r.URL.Query().Get("kind"))
		a.respond(w, r, map[string]any{"drafts": v}, e)
	})
	mux.HandleFunc("GET /api/team/members", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.store.ListUsers(r.Context())
		a.respond(w, r, map[string]any{"members": v}, e)
	})
	mux.HandleFunc("PATCH /api/team/members/{id}", a.admin(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Role    *string `json:"role"`
			Enabled *bool   `json:"enabled"`
		}
		if !decode(w, r, &in, 4096) {
			return
		}
		u, e := a.team.UpdateUser(r.Context(), r.PathValue("id"), in.Role, in.Enabled)
		if e == nil && !u.Enabled {
			cleanup, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
			defer cancel()
			if manager, ok := a.rooms.(userRunsCanceller); ok {
				e = manager.CancelUser(cleanup, u.ID)
			} else if a.rooms != nil {
				var runs []storage.Run
				runs, e = a.store.ListUserActiveRuns(cleanup, u.ID)
				for _, run := range runs {
					if e != nil {
						break
					}
					_, e = a.rooms.Cancel(cleanup, run.ID)
				}
			}
		}
		a.respond(w, r, map[string]any{"member": u}, e)
	}))
	mux.HandleFunc("GET /api/team/invites", a.admin(func(w http.ResponseWriter, r *http.Request) {
		v, e := a.store.ListInvites(r.Context())
		a.respond(w, r, map[string]any{"invites": v}, e)
	}))
	mux.HandleFunc("POST /api/team/invites", a.admin(func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			ExpiresInHours int `json:"expiresInHours"`
		}
		if !decode(w, r, &in, 4096) {
			return
		}
		v, token, e := a.team.CreateInvite(r.Context(), in.ExpiresInHours)
		a.respond(w, r, map[string]any{"invite": v, "token": token}, e)
	}))
	mux.HandleFunc("DELETE /api/team/invites/{id}", a.admin(func(w http.ResponseWriter, r *http.Request) {
		a.respond(w, r, map[string]bool{"ok": true}, a.store.RevokeInvite(r.Context(), r.PathValue("id")))
	}))
	mux.HandleFunc("GET /api/me/drafts/{kind}", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.store.GetDraft(r.Context(), r.PathValue("kind"), r.URL.Query().Get("key"))
		a.respond(w, r, map[string]any{"draft": v}, e)
	})
	mux.HandleFunc("PUT /api/me/drafts/{kind}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Key      string          `json:"key"`
			Value    json.RawMessage `json:"value"`
			Revision int64           `json:"revision"`
		}
		if !decode(w, r, &in, (2<<20)+8192) {
			return
		}
		v, e := a.store.SaveDraft(r.Context(), r.PathValue("kind"), in.Key, in.Value, in.Revision)
		a.respond(w, r, map[string]any{"draft": v}, e)
	})
	mux.HandleFunc("DELETE /api/me/drafts/{kind}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Key      string `json:"key"`
			Revision int64  `json:"revision"`
		}
		if !decode(w, r, &in, 4096) {
			return
		}
		draft, err := a.store.DeleteDraft(r.Context(), r.PathValue("kind"), in.Key, in.Revision)
		a.respond(w, r, map[string]any{"ok": true, "draft": draft}, err)
	})
	mux.HandleFunc("GET /api/me/room-reads", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.store.ListRoomReads(r.Context())
		a.respond(w, r, map[string]any{"reads": v}, e)
	})
	mux.HandleFunc("PUT /api/me/room-reads/{roomId}", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Seq int64 `json:"seq"`
		}
		if !decode(w, r, &in, 1024) {
			return
		}
		a.respond(w, r, map[string]bool{"ok": true}, a.store.SaveRoomRead(r.Context(), r.PathValue("roomId"), in.Seq))
	})
	mux.HandleFunc("GET /api/me/inbox", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.store.ListInbox(r.Context())
		a.respond(w, r, map[string]any{"items": v}, e)
	})
	mux.HandleFunc("POST /api/me/inbox/{id}/read", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !decode(w, r, &in, 1024) {
			return
		}
		a.respond(w, r, map[string]bool{"ok": true}, a.store.ReadInboxItem(r.Context(), r.PathValue("id")))
	})
}
