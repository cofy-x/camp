package daemon

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"

	"github.com/cofy-x/camp/apps/server/internal/releaseinfo"
)

type runtimeTransport func(*http.Request) (*http.Response, error)

func (f runtimeTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestPublicRuntimeRedirectResumesWithoutCampCredentials(t *testing.T) {
	payload := "fixed-runtime"
	target, _ := releaseinfo.DownloadURL("v0.1.0-alpha.1", releaseinfo.RuntimeName(strings.Repeat("a", 64)))
	requests := 0
	client := publicRuntimeClient(runtimeTransport(func(r *http.Request) (*http.Response, error) {
		requests++
		for _, name := range []string{"Authorization", "Cookie", "Origin", "Proxy-Authorization"} {
			if r.Header.Get(name) != "" {
				t.Fatal("A public release request carried credentials", name)
			}
		}
		if r.Header.Get("Range") != "bytes=5-" {
			t.Fatal("Public asset resume range changed")
		}
		if requests == 1 {
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": {"https://release-assets.githubusercontent.com/fixed?public-signature=test"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}
		return &http.Response{StatusCode: 206, Header: http.Header{"Content-Range": {fmt.Sprintf("bytes 5-%d/%d", len(payload)-1, len(payload))}}, Body: io.NopCloser(strings.NewReader(payload[5:])), ContentLength: int64(len(payload) - 5), Request: r}, nil
	}))
	file, err := os.CreateTemp(t.TempDir(), "runtime-")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	_, _ = file.WriteString(payload[:5])
	if err := downloadRuntime(context.Background(), client, target, "", int64(len(payload)), file, nil); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(file.Name())
	if string(data) != payload || requests != 2 {
		t.Fatal("Public runtime bytes changed", requests)
	}
}

func TestPublicRuntimeRejectsRedirectEscapesAndDoesNotLogSignedURLs(t *testing.T) {
	for _, location := range []string{"http://release-assets.githubusercontent.com/file", "https://github.com.evil.invalid/file", "https://user:private@github.com/file", "https://release-assets.githubusercontent.com:444/file", "https://github.com/other/release", "https://127.0.0.1/file"} {
		client := publicRuntimeClient(runtimeTransport(func(r *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: 302, Header: http.Header{"Location": {location + "?secret=signed-value"}}, Body: io.NopCloser(strings.NewReader("")), Request: r}, nil
		}))
		file, err := os.CreateTemp(t.TempDir(), "runtime-")
		if err != nil {
			t.Fatal(err)
		}
		err = downloadRuntime(context.Background(), client, "https://github.com/cofy-x/camp/releases/download/v0.1.0-alpha.1/file", "", 5, file, nil)
		_ = file.Close()
		if err == nil || strings.Contains(err.Error(), "signed-value") || strings.Contains(err.Error(), "private") {
			t.Fatal("Public redirect accepted or leaked its URL", err)
		}
	}
}
