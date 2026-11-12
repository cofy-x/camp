package httpapi

import (
	"net/http"
	"strconv"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func (a *API) runActivity(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	position := storage.RunActivityPosition{}
	for key, values := range query {
		if len(values) != 1 || (key != "before" && key != "after" && key != "through" && key != "limit") {
			writeError(w, 400, "invalid_page", "Choose one activity window.")
			return
		}
		value, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil || value < 0 || strconv.FormatInt(value, 10) != values[0] || (key != "through" && value == 0) {
			writeError(w, 400, "invalid_page", "Use a valid activity sequence or page size.")
			return
		}
		switch key {
		case "before":
			position.Before = value
		case "after":
			position.After = value
		case "through":
			position.Through = &value
		case "limit":
			if value > storage.RunActivityLimit {
				writeError(w, 400, "invalid_page", "Read at most 50 activity entries.")
				return
			}
			position.Limit = int(value)
		}
	}
	page, err := a.store.RunActivity(r.Context(), r.PathValue("id"), position)
	a.respond(w, r, page, err)
}
