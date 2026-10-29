package server

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/httpapi"
	"github.com/cofy-x/camp/apps/server/internal/storage"
)

type readinessHandler struct{ urls chan string }

func (h readinessHandler) Enabled(context.Context, slog.Level) bool { return true }
func (h readinessHandler) Handle(_ context.Context, r slog.Record) error {
	r.Attrs(func(a slog.Attr) bool {
		if a.Key == "url" {
			h.urls <- a.Value.String()
		}
		return true
	})
	return nil
}
func (h readinessHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h readinessHandler) WithGroup(string) slog.Handler      { return h }

func TestOccupiedListenerDoesNotAnnounceReadiness(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	urls := make(chan string, 1)
	dataDir := filepath.Join(t.TempDir(), "unused")
	config := Config{Addr: listener.Addr().String(), DataDir: dataDir, Logger: slog.New(readinessHandler{urls})}
	if err := Run(context.Background(), config); err == nil {
		t.Fatal("run succeeded while a different listener owns the address")
	}
	select {
	case url := <-urls:
		t.Fatalf("failed startup announced %s", url)
	default:
	}
	if _, err := os.Stat(dataDir); !os.IsNotExist(err) {
		t.Fatalf("failed listener created data directory: %v", err)
	}
	connection, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("existing listener was affected: %v", err)
	}
	connection.Close()
}

func TestRunReportsBoundAddressAndStopsOnCancellation(t *testing.T) {
	urls := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	dataDir := t.TempDir()
	go func() {
		done <- Run(ctx, Config{Addr: "127.0.0.1:0", DataDir: dataDir, Logger: slog.New(readinessHandler{urls})})
	}()
	var address string
	select {
	case address = <-urls:
	case err := <-done:
		t.Fatalf("startup failed: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("server did not start")
	}
	client := &http.Client{Timeout: 2 * time.Second}
	response, err := client.Get(address + "/api/health")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("health status %d", response.StatusCode)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not stop the server")
	}
	if response, err := client.Get(address + "/api/health"); err == nil {
		response.Body.Close()
		t.Fatal("listener survived cancellation")
	}
}

func TestCanceledRunDoesNotCreateResources(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	data := filepath.Join(t.TempDir(), "unused")
	if err := Run(ctx, Config{Addr: "127.0.0.1:0", DataDir: data}); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
	if _, err := os.Stat(data); !os.IsNotExist(err) {
		t.Fatalf("canceled run created data: %v", err)
	}
}

func TestShutdownDeadlineClosesActiveHTTPConnections(t *testing.T) {
	started, stopped := make(chan struct{}), make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
		close(stopped)
	}))
	defer s.Close()
	requestDone := make(chan struct{})
	go func() {
		defer close(requestDone)
		response, err := s.Client().Get(s.URL)
		if err == nil {
			io.Copy(io.Discard, response.Body)
			response.Body.Close()
		}
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	terminalsStopped := make(chan struct{})
	err := shutdownService(ctx, s.Config, func(context.Context) error { close(terminalsStopped); return nil }, func() {})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shutdown error %v", err)
	}
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("HTTP handler survived forced close")
	}
	select {
	case <-requestDone:
	case <-time.After(time.Second):
		t.Fatal("HTTP client survived forced close")
	}
	select {
	case <-terminalsStopped:
	default:
		t.Fatal("terminal cleanup was skipped")
	}
}

func TestRunDrainsConnectedWorkspaceStreamsBeforeShutdownDeadline(t *testing.T) {
	urls := make(chan string, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dataDir := t.TempDir()
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, Config{Addr: "127.0.0.1:0", DataDir: dataDir, Logger: slog.New(readinessHandler{urls}), ShutdownTimeout: 2 * time.Second})
	}()
	var address string
	select {
	case address = <-urls:
	case err := <-done:
		t.Fatalf("startup failed: %v", err)
	case <-time.After(10 * time.Second):
		t.Fatal("server did not start")
	}
	jar, _ := cookiejar.New(nil)
	client := &http.Client{Timeout: 5 * time.Second, Jar: jar}
	defer client.CloseIdleConnections()
	token, err := os.ReadFile(filepath.Join(dataDir, "team-setup.token"))
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]string{"token": strings.TrimSpace(string(token)), "username": "admin", "name": "Administrator", "password": "long test password"})
	setup, err := http.NewRequest("POST", address+"/api/auth/setup", strings.NewReader(string(body)))
	if err != nil {
		t.Fatal(err)
	}
	setup.Header.Set("Origin", address)
	setup.Header.Set("Content-Type", "application/json")
	response, err := client.Do(setup)
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatalf("setup status %d", response.StatusCode)
	}
	streams := make(chan error, 3)
	for range 3 {
		response, err := client.Get(address + "/api/workspace/events")
		if err != nil {
			t.Fatal(err)
		}
		if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "text/event-stream" {
			response.Body.Close()
			t.Fatalf("stream status %d", response.StatusCode)
		}
		reader := bufio.NewReader(response.Body)
		if line, err := reader.ReadString('\n'); err != nil || line != ": connected\n" {
			response.Body.Close()
			t.Fatalf("stream greeting %q: %v", line, err)
		}
		go func() {
			_, err := io.Copy(io.Discard, reader)
			response.Body.Close()
			streams <- err
		}()
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("connected SSE prevented graceful shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("server did not drain connected streams")
	}
	for range 3 {
		select {
		case err := <-streams:
			if err != nil {
				t.Fatalf("stream was force-closed instead of drained: %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("stream reader survived server shutdown")
		}
	}
}

func TestStoppingStreamsPreservesInFlightWriteGrace(t *testing.T) {
	store, err := storage.Open(filepath.Join(t.TempDir(), "desktop.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err = store.EnsureSetupToken(context.Background(), "setup-test"); err != nil {
		t.Fatal(err)
	}
	user, err := store.SetupAdmin(context.Background(), "setup-test", "owner", "Owner", strings.Repeat("x", 64), "session-test", time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano))
	if err != nil {
		t.Fatal(err)
	}
	api := httpapi.New(store, nil)
	defer api.StopStreams()
	requests, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	started, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/preferences" && r.Method == http.MethodPut {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		api.ServeHTTP(w, r.WithContext(storage.WithUser(r.Context(), user)))
	}))
	server.Config.BaseContext = func(net.Listener) context.Context { return requests }
	server.Start()
	defer server.Close()
	writeDone := make(chan error, 1)
	go func() {
		request, err := http.NewRequest("PUT", server.URL+"/api/preferences", strings.NewReader(`{"wallpaper":"solid","iconSnap":true,"controlStrip":true}`))
		if err != nil {
			writeDone <- err
			return
		}
		request.Header.Set("Content-Type", "application/json")
		response, err := server.Client().Do(request)
		if err != nil {
			writeDone <- err
			return
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		if err == nil && (response.StatusCode != http.StatusOK || !strings.Contains(string(body), `"wallpaper":"solid"`)) {
			err = errors.New("the in-flight write was not saved")
		}
		writeDone <- err
	}()
	<-started
	api.StopStreams()
	api.StopStreams()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	draining, done := make(chan struct{}), make(chan error, 1)
	go func() {
		done <- shutdownService(ctx, server.Config, func(context.Context) error { close(draining); return nil }, cancelRequests)
	}()
	<-draining
	if requests.Err() != nil {
		t.Fatal("stopping streams cancelled ordinary requests")
	}
	close(release)
	if err := <-writeDone; err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	bootstrap, err := store.Bootstrap(storage.WithUser(context.Background(), user), "test")
	if err != nil || bootstrap.Preferences.Wallpaper != "solid" {
		t.Fatal("completed request did not persist preferences", err)
	}
}
