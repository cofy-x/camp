package remoteruns

import (
	"net/http"
	"strconv"

	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func (m *Manager) taskInputCapability(w http.ResponseWriter, r *http.Request, runner storage.Runner) {
	if r.URL.RawQuery != "" {
		machineError(w, 400, "No query is accepted.")
		return
	}
	token, run, err := m.store.TaskReadCapability(r.Context(), r.PathValue("id"), runner.ID)
	if err != nil {
		machineError(w, 410, "Task history is no longer authorized.")
		return
	}
	sendJSON(w, 200, p.TaskInputGrant{Token: token, URL: "/api/daemon/task-inputs/" + r.PathValue("id"), Scope: p.TaskInputScope{Version: 1, After: run.TaskInputAfter, Through: run.TaskInputRevision}})
}

func (m *Manager) taskInputs(w http.ResponseWriter, r *http.Request) {
	query, err := r.URL.Query(), r.ParseForm()
	if err != nil || len(query) > 2 {
		machineError(w, 400, "Invalid input page.")
		return
	}
	for key, values := range query {
		if (key != "after" && key != "limit") || len(values) != 1 {
			machineError(w, 400, "Invalid input page.")
			return
		}
	}
	after, err := strconv.ParseInt(query.Get("after"), 10, 64)
	limit, limitErr := strconv.Atoi(query.Get("limit"))
	if err != nil || limitErr != nil {
		machineError(w, 400, "Invalid input page.")
		return
	}
	page, err := m.store.ReadAttemptTaskInputs(r.Context(), r.PathValue("id"), bearer(r), after, limit)
	if err != nil {
		machineError(w, 410, "Task history is unavailable for this capability or range.")
		return
	}
	wire := p.TaskInputPage{Items: []p.TaskInput{}, Through: page.Through, NextAfter: page.NextAfter, HasMore: page.HasMore}
	for _, item := range page.Items {
		wire.Items = append(wire.Items, p.TaskInput{Seq: item.Seq, ID: item.ID, MessageID: item.MessageID, SourceEventID: item.SourceEventID, PartIndex: item.PartIndex, AuthorKind: item.AuthorKind, ExternalActorID: item.ExternalActorID, AuthorUserID: item.AuthorUserID, AuthorName: item.AuthorName, Content: item.Content, CreatedAt: item.CreatedAt})
	}
	sendJSON(w, 200, wire)
}
