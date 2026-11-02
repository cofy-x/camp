package httpapi

import (
	"net/http"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func (a *API) orchestrationRoutes(mux *http.ServeMux) {
	const route = "GET /api/rooms/{id}/orchestration"
	mux.HandleFunc(route, a.authorizeRoute(route, func(w http.ResponseWriter, r *http.Request) {
		after, _, ok := pageNumber(w, r, "after")
		if !ok {
			return
		}
		through, present, ok := pageNumber(w, r, "through")
		if !ok {
			return
		}
		var end *int64
		if present {
			end = &through
		}
		limit, _, ok := pageNumber(w, r, "limit")
		if !ok {
			return
		}
		if limit > storage.OrchestrationPageLimit {
			writeError(w, 400, "invalid_page", "Read at most 20 admission records at a time.")
			return
		}
		page, err := a.store.OrchestrationPage(r.Context(), r.PathValue("id"), after, end, int(limit))
		a.respond(w, r, page, err)
	}))
}
