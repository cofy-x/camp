package httpapi

import "net/http"

func (a *API) latestDocument(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()["commit"]) != 1 || len(r.URL.Query()["path"]) != 1 {
		writeError(w, 400, "invalid_reference", "Provide one file path and exact resource revision.")
		return
	}
	d, err := a.store.LatestDocument(r.Context(), r.PathValue("id"), r.URL.Query().Get("commit"), r.URL.Query().Get("path"))
	a.respond(w, r, map[string]any{"document": d}, err)
}
