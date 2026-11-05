package httpapi

import "net/http"

func (a *API) workItemHistoryRoutes(mux *http.ServeMux) {
	const route = "GET /api/work-items/{id}/history/{kind}"
	mux.HandleFunc(route, a.authorizeRoute(route, func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Query()["cursor"]) > 1 {
			writeError(w, 400, "invalid_page", "Provide one work item history cursor.")
			return
		}
		limit, _, ok := pageNumber(w, r, "limit")
		if !ok {
			return
		}
		if limit > 20 {
			writeError(w, 400, "invalid_page", "Read at most 20 history records at a time.")
			return
		}
		page, err := a.store.WorkItemHistory(r.Context(), r.PathValue("id"), r.PathValue("kind"), r.URL.Query().Get("cursor"), int(limit))
		a.respond(w, r, page, err)
	}))
}
