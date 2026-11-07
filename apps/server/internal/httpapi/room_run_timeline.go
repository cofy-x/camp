package httpapi

import (
	"context"
	"github.com/cofy-x/camp/apps/server/internal/storage"
	"net/http"
	"strconv"
)

type roomTimelineReader interface {
	RoomRunTimeline(context.Context, string, string, int) (storage.RoomRunTimelinePage, error)
}

func (a *API) roomRunTimeline(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	for key, values := range query {
		if (key != "view" && key != "cursor" && key != "limit") || len(values) != 1 {
			writeError(w, 400, "invalid_page", "Choose one timeline view, cursor and limit.")
			return
		}
	}
	cursor := query.Get("cursor")
	limit := 0
	if value, present := query["limit"]; present {
		parsed, err := strconv.Atoi(value[0])
		if err != nil || parsed < 1 || parsed > storage.RoomRunTimelineLimit || strconv.Itoa(parsed) != value[0] {
			writeError(w, 400, "invalid_page", "Read at most 20 execution summaries at a time.")
			return
		}
		limit = parsed
	}
	var page storage.RoomRunTimelinePage
	var err error
	if reader, ok := a.rooms.(roomTimelineReader); ok {
		page, err = reader.RoomRunTimeline(r.Context(), r.PathValue("id"), cursor, limit)
	} else if _, managed := a.rooms.(runReader); managed {
		writeError(w, http.StatusServiceUnavailable, "execution_unavailable", "Execution timeline summaries are unavailable.")
		return
	} else {
		page, err = a.store.RoomRunTimeline(r.Context(), r.PathValue("id"), cursor, limit)
	}
	a.respond(w, r, page, err)
}
