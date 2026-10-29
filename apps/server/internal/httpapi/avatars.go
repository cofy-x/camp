package httpapi

import (
	"net/http"

	"github.com/cofy-x/camp/apps/server/internal/avatars"
)

func (a *API) avatarRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/avatars", func(w http.ResponseWriter, r *http.Request) {
		a.write(w, http.StatusOK, map[string]any{"avatars": avatars.Catalog()})
	})
	mux.HandleFunc("GET /api/avatars/{id}", func(w http.ResponseWriter, r *http.Request) {
		image, ok := avatars.Image(r.PathValue("id"))
		if !ok {
			writeError(w, http.StatusNotFound, "not_found", "This avatar is not available.")
			return
		}
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "private, max-age=31536000, immutable")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		_, _ = w.Write(image)
	})
	mux.HandleFunc("PATCH /api/me/avatar", func(w http.ResponseWriter, r *http.Request) {
		var in struct {
			Avatar         string `json:"avatar"`
			ExpectedAvatar string `json:"expectedAvatar"`
		}
		if !decode(w, r, &in, 1024) {
			return
		}
		user, err := a.store.UpdateOwnAvatar(r.Context(), in.Avatar, in.ExpectedAvatar)
		a.respond(w, r, map[string]any{"user": user}, err)
	})
}
