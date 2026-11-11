package httpapi

import (
	"net/http"
	"strconv"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func (a *API) roomMessageWindow(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	for key, values := range query {
		if len(values) != 1 || (key != "view" && key != "threadRootId" && key != "before" && key != "after" && key != "around") {
			writeError(w, 400, "invalid_page", "Choose one conversation and one history position.")
			return
		}
	}
	position := storage.MessageWindowPosition{Around: query.Get("around")}
	anchors := 0
	for _, key := range []string{"before", "after", "around"} {
		if query.Has(key) {
			anchors++
		}
	}
	if anchors > 1 || (query.Has("around") && (len(position.Around) == 0 || len(position.Around) > 128)) {
		writeError(w, 400, "invalid_page", "Choose one history position.")
		return
	}
	for _, key := range []string{"before", "after"} {
		if !query.Has(key) {
			continue
		}
		value, err := strconv.ParseInt(query.Get(key), 10, 64)
		if err != nil || value < 1 || strconv.FormatInt(value, 10) != query.Get(key) {
			writeError(w, 400, "invalid_page", "Use a positive message sequence.")
			return
		}
		if key == "before" {
			position.Before = value
		} else {
			position.After = value
		}
	}
	page, err := a.store.RoomMessageWindow(r.Context(), r.PathValue("id"), query.Get("threadRootId"), position)
	a.respond(w, r, page, err)
}
