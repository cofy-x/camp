package httpapi

import (
	"net/http"
	"os"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/httptransfer"
)

func (a *API) runnerInstallationRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/runner-installation", a.authorizeRoute("GET /api/runner-installation", func(w http.ResponseWriter, r *http.Request) {
		if service := a.remote; service != nil {
			a.write(w, 200, service.RunnerInstallation())
			return
		}
		writeError(w, 503, "execution_unavailable", "Runner installation is unavailable.")
	}))
	for _, route := range []string{"/api/runner-package", "/api/runner-package/checksum"} {
		mux.HandleFunc("GET "+route, a.authorizeRoute("GET "+route, func(w http.ResponseWriter, r *http.Request) {
			if a.remote == nil {
				writeError(w, 503, "execution_unavailable", "Runner installation is unavailable.")
				return
			}
			variant := r.URL.Query().Get("variant")
			if variant != "" && variant != "offline" {
				writeError(w, 400, "invalid_request", "Choose the installer or offline package.")
				return
			}
			name, meta, err := a.remote.RunnerPackagePath(variant == "offline")
			if err != nil {
				writeError(w, 404, "not_found", "Ask an administrator to publish this Camp Runner release.")
				return
			}
			w.Header().Set("Cache-Control", "private, no-cache")
			if route == "/api/runner-package/checksum" {
				w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				w.Header().Set("Content-Disposition", `attachment; filename="`+meta.Filename+`.sha256"`)
				_, _ = w.Write([]byte(meta.SHA256 + "  " + meta.Filename + "\n"))
				return
			}
			file, err := os.Open(name)
			if err != nil {
				a.fail(w, r, err)
				return
			}
			defer file.Close()
			st, err := file.Stat()
			if err != nil {
				a.fail(w, r, err)
				return
			}
			w.Header().Set("Content-Type", "application/gzip")
			w.Header().Set("Content-Disposition", `attachment; filename="`+meta.Filename+`"`)
			download, err := httptransfer.DownloadWriter(w, time.Minute)
			if err != nil {
				a.fail(w, r, err)
				return
			}
			http.ServeContent(download, r, meta.Filename, st.ModTime(), file)
		}))
	}
}
