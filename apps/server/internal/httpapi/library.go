package httpapi

import (
	"net/http"

	"github.com/cofy-x/camp/apps/server/internal/library"
)

func (a *API) EnableLibrary(service *library.Service) { a.library = service }
func (a *API) libraryRoutes(mux *http.ServeMux) {
	register := func(route string, h http.HandlerFunc) { mux.HandleFunc(route, a.authorizeRoute(route, h)) }
	register("GET /api/library", func(w http.ResponseWriter, r *http.Request) {
		files, err := a.store.LibraryResources(r.Context())
		if err != nil {
			a.fail(w, r, err)
			return
		}
		rooms, err := a.store.ListRooms(r.Context())
		a.respond(w, r, map[string]any{"resources": files, "rooms": rooms}, err)
	})
	register("GET /api/resources/{id}/history", func(w http.ResponseWriter, r *http.Request) {
		before, ok := queryCursor(w, r, "before")
		if !ok {
			return
		}
		items, err := a.store.ListPublications(r.Context(), r.PathValue("id"), before)
		a.respond(w, r, map[string]any{"publications": items}, err)
	})
	register("GET /api/resources/{id}/patch", func(w http.ResponseWriter, r *http.Request) {
		if !a.libraryReady(w) {
			return
		}
		if len(r.URL.Query()["from"]) != 1 || len(r.URL.Query()["to"]) != 1 {
			writeError(w, 400, "invalid_reference", "Provide two exact resource revisions.")
			return
		}
		patch, err := a.library.Patch(r.Context(), r.PathValue("id"), r.URL.Query().Get("from"), r.URL.Query().Get("to"))
		if err != nil {
			a.fail(w, r, err)
			return
		}
		w.Header().Set("Content-Type", "text/x-patch; charset=utf-8")
		w.Header().Set("Content-Disposition", `attachment; filename="changes.patch"`)
		_, _ = w.Write(patch)
	})
	register("PUT /api/resources/{id}/files", func(w http.ResponseWriter, r *http.Request) {
		var in library.Edit
		if !decode(w, r, &in, 2<<20) || !a.libraryReady(w) {
			return
		}
		result, err := a.library.Edit(r.Context(), r.PathValue("id"), in)
		a.respond(w, r, result, err)
	})
	register("POST /api/resources/{id}/restore", func(w http.ResponseWriter, r *http.Request) {
		var in library.Restore
		if !decode(w, r, &in, 4096) || !a.libraryReady(w) {
			return
		}
		result, err := a.library.Restore(r.Context(), r.PathValue("id"), in)
		a.respond(w, r, result, err)
	})
	register("POST /api/resources/{id}/copy-file", func(w http.ResponseWriter, r *http.Request) {
		var in library.Copy
		if !decode(w, r, &in, 8192) || !a.libraryReady(w) {
			return
		}
		result, err := a.library.Copy(r.Context(), r.PathValue("id"), in)
		a.respond(w, r, result, err)
	})
}
func (a *API) libraryReady(w http.ResponseWriter) bool {
	if a.library == nil {
		writeError(w, 503, "unavailable", "Library publication is unavailable.")
		return false
	}
	return true
}
