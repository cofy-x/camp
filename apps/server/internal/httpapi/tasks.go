package httpapi

import (
	"net/http"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

// Shadow the legacy input field instead of changing its wire representation.
// Summary callers fetch originals from the separately bounded input log.
type taskSummary struct {
	storage.CollaborationTask
	Inputs []storage.TaskInput `json:"inputs,omitempty"`
}

func taskView(task storage.CollaborationTask, summary bool) any {
	if summary {
		return taskSummary{CollaborationTask: task}
	}
	return task
}

func taskSummaryView(w http.ResponseWriter, r *http.Request) (bool, bool) {
	values := r.URL.Query()["view"]
	if len(values) == 0 {
		return false, true
	}
	if len(values) == 1 && values[0] == "summary" {
		return true, true
	}
	writeError(w, 400, "invalid_page", "Choose the summary view or omit view for the legacy response.")
	return false, false
}

func (a *API) taskRoutes(mux *http.ServeMux) {
	register := func(route string, h http.HandlerFunc) { mux.HandleFunc(route, a.authorizeRoute(route, h)) }
	register("GET /api/rooms/{id}/tasks", func(w http.ResponseWriter, r *http.Request) {
		summary, ok := taskSummaryView(w, r)
		if !ok {
			return
		}
		var tasks []storage.CollaborationTask
		var err error
		if summary {
			tasks, err = a.store.ListRoomTaskStates(r.Context(), r.PathValue("id"))
		} else {
			tasks, err = a.store.ListRoomTasks(r.Context(), r.PathValue("id"))
		}
		views := make([]any, 0, len(tasks))
		for _, task := range tasks {
			views = append(views, taskView(task, summary))
		}
		a.respond(w, r, map[string]any{"tasks": views}, err)
	})
	register("POST /api/rooms/{id}/tasks", func(w http.ResponseWriter, r *http.Request) {
		summary, ok := taskSummaryView(w, r)
		if !ok {
			return
		}
		var in storage.CreateTaskInput
		if !decode(w, r, &in, 64<<10) {
			return
		}
		in.PagedResponse = summary
		service := a.remote
		if service == nil {
			writeError(w, 503, "execution_unavailable", "Remote collaboration is not configured.")
			return
		}
		task, err := service.CreateTask(r.Context(), r.PathValue("id"), in)
		a.respond(w, r, map[string]any{"task": taskView(task, summary)}, err)
	})
	register("GET /api/tasks/{id}", func(w http.ResponseWriter, r *http.Request) {
		summary, ok := taskSummaryView(w, r)
		if !ok {
			return
		}
		if summary {
			task, err := a.store.GetTaskState(r.Context(), r.PathValue("id"))
			var recovery *storage.TaskRecoveryRun
			if err == nil {
				recovery, err = a.store.TaskRecoveryCandidate(r.Context(), task.ID)
			}
			a.respond(w, r, map[string]any{"task": taskView(task, true), "recoveryRun": recovery}, err)
			return
		}
		task, err := a.store.GetTask(r.Context(), r.PathValue("id"))
		if err != nil {
			a.fail(w, r, err)
			return
		}
		runs, err := a.store.ListTaskRuns(r.Context(), task.ID)
		a.respond(w, r, map[string]any{"task": task, "runs": runs}, err)
	})
	register("GET /api/tasks/{id}/inputs", func(w http.ResponseWriter, r *http.Request) {
		after, _, ok := pageNumber(w, r, "after")
		if !ok {
			return
		}
		through, present, ok := pageNumber(w, r, "through")
		if !ok {
			return
		}
		var end *int64
		if present {
			end = &through
		}
		limit, _, ok := pageNumber(w, r, "limit")
		if !ok {
			return
		}
		if limit > storage.TaskInputPageLimit {
			writeError(w, 400, "invalid_page", "Read at most 20 inputs at a time.")
			return
		}
		page, err := a.store.TaskInputsPage(r.Context(), r.PathValue("id"), after, end, int(limit))
		a.respond(w, r, page, err)
	})
	register("GET /api/tasks/{id}/runs", func(w http.ResponseWriter, r *http.Request) {
		limit, _, ok := pageNumber(w, r, "limit")
		if !ok {
			return
		}
		if limit > storage.TaskInputPageLimit || len(r.URL.Query()["cursor"]) > 1 {
			writeError(w, 400, "invalid_page", "Choose one cursor and at most 20 rounds.")
			return
		}
		page, err := a.store.TaskRunsPage(r.Context(), r.PathValue("id"), r.URL.Query().Get("cursor"), int(limit))
		a.respond(w, r, page, err)
	})
	for _, action := range []string{"inputs", "answers", "continue", "cancel"} {
		register("POST /api/tasks/{id}/"+action, func(w http.ResponseWriter, r *http.Request) {
			summary, ok := taskSummaryView(w, r)
			if !ok {
				return
			}
			var in storage.TaskOperation
			if !decode(w, r, &in, 32<<10) {
				return
			}
			in.PagedResponse = summary
			service := a.remote
			if service == nil {
				writeError(w, 503, "execution_unavailable", "Remote collaboration is not configured.")
				return
			}
			task, err := service.OperateTask(r.Context(), r.PathValue("id"), action, in)
			a.respond(w, r, map[string]any{"task": taskView(task, summary)}, err)
		})
	}
}
