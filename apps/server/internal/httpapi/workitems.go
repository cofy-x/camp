package httpapi

import (
	"net/http"

	"github.com/cofy-x/camp/apps/server/internal/orchestration"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func (a *API) workItemRoutes(mux *http.ServeMux) {
	register := func(route string, h http.HandlerFunc) { mux.HandleFunc(route, a.authorizeRoute(route, h)) }
	register("GET /api/work-items", func(w http.ResponseWriter, r *http.Request) {
		allowed := map[string]bool{"scope": true, "view": true, "roomId": true, "cursor": true, "limit": true}
		for key, values := range r.URL.Query() {
			if !allowed[key] || len(values) != 1 || ((key == "scope" || key == "view") && values[0] == "") {
				writeError(w, 400, "invalid_page", "Provide one value for each supported Work filter.")
				return
			}
		}
		limit, present, ok := pageNumber(w, r, "limit")
		if !ok {
			return
		}
		if (present && limit == 0) || limit > 20 {
			writeError(w, 400, "invalid_page", "Read between one and 20 work items at a time.")
			return
		}
		query := r.URL.Query()
		page, err := a.store.ListWorkItems(r.Context(), storage.WorkListOptions{Scope: query.Get("scope"), View: query.Get("view"), RoomID: query.Get("roomId"), Cursor: query.Get("cursor"), Limit: int(limit)})
		a.respond(w, r, page, err)
	})
	register("GET /api/automation-events/{id}", func(w http.ResponseWriter, r *http.Request) {
		item, err := a.store.GetAutomationEvent(r.Context(), r.PathValue("id"))
		a.respond(w, r, map[string]any{"event": item}, err)
	})
	register("GET /api/rooms/{id}/work-items", func(w http.ResponseWriter, r *http.Request) {
		after, _, ok := pageNumber(w, r, "after")
		if !ok {
			return
		}
		limit, _, ok := pageNumber(w, r, "limit")
		if !ok {
			return
		}
		if limit > 20 {
			writeError(w, http.StatusBadRequest, "invalid_page", "Read at most 20 work items at a time.")
			return
		}
		if len(r.URL.Query()["state"]) > 1 {
			writeError(w, 400, "invalid_page", "Choose one work state.")
			return
		}
		var page orchestration.WorkItemPage
		var err error
		if _, present := r.URL.Query()["state"]; present {
			page, err = a.store.WorkItemsFiltered(r.Context(), r.PathValue("id"), after, int(limit), r.URL.Query().Get("state"))
		} else {
			page, err = a.store.WorkItems(r.Context(), r.PathValue("id"), after, int(limit))
		}
		a.respond(w, r, page, err)
	})
	register("POST /api/rooms/{id}/work-items", func(w http.ResponseWriter, r *http.Request) {
		var in orchestration.WorkItemInput
		if !decode(w, r, &in, 128<<10) {
			return
		}
		var item orchestration.WorkItem
		var err error
		if in.Start && a.remote != nil {
			item, err = a.remote.CreateWorkItem(r.Context(), r.PathValue("id"), in)
		} else {
			item, err = a.store.CreateWorkItem(r.Context(), r.PathValue("id"), in)
		}
		a.respond(w, r, map[string]any{"item": item}, err)
	})
	register("GET /api/work-items/{id}", func(w http.ResponseWriter, r *http.Request) {
		item, err := a.store.GetWorkItem(r.Context(), r.PathValue("id"))
		a.respond(w, r, map[string]any{"item": item}, err)
	})
	register("GET /api/rooms/{id}/work-item-defaults", func(w http.ResponseWriter, r *http.Request) {
		if len(r.URL.Query()["policyId"]) > 1 {
			writeError(w, 400, "invalid_page", "Choose one automation policy.")
			return
		}
		defaults, err := a.store.WorkItemDefaults(r.Context(), r.PathValue("id"), r.URL.Query().Get("policyId"))
		a.respond(w, r, map[string]any{"defaults": defaults}, err)
	})
	operate := func(typed bool) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			var in orchestration.WorkItemOperation
			if !decode(w, r, &in, 128<<10) {
				return
			}
			if typed {
				if in.Kind != "" {
					writeError(w, 400, "invalid_work_item", "Choose the action through its typed route.")
					return
				}
				kinds := map[string]string{"start": "execute", "answer": "answer", "answer_continue": "answer_continue", "wait": "wait", "complete": "complete", "stop": "stop", "delegate": "delegate", "attach": "evidence", "follow": "follow", "unfollow": "unfollow", "authorize": "authorize"}
				var ok bool
				in.Kind, ok = kinds[r.PathValue("action")]
				if !ok {
					writeError(w, 400, "invalid_work_item", "Choose a supported matter action.")
					return
				}
			}
			var item orchestration.WorkItem
			var err error
			if a.remote != nil {
				item, err = a.remote.OperateWorkItem(r.Context(), r.PathValue("id"), in)
			} else {
				item, err = a.store.OperateWorkItem(r.Context(), r.PathValue("id"), in, storage.Budget{})
			}
			a.respond(w, r, map[string]any{"item": item}, err)
		}
	}
	register("POST /api/work-items/{id}/decisions", operate(false))
	register("POST /api/work-items/{id}/actions/{action}", operate(true))
	register("GET /api/work-item-requests/{id}", func(w http.ResponseWriter, r *http.Request) {
		item, err := a.store.WorkItemRequest(r.Context(), r.PathValue("id"))
		a.respond(w, r, map[string]any{"item": item}, err)
	})
	register("POST /api/work-item-requests/{id}/cancel", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !decode(w, r, &in, 4096) {
			return
		}
		cancelled, err := a.store.CancelWorkItemRequest(r.Context(), r.PathValue("id"))
		a.respond(w, r, map[string]any{"cancelled": cancelled}, err)
	})
}
