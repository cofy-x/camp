package httpapi

import (
	"context"
	"net/http"
	"strings"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type conversationActivityReader interface {
	ConversationRuns(context.Context, string, []string) (storage.ConversationActivity, error)
}

func (a *API) conversationActivity(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	for key, values := range query {
		if len(values) != 1 || (key != "view" && key != "messageIds") {
			writeError(w, 400, "invalid_page", "Choose one visible message batch.")
			return
		}
	}
	ids := []string{}
	if value := query.Get("messageIds"); value != "" {
		ids = strings.Split(value, ",")
	}
	if len(ids) > storage.MessageWindowLimit+1 {
		writeError(w, 400, "invalid_page", "Read activity for at most 51 visible messages.")
		return
	}
	for _, id := range ids {
		if len(id) == 0 || len(id) > 128 {
			writeError(w, 400, "invalid_page", "Use valid message identities.")
			return
		}
	}
	var page storage.ConversationActivity
	var err error
	if reader, ok := a.rooms.(conversationActivityReader); ok {
		page, err = reader.ConversationRuns(r.Context(), r.PathValue("id"), ids)
	} else if _, managed := a.rooms.(runReader); managed {
		writeError(w, 503, "execution_unavailable", "Execution summaries are unavailable.")
		return
	} else {
		page, err = a.store.ConversationRuns(r.Context(), r.PathValue("id"), ids)
	}
	a.respond(w, r, page, err)
}
