package httpapi

import (
	"context"
	"net/http"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type runSummaryReader interface {
	GetRunSummary(context.Context, string) (storage.RunTimeline, error)
}

func (a *API) runSummary(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	if len(query) != 1 || len(query["view"]) != 1 || query.Get("view") != "summary" {
		writeError(w, http.StatusBadRequest, "invalid_page", "Choose the summary view or omit view for the full execution.")
		return
	}
	var item storage.RunTimeline
	var err error
	if reader, ok := a.rooms.(runSummaryReader); ok {
		item, err = reader.GetRunSummary(r.Context(), r.PathValue("id"))
	} else if _, managed := a.rooms.(runReader); managed {
		writeError(w, http.StatusServiceUnavailable, "execution_unavailable", "Execution summaries are unavailable.")
		return
	} else {
		item, err = a.store.GetRunSummary(r.Context(), r.PathValue("id"))
	}
	a.respond(w, r, map[string]any{"run": item}, err)
}
