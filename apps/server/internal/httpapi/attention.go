package httpapi

import (
	"net/http"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func (a *API) attentionRoutes(mux *http.ServeMux) {
	const route = "GET /api/attention"
	mux.HandleFunc(route, a.authorizeRoute(route, func(w http.ResponseWriter, r *http.Request) {
		allowed := map[string]bool{"roomId": true, "kind": true, "cursor": true, "limit": true}
		for key, values := range r.URL.Query() {
			if !allowed[key] || len(values) != 1 || (key == "kind" && values[0] == "") {
				writeError(w, 400, "invalid_page", "Provide one value for each supported attention filter.")
				return
			}
		}
		limit, present, ok := pageNumber(w, r, "limit")
		if !ok {
			return
		}
		if (present && limit == 0) || limit > 20 {
			writeError(w, 400, "invalid_page", "Read between one and 20 attention items at a time.")
			return
		}
		query := r.URL.Query()
		page, err := a.store.Attention(r.Context(), storage.AttentionOptions{RoomID: query.Get("roomId"), Kind: query.Get("kind"), Cursor: query.Get("cursor"), Limit: int(limit)})
		a.respond(w, r, page, err)
	}))
}
