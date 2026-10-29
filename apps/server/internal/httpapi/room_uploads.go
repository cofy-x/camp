package httpapi

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"

	"github.com/cofy-x/camp/apps/server/internal/resources"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type roomUploader interface {
	Upload(context.Context, string, string, string, []byte) (storage.Reference, error)
}

func (a *API) roomUploads(w http.ResponseWriter, r *http.Request) {
	if len(r.URL.Query()["before"]) > 1 {
		writeError(w, 400, "invalid_upload", "Provide one attachment cursor.")
		return
	}
	refs, next, err := a.store.ListRoomUploads(r.Context(), r.PathValue("id"), r.URL.Query().Get("before"))
	a.respond(w, r, map[string]any{"references": refs, "nextCursor": next}, err)
}

func (a *API) roomUpload(w http.ResponseWriter, r *http.Request) {
	receipt, err := a.store.GetRoomUpload(r.Context(), r.PathValue("id"), r.PathValue("requestId"))
	a.respond(w, r, map[string]any{"reference": receipt.Reference}, err)
}

// Stream exactly two bounded parts into memory, without multipart spill files.
// Validate the raw filename: multipart.Part.FileName would silently strip paths.
func (a *API) uploadRoomFile(w http.ResponseWriter, r *http.Request) {
	uploader, ok := a.rooms.(roomUploader)
	if !ok {
		writeError(w, 503, "execution_unavailable", "File uploads are not configured.")
		return
	}
	const maxBytes = resources.MaxFileBytes + 32<<10
	if r.ContentLength > maxBytes {
		writeError(w, 413, "body_too_large", "Attachments must be no larger than 8 MiB.")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	reader, err := r.MultipartReader()
	if err != nil {
		writeError(w, 400, "invalid_upload", "Use multipart/form-data with one requestId and one file.")
		return
	}
	var requestID, name string
	var content []byte
	seen := map[string]bool{}
	for {
		part, err := reader.NextRawPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			uploadReadError(w, err)
			return
		}
		kind, fields, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
		field := fields["name"]
		filename, hasFilename := fields["filename"]
		if err != nil || kind != "form-data" || seen[field] || (field != "requestId" && field != "file") || part.Header.Get("Content-Transfer-Encoding") != "" || field == "requestId" && hasFilename || field == "file" && !hasFilename {
			writeError(w, 400, "invalid_upload", "Send exactly one requestId field and one unencoded file part.")
			return
		}
		seen[field] = true
		limit := resources.MaxFileBytes
		if field == "requestId" {
			limit = 128
		} else if err := resources.ValidateUploadName(filename); err != nil {
			a.fail(w, r, err)
			return
		}
		data, err := io.ReadAll(io.LimitReader(part, int64(limit)+1))
		if err != nil {
			uploadReadError(w, err)
			return
		}
		if len(data) > limit {
			if field == "file" {
				writeError(w, 413, "file_too_large", "Attachments must be no larger than 8 MiB.")
			} else {
				writeError(w, 400, "invalid_upload", "The upload request ID exceeds 128 UTF-8 bytes.")
			}
			return
		}
		_ = part.Close()
		if field == "requestId" {
			requestID = string(data)
		} else {
			name, content = filename, data
		}
	}
	if !seen["requestId"] || !seen["file"] {
		writeError(w, 400, "invalid_upload", "Choose one file and provide an upload request ID.")
		return
	}
	reference, err := uploader.Upload(r.Context(), r.PathValue("id"), requestID, name, content)
	a.respond(w, r, map[string]any{"reference": reference}, err)
}

func uploadReadError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "The upload is too large.")
		return
	}
	writeError(w, http.StatusBadRequest, "invalid_upload", "The upload could not be read.")
}
