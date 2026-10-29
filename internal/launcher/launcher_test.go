package launcher

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func packageBytes(t *testing.T, headers ...tar.Header) []byte {
	t.Helper()
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	tw := tar.NewWriter(gz)
	for _, h := range headers {
		if err := tw.WriteHeader(&h); err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			if _, err := tw.Write(bytes.Repeat([]byte("x"), int(h.Size))); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return data.Bytes()
}

func fixture(t *testing.T, payload []byte) (Options, *atomic.Int64) {
	t.Helper()
	var requests atomic.Int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("release request contains credentials")
		}
		if strings.HasSuffix(r.URL.Path, ".sha256") {
			_, _ = fmt.Fprintf(w, "%x  camp-server-linux-amd64.tar.gz\n", sha256.Sum256(payload))
		} else {
			_, _ = w.Write(payload)
		}
	}))
	t.Cleanup(server.Close)
	return Options{Version: "v0.1.0-alpha.2", Role: "server", OS: "linux", Arch: "amd64", CacheDir: filepath.Join(t.TempDir(), "new-user-cache"), base: server.URL + "/", client: server.Client()}, &requests
}

func validPackage(t *testing.T) []byte {
	return packageBytes(t,
		tar.Header{Name: "camp-server-linux-amd64/camp", Typeflag: tar.TypeReg, Mode: 0755, Size: 5},
		tar.Header{Name: "camp-server-linux-amd64/licenses/Camp-LICENSE", Typeflag: tar.TypeReg, Mode: 0644, Size: 2},
	)
}

func TestConcurrentInstallationCacheAndRepair(t *testing.T) {
	o, requests := fixture(t, validPackage(t))
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			if _, err := Ensure(t.Context(), o); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if requests.Load() != 2 {
		t.Fatalf("concurrent installation fetched %d times", requests.Load())
	}
	binary, err := Ensure(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(binary), "licenses", "Camp-LICENSE")); err != nil {
		t.Fatal("dependency licenses were not retained", err)
	}
	if err := os.WriteFile(binary, []byte("changed"), 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 4 {
		t.Fatalf("damaged cache was not repaired: requests=%d", requests.Load())
	}
	if err := os.Chmod(binary, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(t.Context(), o); err != nil {
		t.Fatal(err)
	}
	if requests.Load() != 6 {
		t.Fatal("nonexecutable cache was not repaired")
	}
}

func TestUnsafeArchivesNeverPublish(t *testing.T) {
	for _, h := range []tar.Header{
		{Name: "camp-server-linux-amd64/../escape", Typeflag: tar.TypeReg, Size: 1},
		{Name: "camp-server-linux-amd64/camp", Typeflag: tar.TypeSymlink, Linkname: "/bin/sh"},
		{Name: "camp-server-linux-amd64/camp", Typeflag: tar.TypeLink, Linkname: "/bin/sh"},
		{Name: "camp-server-linux-amd64/camp", Typeflag: tar.TypeFifo},
		{Name: "camp-server-linux-amd64/.camp-install.json", Typeflag: tar.TypeReg, Size: 1},
		{Name: "another-root/camp", Typeflag: tar.TypeReg, Size: 1},
	} {
		t.Run(h.Name+string(h.Typeflag), func(t *testing.T) {
			o, _ := fixture(t, packageBytes(t, h))
			if _, err := Ensure(t.Context(), o); err == nil {
				t.Fatal("unsafe package accepted")
			}
			destination := filepath.Join(o.CacheDir, "camp", "releases", o.Version, "camp-server-linux-amd64")
			if _, err := os.Lstat(destination); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed install published a cache", err)
			}
		})
	}
}

func TestChecksumFailureAndInterruptedTransferPreservePreviousCache(t *testing.T) {
	o, _ := fixture(t, validPackage(t))
	binary, err := Ensure(t.Context(), o)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("retained damaged bytes"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, scenario := range []string{"checksum", "interrupted", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if scenario == "missing" {
					http.NotFound(w, r)
				} else if strings.HasSuffix(r.URL.Path, ".sha256") {
					_, _ = fmt.Fprintf(w, "%s  camp-server-linux-amd64.tar.gz\n", strings.Repeat("0", 64))
				} else {
					if scenario == "interrupted" {
						w.Header().Set("Content-Length", "9999")
					}
					_, _ = w.Write([]byte("wrong or incomplete bytes"))
				}
			}))
			defer server.Close()
			broken := o
			broken.base, broken.client = server.URL+"/", server.Client()
			if _, err := Ensure(t.Context(), broken); err == nil {
				t.Fatal("broken transfer accepted")
			}
			data, err := os.ReadFile(binary)
			if err != nil || string(data) != "retained damaged bytes" {
				t.Fatal("failed repair discarded old installation", err)
			}
		})
	}
	if _, err := Ensure(t.Context(), o); err != nil {
		t.Fatal("same-version retry failed", err)
	}
}

func TestPlatformVersionAndCacheBoundaries(t *testing.T) {
	o, requests := fixture(t, validPackage(t))
	for _, version := range []string{"dev", "latest", "v1.0.0/../../escape", "v0.1.0-rc.01"} {
		bad := o
		bad.Version = version
		if _, err := Ensure(t.Context(), bad); err == nil {
			t.Fatal("invalid version accepted", version)
		}
	}
	for _, platform := range [][3]string{{"runner", "darwin", "arm64"}, {"server", "windows", "amd64"}, {"server", "linux", "arm64"}} {
		bad := o
		bad.Role, bad.OS, bad.Arch = platform[0], platform[1], platform[2]
		if _, err := Ensure(t.Context(), bad); err == nil {
			t.Fatal("unsupported platform accepted", platform)
		}
	}
	if requests.Load() != 0 {
		t.Fatal("unsupported request reached network")
	}
	if err := os.MkdirAll(o.CacheDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(t.TempDir(), filepath.Join(o.CacheDir, "camp")); err != nil {
		t.Fatal(err)
	}
	if _, err := Ensure(t.Context(), o); err == nil {
		t.Fatal("symlinked private cache accepted")
	}
}

func TestCancellationAndRedirectPolicy(t *testing.T) {
	o, _ := fixture(t, validPackage(t))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := Ensure(ctx, o); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
	for _, target := range []string{"http://github.com/file", "https://evil.example/file", "https://token@github.com/file", "https://github.com:8443/file"} {
		u, _ := url.Parse(target)
		if publicClient().CheckRedirect(&http.Request{URL: u}, nil) == nil {
			t.Fatal("unsafe redirect accepted", target)
		}
	}
	u, _ := url.Parse("https://release-assets.githubusercontent.com/fixed-asset")
	if err := publicClient().CheckRedirect(&http.Request{URL: u}, nil); err != nil {
		t.Fatal("GitHub asset redirect rejected", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	defer server.Close()
	ctx, cancel = context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	var out bytes.Buffer
	if err := download(ctx, server.Client(), server.URL, &out, 100); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("in-flight cancellation lost", err)
	}
}
