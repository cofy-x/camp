package httpapi

import (
	"net/http"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func (a *API) prepareDocument(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Commit string `json:"commit"`
		Path   string `json:"path"`
		Retry  bool   `json:"retry"`
	}
	if !decode(w, r, &in, 8192) {
		return
	}
	// Keep the old operation explicit for existing clients, without enqueuing
	// work or changing any retained document snapshot.
	if _, err := a.store.LatestDocument(r.Context(), r.PathValue("id"), in.Commit, in.Path); err != nil {
		a.respond(w, r, nil, err)
		return
	}
	writeError(w, http.StatusGone, storage.DocumentPreparationDisabledCode, storage.DocumentPreparationDisabledMessage)
}
func (a *API) latestDocument(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()["commit"]) != 1 || len(r.URL.Query()["path"]) != 1 {
		writeError(w, 400, "invalid_reference", "Provide one file path and exact resource revision.")
		return
	}
	d, err := a.store.LatestDocument(r.Context(), r.PathValue("id"), r.URL.Query().Get("commit"), r.URL.Query().Get("path"))
	a.respond(w, r, map[string]any{"document": d}, err)
}
