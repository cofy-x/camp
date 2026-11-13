package httpapi

import (
	"net/http"
	"net/url"

	"github.com/cofy-x/camp/apps/server/internal/storage"
)

func (a *API) reviewRoutes(mux *http.ServeMux) {
	register := func(route string, h http.HandlerFunc) { mux.HandleFunc(route, a.authorizeRoute(route, h)) }
	register("GET /api/reviews", func(w http.ResponseWriter, r *http.Request) {
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			writeError(w, 400, "invalid_page", "Use valid review page parameters.")
			return
		}
		for key, values := range query {
			if (key != "roomId" && key != "resourceId" && key != "path" && key != "cursor" && key != "limit") || len(values) != 1 {
				writeError(w, 400, "invalid_page", "Use one Room, source, file, cursor and page limit.")
				return
			}
		}
		limit, present, ok := pageNumber(w, r, "limit")
		if !ok {
			return
		}
		if (present && limit == 0) || limit > 20 {
			writeError(w, 400, "invalid_page", "Read at most 20 reviews at a time.")
			return
		}
		page, err := a.store.ListReviews(r.Context(), storage.ReviewListOptions{RoomID: query.Get("roomId"), ResourceID: query.Get("resourceId"), Path: query.Get("path"), Cursor: query.Get("cursor"), Limit: int(limit)})
		a.respond(w, r, page, err)
	})
	register("GET /api/reviews/{id}", func(w http.ResponseWriter, r *http.Request) {
		view, ok := reviewReadView(w, r)
		if !ok {
			return
		}
		result, err := a.store.GetReview(r.Context(), r.PathValue("id"), view)
		a.respond(w, r, reviewResponse(result, view), err)
	})
	register("GET /api/reviews/{id}/events", func(w http.ResponseWriter, r *http.Request) {
		query, err := url.ParseQuery(r.URL.RawQuery)
		if err != nil {
			writeError(w, 400, "invalid_page", "Use valid review history parameters.")
			return
		}
		for key, values := range query {
			if (key != "through" && key != "cursor" && key != "limit") || len(values) != 1 {
				writeError(w, 400, "invalid_page", "Use one review revision, cursor and page limit.")
				return
			}
		}
		through, present, ok := pageNumber(w, r, "through")
		if !ok {
			return
		}
		if present && through == 0 {
			writeError(w, 400, "invalid_page", "Choose a retained review revision.")
			return
		}
		limit, present, ok := pageNumber(w, r, "limit")
		if !ok {
			return
		}
		if (present && limit == 0) || limit > 20 {
			writeError(w, 400, "invalid_page", "Read at most 20 review events at a time.")
			return
		}
		page, err := a.store.ReviewEvents(r.Context(), r.PathValue("id"), storage.ReviewEventOptions{Through: through, Cursor: query.Get("cursor"), Limit: int(limit)})
		a.respond(w, r, page, err)
	})
	register("POST /api/rooms/{id}/reviews", func(w http.ResponseWriter, r *http.Request) {
		view, ok := reviewReadView(w, r)
		if !ok {
			return
		}
		var in storage.CreateReviewInput
		if !decode(w, r, &in, 8192) {
			return
		}
		if previous, found, err := a.store.FindCreateReview(r.Context(), r.PathValue("id"), in, view); err != nil {
			a.fail(w, r, err)
			return
		} else if found {
			a.write(w, 200, reviewResponse(previous, view))
			return
		}
		if !a.reviewFile(w, r, in.ResourceID, in.Path, in.Commit) {
			return
		}
		result, err := a.store.CreateReview(r.Context(), r.PathValue("id"), in, view)
		a.respond(w, r, reviewResponse(result, view), err)
	})
	register("POST /api/reviews/{id}/actions", func(w http.ResponseWriter, r *http.Request) {
		view, ok := reviewReadView(w, r)
		if !ok {
			return
		}
		var in storage.ReviewActionInput
		if !decode(w, r, &in, 16384) {
			return
		}
		if previous, found, err := a.store.FindReviewAction(r.Context(), r.PathValue("id"), in, view); err != nil {
			a.fail(w, r, err)
			return
		} else if found {
			a.write(w, 200, reviewResponse(previous, view))
			return
		}
		if in.Action == "resubmit" {
			current, err := a.store.GetReview(r.Context(), r.PathValue("id"), storage.ReviewSummary)
			if err != nil {
				a.fail(w, r, err)
				return
			}
			if !a.reviewFile(w, r, current.Review.ResourceID, current.Review.Path, in.Commit) {
				return
			}
		}
		result, err := a.store.ActOnReview(r.Context(), r.PathValue("id"), in, view)
		a.respond(w, r, reviewResponse(result, view), err)
	})
}

func reviewReadView(w http.ResponseWriter, r *http.Request) (storage.ReviewView, bool) {
	query, err := url.ParseQuery(r.URL.RawQuery)
	if err == nil {
		valid := true
		for key, values := range query {
			if key != "view" || len(values) != 1 {
				valid = false
			}
		}
		if valid && (!query.Has("view") || query.Get("view") == "summary") {
			return storage.ReviewView(query.Get("view")), true
		}
	}
	writeError(w, 400, "invalid_view", "Use view=summary or omit the view for the complete audit result.")
	return "", false
}

func reviewResponse(result storage.ReviewResult, view storage.ReviewView) any {
	if view != storage.ReviewSummary {
		return result
	}
	return struct {
		Review   storage.Review       `json:"review"`
		Feedback *storage.ReviewEvent `json:"feedback,omitempty"`
		Replayed bool                 `json:"replayed"`
	}{result.Review, result.Feedback, result.Replayed}
}

// Git versions are immutable, so byte existence can be checked before the SQLite
// transaction. Admission repeats resource identity and current-version checks.
func (a *API) reviewFile(w http.ResponseWriter, r *http.Request, resource, path, commit string) bool {
	if !a.runtime(w) {
		return false
	}
	if _, err := a.store.WorkingResource(r.Context(), resource); err != nil {
		a.fail(w, r, err)
		return false
	}
	if _, err := a.rooms.ResourceFile(r.Context(), resource, path, commit); err != nil {
		a.fail(w, r, err)
		return false
	}
	return true
}
