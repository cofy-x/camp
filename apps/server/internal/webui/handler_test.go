package webui

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

func TestAssetHandlerAndSPAFallback(t *testing.T) {
	h := fromFS(fstest.MapFS{"index.html": {Data: []byte("<html>Platinum</html>")}, "assets/app.js": {Data: []byte("console.log('desktop')")}})
	for _, tc := range []struct {
		path     string
		status   int
		contains string
	}{{"/", 200, "Platinum"}, {"/notes/example", 200, "Platinum"}, {"/assets/app.js", 200, "console.log"}, {"/assets/missing.js", 404, "404"}} {
		t.Run(tc.path, func(t *testing.T) {
			w := httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", tc.path, nil))
			if w.Code != tc.status || !strings.Contains(w.Body.String(), tc.contains) {
				t.Fatalf("HTTP %d: %s", w.Code, w.Body.String())
			}
		})
	}
}

func assetFixture(t *testing.T) (http.Handler, []byte, []byte) {
	t.Helper()
	body := []byte("console.log('desktop')\n")
	var compressed bytes.Buffer
	writer := gzip.NewWriter(&compressed)
	if _, err := writer.Write(body); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	return fromFS(fstest.MapFS{
		"index.html":                {Data: []byte("<html>Platinum</html>")},
		"assets/app-12345678.js":    {Data: body},
		"assets/app-12345678.js.gz": {Data: compressed.Bytes()},
		"assets/app-12345678.js.br": {Data: []byte("a distinct precompressed representation")},
	}), body, compressed.Bytes()
}

func TestCompressedAssetNegotiation(t *testing.T) {
	h, original, compressed := assetFixture(t)
	for _, tc := range []struct {
		header   string
		encoding string
		status   int
	}{
		{"", "", 200},
		{"gzip", "gzip", 200},
		{"gzip, br", "br", 200},
		{"br;q=0, gzip", "gzip", 200},
		{"gzip;q=0, br;q=0", "", 200},
		{"gzip;q=0.2, br;q=0.8, identity;q=0.1", "br", 200},
		{"identity;q=0, gzip;q=0.3", "gzip", 200},
		{"*;q=0", "", 406},
		{"gzip;q=bad, br;q=0, identity;q=0", "", 406},
	} {
		t.Run(tc.header, func(t *testing.T) {
			r := httptest.NewRequest("GET", "/assets/app-12345678.js", nil)
			r.Header.Set("Accept-Encoding", tc.header)
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.status || w.Header().Get("Content-Encoding") != tc.encoding || w.Header().Get("Vary") != "Accept-Encoding" {
				t.Fatalf("HTTP %d headers=%v", w.Code, w.Header())
			}
			if tc.status != 200 {
				return
			}
			if !strings.Contains(w.Header().Get("Content-Type"), "javascript") || w.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
				t.Fatalf("asset headers=%v", w.Header())
			}
			switch tc.encoding {
			case "":
				if !bytes.Equal(w.Body.Bytes(), original) {
					t.Fatal("identity bytes changed")
				}
			case "gzip":
				if !bytes.Equal(w.Body.Bytes(), compressed) {
					t.Fatal("not the stored gzip representation")
				}
				reader, err := gzip.NewReader(w.Body)
				if err != nil {
					t.Fatal(err)
				}
				decoded, err := io.ReadAll(reader)
				if err != nil || !bytes.Equal(decoded, original) {
					t.Fatalf("decoded %q: %v", decoded, err)
				}
				_ = reader.Close()
			case "br":
				if w.Body.String() != "a distinct precompressed representation" {
					t.Fatal("not the stored Brotli representation")
				}
			}
		})
	}
}

func TestAssetValidatorsHeadAndRange(t *testing.T) {
	h, original, compressed := assetFixture(t)
	tags := make(map[string]string)
	for _, encoding := range []string{"identity", "gzip"} {
		r := httptest.NewRequest("GET", "/assets/app-12345678.js", nil)
		r.Header.Set("Accept-Encoding", encoding)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		tags[encoding] = w.Header().Get("ETag")
		if tags[encoding] == "" {
			t.Fatal("missing representation validator")
		}
		r = httptest.NewRequest("GET", "/assets/app-12345678.js", nil)
		r.Header.Set("Accept-Encoding", encoding)
		r.Header.Set("If-None-Match", tags[encoding])
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 304 || w.Body.Len() != 0 {
			t.Fatalf("conditional GET: HTTP %d body=%q", w.Code, w.Body.String())
		}
	}
	if tags["identity"] == tags["gzip"] {
		t.Fatal("identity and compressed validators must differ")
	}
	r := httptest.NewRequest("HEAD", "/assets/app-12345678.js", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("Content-Length") != strconv.Itoa(len(compressed)) || w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("HEAD: HTTP %d headers=%v body=%q", w.Code, w.Header(), w.Body.String())
	}
	r = httptest.NewRequest("GET", "/assets/app-12345678.js", nil)
	r.Header.Set("Range", "bytes=0-3")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 206 || !bytes.Equal(w.Body.Bytes(), original[:4]) {
		t.Fatalf("Range: HTTP %d body=%q", w.Code, w.Body.String())
	}
}

func TestHTMLRevalidatesAcrossSPARoutes(t *testing.T) {
	h, _, _ := assetFixture(t)
	var tag string
	for _, target := range []string{"/", "/notes/example"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", target, nil))
		if w.Code != 200 || w.Header().Get("Cache-Control") != "no-cache" || w.Header().Get("ETag") == "" || w.Header().Get("Content-Encoding") != "" {
			t.Fatalf("HTML: HTTP %d headers=%v", w.Code, w.Header())
		}
		if tag != "" && tag != w.Header().Get("ETag") {
			t.Fatal("SPA routes must validate the same index")
		}
		tag = w.Header().Get("ETag")
	}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("If-None-Match", tag)
	h.ServeHTTP(w, r)
	if w.Code != 304 {
		t.Fatalf("HTML conditional GET: %d", w.Code)
	}
}
