package webui

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

var hashedAsset = regexp.MustCompile(`^assets/.+-[A-Za-z0-9_-]{8,}\.[A-Za-z0-9]+$`)

type assetMetadata struct {
	tag  string
	size int
}

// fromFS also supports deep links without turning missing assets into HTML.
func fromFS(files fs.FS) http.Handler {
	static := http.FileServer(http.FS(files))
	var tags sync.Map
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" && r.Method != "HEAD" {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
		if name == "" {
			name = "index.html"
		}
		info, err := fs.Stat(files, name)
		if err != nil {
			if path.Ext(name) != "" {
				http.NotFound(w, r)
				return
			}
			name = "index.html"
		} else if info.IsDir() {
			static.ServeHTTP(w, r)
			return
		}
		if hashedAsset.MatchString(name) {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			w.Header().Set("Cache-Control", "no-cache")
		}
		if contentType := mime.TypeByExtension(path.Ext(name)); contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		selected := name
		if ext := path.Ext(name); ext == ".js" || ext == ".css" {
			w.Header().Set("Vary", "Accept-Encoding")
			encoding := selectEncoding(r.Header.Get("Accept-Encoding"), func(encoding string) bool {
				_, err := fs.Stat(files, name+map[string]string{"br": ".br", "gzip": ".gz"}[encoding])
				return err == nil
			})
			if encoding == "" {
				w.WriteHeader(http.StatusNotAcceptable)
				return
			}
			if encoding != "identity" {
				w.Header().Set("Content-Encoding", encoding)
				selected += map[string]string{"br": ".br", "gzip": ".gz"}[encoding]
			}
		}
		metadata, ok := tags.Load(selected)
		if !ok {
			body, err := fs.ReadFile(files, selected)
			if err != nil {
				http.NotFound(w, r)
				return
			}
			metadata = assetMetadata{tag: fmt.Sprintf(`"%x"`, sha256.Sum256(body)), size: len(body)}
			tags.Store(selected, metadata)
		}
		asset := metadata.(assetMetadata)
		w.Header().Set("ETag", asset.tag)
		if w.Header().Get("Content-Encoding") != "" {
			w.Header().Set("Content-Length", strconv.Itoa(asset.size))
		}
		copy := r.Clone(r.Context())
		copy.URL.Path = "/" + selected
		// FileServer redirects /index.html to /. Serve the root directly while
		// retaining the representation and validator chosen above.
		if selected == "index.html" {
			copy.URL.Path = "/"
		}
		static.ServeHTTP(w, copy)
	})
}

func selectEncoding(header string, available func(string) bool) string {
	quality := map[string]float64{}
	for _, part := range strings.Split(header, ",") {
		fields := strings.Split(part, ";")
		coding := strings.ToLower(strings.TrimSpace(fields[0]))
		q := 1.0
		for _, field := range fields[1:] {
			if value, ok := strings.CutPrefix(strings.TrimSpace(field), "q="); ok {
				parsed, err := strconv.ParseFloat(value, 64)
				if err != nil || parsed < 0 || parsed > 1 {
					q = 0
				} else {
					q = parsed
				}
			}
		}
		quality[coding] = q
	}
	best, bestQ := "", 0.0
	for _, coding := range []string{"br", "gzip", "identity"} {
		q, explicit := quality[coding]
		if !explicit {
			q = quality["*"]
			if coding == "identity" && q == 0 {
				if _, wildcard := quality["*"]; !wildcard {
					q = 1
				}
			}
		}
		if q > bestQ && (coding == "identity" || available(coding)) {
			best, bestQ = coding, q
		}
	}
	return best
}
