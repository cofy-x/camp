package httpapi

import (
	"github.com/cofy-x/camp/apps/server/internal/storage"
	"net/http"
)

func (a *API) taskRoutes(mux *http.ServeMux) {
	register := func(route string, h http.HandlerFunc) { mux.HandleFunc(route, a.authorizeRoute(route, h)) }
	register("GET /api/rooms/{id}/tasks", func(w http.ResponseWriter, r *http.Request) {
		tasks, e := a.store.ListRoomTasks(r.Context(), r.PathValue("id"))
		a.respond(w, r, map[string]any{"tasks": tasks}, e)
	})
	register("POST /api/rooms/{id}/tasks", func(w http.ResponseWriter, r *http.Request) {
		var in storage.CreateTaskInput
		if !decode(w, r, &in, 64<<10) {
			return
		}
		service := a.remote
		if service == nil {
			writeError(w, 503, "execution_unavailable", "Remote collaboration is not configured.")
			return
		}
		task, e := service.CreateTask(r.Context(), r.PathValue("id"), in)
		a.respond(w, r, map[string]any{"task": task}, e)
	})
	register("GET /api/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		task, e := a.store.GetTask(r.Context(), r.PathValue("id"))
		if e != nil {
			a.fail(w, r, e)
			return
		}
		runs, e := a.store.ListTaskRuns(r.Context(), task.ID)
		a.respond(w, r, map[string]any{"task": task, "runs": runs}, e)
	})
	for _, action := range []string{"inputs", "answers", "continue", "cancel"} {
		register("POST /api/tasks/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			var in storage.TaskOperation
			if !decode(w, r, &in, 32<<10) {
				return
			}
			service := a.remote
			if service == nil {
				writeError(w, 503, "execution_unavailable", "Remote collaboration is not configured.")
				return
			}
			task, e := service.OperateTask(r.Context(), r.PathValue("id"), action, in)
			a.respond(w, r, map[string]any{"task": task}, e)
		})
	}
}
