package httpapi

import (
	"net/http"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func (a *API) memoryRoutes(mux *http.ServeMux) {
	register := func(route string, h http.HandlerFunc) { mux.HandleFunc(route, a.authorizeRoute(route, h)) }
	register("GET /api/rooms/{id}/memories", func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Query()["q"]) > 1 {
			writeError(w, 400, "invalid_memory", "Provide one memory query.")
			return
		}
		limit, _, ok := pageNumber(w, r, "limit")
		if !ok {
			return
		}
		if limit > storage.MaxMemoryResults {
			writeError(w, 400, "invalid_memory", "Request at most 20 memories.")
			return
		}
		v, e := a.store.Memories(r.Context(), r.PathValue("id"), r.URL.Query().Get("q"), int(limit))
		a.respond(w, r, map[string]any{"memories": v}, e)
	})
	register("GET /api/memories/{id}", func(w http.ResponseWriter, r *http.Request) {
		revision, present, ok := pageNumber(w, r, "revision")
		if !ok {
			return
		}
		if present && revision == 0 {
			writeError(w, 400, "invalid_memory", "Choose a positive memory revision.")
			return
		}
		v, e := a.store.GetMemory(r.Context(), r.PathValue("id"), revision)
		a.respond(w, r, map[string]any{"memory": v}, e)
	})
	save := func(create bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var in orchestration.MemoryInput
			if !decode(w, r, &in, 24<<10) {
				return
			}
			room, id := "", r.PathValue("id")
			if create {
				room, id = id, ""
			}
			if old, found, e := a.store.FindSaveMemory(r.Context(), room, id, in); e != nil || found {
				a.respond(w, r, map[string]any{"memory": old}, e)
				return
			}
			if in.Provenance.Kind == "file" {
				p := in.Provenance
				if _, e := a.store.ValidateFileReference(r.Context(), room, storage.ReferenceInput{Kind: "file", ResourceID: p.ResourceID, Commit: p.Commit, Path: p.Path}); e != nil {
					a.fail(w, r, e)
					return
				}
				if !a.runtime(w) {
					return
				}
				if _, e := a.rooms.ResourceFile(r.Context(), p.ResourceID, p.Path, p.Commit); e != nil {
					a.fail(w, r, e)
					return
				}
			}
			v, e := a.store.SaveMemory(r.Context(), room, id, in)
			a.respond(w, r, map[string]any{"memory": v}, e)
		}
	}
	register("POST /api/rooms/{id}/memories", save(true))
	register("PUT /api/memories/{id}", save(false))
	register("POST /api/memories/{id}/retract", func(w http.ResponseWriter, r *http.Request) {
		var in orchestration.MemoryRetraction
		if !decode(w, r, &in, 4096) {
			return
		}
		v, e := a.store.RetractMemory(r.Context(), r.PathValue("id"), in)
		a.respond(w, r, map[string]any{"memory": v}, e)
	})
	register("GET /api/memory-requests/{id}", func(w http.ResponseWriter, r *http.Request) {
		v, e := a.store.MemoryRequest(r.Context(), r.PathValue("id"))
		a.respond(w, r, v, e)
	})
	register("POST /api/memory-requests/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !decode(w, r, &in, 4096) {
			return
		}
		v, e := a.store.CancelMemoryRequest(r.Context(), r.PathValue("id"))
		a.respond(w, r, v, e)
	})
}
