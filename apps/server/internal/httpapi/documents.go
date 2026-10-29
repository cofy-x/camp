package httpapi

import (
	"context"
	"net/http"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type documentPreparer interface {
	PrepareDocument(context.Context, string, string, string, bool) (storage.Document, error)
}

func (a *API) prepareDocument(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Commit string `json:"commit"`
		Path   string `json:"path"`
		Retry  bool   `json:"retry"`
	}
	if !decode(w, r, &in, 8192) {
		return
	}
	service, ok := a.rooms.(documentPreparer)
	if !ok {
		writeError(w, 503, "execution_unavailable", "Document preparation is not configured.")
		return
	}
	d, err := service.PrepareDocument(r.Context(), r.PathValue("id"), in.Commit, in.Path, in.Retry)
	a.respond(w, r, map[string]any{"document": d}, err)
}
func (a *API) latestDocument(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()["commit"]) != 1 || len(r.URL.Query()["path"]) != 1 {
		writeError(w, 400, "invalid_reference", "Provide one file path and exact resource revision.")
		return
	}
	d, err := a.store.LatestDocument(r.Context(), r.PathValue("id"), r.URL.Query().Get("commit"), r.URL.Query().Get("path"))
	a.respond(w, r, map[string]any{"document": d}, err)
}
