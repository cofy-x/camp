package httpapi

import "net/http"

func (a *API) automationEvaluationContextRoutes(mux *http.ServeMux) {
	const route = "GET /api/automation-evaluations/{id}/context/{kind}"
	mux.HandleFunc(route, a.authorizeRoute(route, func(w http.ResponseWriter, r *http.Request) {
		after, _, ok := pageNumber(w, r, "after")
		if !ok {
			return
		}
		limit, _, ok := pageNumber(w, r, "limit")
		if !ok {
			return
		}
		if limit > 10 {
			writeError(w, 400, "invalid_page", "Read at most 10 frozen context records at a time.")
			return
		}
		page, err := a.store.AutomationEvaluationContext(r.Context(), r.PathValue("id"), r.PathValue("kind"), after, int(limit))
		a.respond(w, r, page, err)
	}))
}
