package daemon

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"syscall"
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

func TestPublicRuntimeFailureClassificationsNeverExposeTransportSecrets(t *testing.T) {
	for _, scenario := range []struct {
		kind string
		err  error
	}{
		{"cancelled", context.Canceled},
		{"timeout", context.DeadlineExceeded},
		{"timeout", &net.OpError{Op: "read", Err: context.DeadlineExceeded}},
		{"dns", &net.DNSError{Err: "private resolver failure", Name: "secret.invalid"}},
		{"tls", &tls.CertificateVerificationError{Err: x509.UnknownAuthorityError{}}},
		{"tls", x509.HostnameError{Host: "private-provider.invalid"}},
		{"connection", &net.OpError{Op: "proxyconnect", Err: syscall.ECONNREFUSED}},
		{"redirect", errPublicRuntimeRedirect},
		{"idle_timeout", errRuntimeDownloadIdle},
		{"interrupted", io.ErrUnexpectedEOF},
		{"http", &httpError{Status: 429}},
		{"network", errors.New("proxy password secret-value")},
	} {
		t.Run(scenario.kind, func(t *testing.T) {
			client := publicRuntimeClient(runtimeTransport(func(r *http.Request) (*http.Response, error) {
				return nil, &url.Error{Op: "Get", URL: "https://proxy-user:proxy-password@release-assets.githubusercontent.com/file?signature=signed-value", Err: scenario.err}
			}))
			file, err := os.CreateTemp(t.TempDir(), "runtime-")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			err = downloadRuntime(context.Background(), client, "https://github.com/cofy-x/camp/releases/file", "", 5, file, nil)
			if err == nil || !strings.Contains(err.Error(), "["+scenario.kind+"]") || !strings.Contains(err.Error(), "same private data directory") {
				t.Fatal("Missing safe recovery classification", err)
			}
			for _, secret := range []string{"proxy-user", "proxy-password", "signed-value", "private resolver", "secret.invalid", "secret-value", "private-provider"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatal("Transport detail leaked", scenario.kind)
				}
			}
		})
	}
}

func TestPublicRuntimeResponseFailureKeepsPartialBytesAndRedactsURLs(t *testing.T) {
	for _, status := range []int{http.StatusForbidden, http.StatusOK} {
		file, err := os.CreateTemp(t.TempDir(), "runtime-")
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		client := publicRuntimeClient(runtimeTransport(func(r *http.Request) (*http.Response, error) {
			body := io.NopCloser(strings.NewReader("ab"))
			return &http.Response{StatusCode: status, Header: http.Header{}, Body: body, ContentLength: 5, Request: r}, nil
		}))
		err = downloadRuntime(context.Background(), client, "https://release-assets.githubusercontent.com/file?signature=must-not-leak", "", 5, file, nil)
		if err == nil || strings.Contains(err.Error(), "must-not-leak") {
			t.Fatal("Response failure accepted or exposed its URL", err)
		}
		info, _ := file.Stat()
		if status == http.StatusOK {
			if info.Size() != 2 || !strings.Contains(err.Error(), "[interrupted]") {
				t.Fatal("Incomplete transfer lost its resumable prefix", err, info.Size())
			}
		} else if info.Size() != 0 || !strings.Contains(err.Error(), "[http]") || !strings.Contains(err.Error(), "403") {
			t.Fatal("HTTP failure did not preserve a safe status", err)
		}
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
		if err == nil || strings.Contains(err.Error(), "signed-value") || strings.Contains(err.Error(), "user:private") {
			t.Fatal("Public redirect accepted or leaked its URL", err)
		}
	}
}
