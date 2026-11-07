package httpapi

import (
	"context"
	"mime"
	"net/http"
	"strconv"
)

type resourceArchiveReader interface {
	ResourceArchive(context.Context, string, string) ([]byte, error)
}

func (a *API) resourceArchive(w http.ResponseWriter, r *http.Request) {
	if !a.runtime(w) {
		return
	}
	commits := r.URL.Query()["commit"]
	if len(commits) != 1 || len(commits[0]) != 40 {
		writeError(w, http.StatusBadRequest, "invalid_reference", "Provide one exact saved resource revision.")
		return
	}
	reader, ok := a.rooms.(resourceArchiveReader)
	if !ok {
		writeError(w, http.StatusServiceUnavailable, "execution_unavailable", "Resource downloads are not available.")
		return
	}
	archive, err := reader.ResourceArchive(r.Context(), r.PathValue("id"), commits[0])
	if err != nil {
		a.fail(w, r, err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": "camp-files-" + commits[0][:12] + ".zip"}))
	w.Header().Set("Content-Length", strconv.Itoa(len(archive)))
	_, _ = w.Write(archive)
}
