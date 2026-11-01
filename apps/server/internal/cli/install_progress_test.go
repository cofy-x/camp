package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/daemon"
)

func offlineProgressFixture(t *testing.T) (daemon.Config, runtimePin, string) {
	t.Helper()
	archive, pin := runtimeArchiveFixture(t)
	root := t.TempDir()
	offline := filepath.Join(root, "offline.tar.gz")
	if err := os.WriteFile(offline, archive, 0600); err != nil {
		t.Fatal(err)
	}
	for name, value := range map[string]string{"daemon.json": "private-identity-sentinel", "journal-sentinel": "retained-work"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte(value), 0600); err != nil {
			t.Fatal(err)
		}
	}
	return daemon.Config{DataDir: root, ServerURL: "https://must-not-contact.invalid", Credential: "private-credential-sentinel"}, pin, offline
}
func assertProgressIdentity(t *testing.T, config daemon.Config) {
	t.Helper()
	for name, want := range map[string]string{"daemon.json": "private-identity-sentinel", "journal-sentinel": "retained-work"} {
		got, err := os.ReadFile(filepath.Join(config.DataDir, name))
		if err != nil || string(got) != want {
			t.Fatal("installation changed private identity/work", err)
		}
	}
}

func TestRuntimeProgressAnnouncesEachPhaseAndReusesVerifiedCache(t *testing.T) {
	config, pin, offline := offlineProgressFixture(t)
	var output bytes.Buffer
	installed, err := installRuntimePackage(t.Context(), config, pin, &output, offline)
	if err != nil || installed.directory != pin.cache(config.DataDir) || installed.manifest.Role != "runner-runtime" {
		t.Fatal(installed, err)
	}
	previous := -1
	for _, phase := range []string{"Copying included offline runtime…", "Verifying runtime archive…", "Validating and extracting runtime archive…", "Validating fixed runtime files…", "Committing verified runtime…"} {
		index := strings.Index(output.String(), phase)
		if index <= previous {
			t.Fatalf("missing/out-of-order phase %q:\n%s", phase, output.String())
		}
		previous = index
	}
	for _, detail := range []string{"compressed download", "unpacked files", "Download completion does not finish setup", "elapsed", "entries processed", "Setup continues with host preparation and self-test"} {
		if !strings.Contains(output.String(), detail) {
			t.Error("missing progress detail", detail)
		}
	}
	for _, private := range []string{config.DataDir, config.Credential, config.ServerURL, "Setup complete."} {
		if strings.Contains(output.String(), private) {
			t.Error("private/premature completion output", private)
		}
	}
	output.Reset()
	cached, err := installRuntimePackage(t.Context(), config, pin, &output, "absent")
	if err != nil || cached.manifest.Role != installed.manifest.Role {
		t.Fatal(cached, err)
	}
	if !strings.Contains(output.String(), "Checking installed runtime cache…") || !strings.Contains(output.String(), "No download or extraction is needed") {
		t.Fatal("cache reuse is unexplained", output.String())
	}
	if strings.Contains(output.String(), "Verifying runtime archive…") || strings.Contains(output.String(), "Validating and extracting runtime archive…") {
		t.Fatal("cache reinstalled", output.String())
	}
	assertProgressIdentity(t, config)
}

type cancelPhaseWriter struct {
	output bytes.Buffer
	label  string
	cancel context.CancelFunc
}

func (w *cancelPhaseWriter) Write(data []byte) (int, error) {
	if strings.Contains(string(data), w.label+"…") {
		w.cancel()
	}
	return w.output.Write(data)
}

func TestCancelledExtractionRetainsIdentityAndVerifiedTransferForRetry(t *testing.T) {
	config, pin, offline := offlineProgressFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	output := &cancelPhaseWriter{label: "Validating and extracting runtime archive", cancel: cancel}
	if _, err := installRuntimePackage(ctx, config, pin, output, offline); !errors.Is(err, context.Canceled) {
		t.Fatal("expected cancellation", err)
	}
	if _, err := os.Stat(pin.cache(config.DataDir)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled extraction became installed", err)
	}
	stages, err := filepath.Glob(filepath.Join(config.DataDir, "runtime", "cache", ".install-*"))
	if err != nil || len(stages) != 0 {
		t.Fatal("owned extraction stage leaked", stages, err)
	}
	archivePath := filepath.Join(config.DataDir, "runtime", "downloads", pin.SHA256+".part")
	if info, err := os.Stat(archivePath); err != nil || info.Size() != pin.Bytes {
		t.Fatal("complete transfer was discarded", err)
	}
	var retried bytes.Buffer
	if _, err := installRuntimePackage(t.Context(), config, pin, &retried, "absent-offline"); err != nil {
		t.Fatal("saved transfer could not resume without networking", err)
	}
	if !strings.Contains(retried.String(), "Using the saved runtime download") {
		t.Fatal(retried.String())
	}
	assertProgressIdentity(t, config)
}

func TestCancellingCacheValidationDoesNotQuarantineValidRuntime(t *testing.T) {
	config, pin, offline := offlineProgressFixture(t)
	if _, err := installRuntimePackage(t.Context(), config, pin, io.Discard, offline); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	output := &cancelPhaseWriter{label: "Checking installed runtime cache", cancel: cancel}
	if _, err := installRuntimePackage(ctx, config, pin, output, offline); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := verifyPinnedRuntime(pin.cache(config.DataDir), pin); err != nil {
		t.Fatal("cancelled check modified a valid cache", err)
	}
	invalid, err := filepath.Glob(pin.cache(config.DataDir) + ".invalid-*")
	if err != nil || len(invalid) != 0 {
		t.Fatal("valid cache quarantined", invalid, err)
	}
	assertProgressIdentity(t, config)
}

type notifyingProgress struct {
	mu      sync.Mutex
	data    bytes.Buffer
	working chan struct{}
	once    sync.Once
}

func (w *notifyingProgress) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	n, err := w.data.Write(data)
	if bytes.Contains(data, []byte("\n")) && strings.Contains(w.data.String(), "working") {
		w.once.Do(func() { close(w.working) })
	}
	return n, err
}
func (w *notifyingProgress) String() string { w.mu.Lock(); defer w.mu.Unlock(); return w.data.String() }

func TestInstallPhaseReportsBoundedActivityAndJoinsItsTicker(t *testing.T) {
	output := &notifyingProgress{working: make(chan struct{})}
	ticks, release, started := make(chan time.Time), make(chan struct{}), make(chan struct{})
	result := make(chan error, 1)
	failure := errors.New("private/signed/url?credential=must-not-print")
	go func() {
		result <- installationPhaseTicks(t.Context(), output, "Synthetic phase", 16384, ticks, func(progress *installPhase) error {
			progress.advance(8192, 3)
			close(started)
			<-release
			return failure
		})
	}()
	<-started
	ticks <- time.Now()
	<-output.working
	close(release)
	if err := <-result; !errors.Is(err, failure) {
		t.Fatal(err)
	}
	text := output.String()
	for _, value := range []string{"Synthetic phase…", "working", "8.0 KiB / 16.0 KiB", "3 entries processed", "elapsed", "failed"} {
		if !strings.Contains(text, value) {
			t.Error("missing heartbeat detail", value)
		}
	}
	if strings.Contains(text, "credential") || strings.Contains(text, "complete") {
		t.Fatal("error details or false success in progress", text)
	}
	select {
	case ticks <- time.Now():
		t.Fatal("phase returned with a live ticker consumer")
	default:
	}
}

func TestRuntimeProgressExplainsPartialTransferAndRequestsOnlyRemainingBytes(t *testing.T) {
	archive, pin := runtimeArchiveFixture(t)
	root := t.TempDir()
	offset := len(archive) / 2
	downloads := filepath.Join(root, "runtime", "downloads")
	if err := os.MkdirAll(downloads, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(downloads, pin.SHA256+".part"), archive[:offset], 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Range") != fmt.Sprintf("bytes=%d-", offset) || r.Header.Get("Authorization") != "Bearer private-credential" {
			t.Error("resume identity/range changed")
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, len(archive)-1, len(archive)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(archive[offset:])
	}))
	defer server.Close()
	var output bytes.Buffer
	if _, err := installRuntimePackage(t.Context(), daemon.Config{DataDir: root, ServerURL: server.URL, Credential: "private-credential"}, pin, &output, "absent"); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 || !strings.Contains(output.String(), "Resuming the saved runtime transfer") {
		t.Fatal("partial transfer was not explained/reused", calls.Load(), output.String())
	}
	if strings.Contains(output.String(), server.URL) || strings.Contains(output.String(), "private-credential") {
		t.Fatal("transfer details leaked", output.String())
	}
}
