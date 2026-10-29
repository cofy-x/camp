package daemon

import (
	"bytes"
	"context"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/cofy-x/camp/apps/server/internal/nativeexecutor"
	p "github.com/cofy-x/camp/apps/server/internal/runnerprotocol"
)

func runtimeDownloadConfig(t *testing.T, server *httptest.Server) Config {
	t.Helper()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	return Config{ServerURL: server.URL, Credential: "private-runner-credential", CAFile: ca}
}

func TestUnavailablePinnedRuntimeConnectsWithoutOpeningNativeCode(t *testing.T) {
	hello := make(chan p.Message, 1)
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Error(err)
			return
		}
		defer connection.CloseNow()
		var message p.Message
		if err := wsjson.Read(r.Context(), connection, &message); err != nil {
			return
		}
		hello <- message
		_ = wsjson.Write(r.Context(), connection, p.Message{Type: "welcome", Version: p.Version, RunnerID: "unavailable", Epoch: 1})
		for {
			if wsjson.Read(r.Context(), connection, &message) != nil {
				return
			}
		}
	}))
	defer server.Close()
	config := runtimeDownloadConfig(t, server)
	config.DataDir = t.TempDir()
	config.RunnerID = "unavailable"
	config.StorageID = "stored"
	config.Executor = "native"
	config.Slots = 1
	config.NativeUnavailable = "Fixed runtime failed integrity verification; repeat setup"
	marker := filepath.Join(config.DataDir, "must-not-run")
	binary := filepath.Join(config.DataDir, "untrusted-node")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	config.Native = nativeexecutor.Config{NodePath: binary, RuntimeDir: config.DataDir, LayerFSRuntimeDir: config.DataDir, SandboxHelper: binary}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Run(ctx, config, io.Discard) }()
	select {
	case message := <-hello:
		if message.Hello == nil || message.Hello.Capabilities.Ready || !strings.Contains(message.Hello.Capabilities.Reason, "integrity") {
			t.Fatal("Unavailable runtime was not reported accurately")
		}
		if message.Hello.Capabilities.Readiness == nil || message.Hello.Capabilities.Readiness.Ready {
			t.Fatal("Unavailable runtime retained a successful self-test report")
		}
	case err := <-done:
		t.Fatal("Runner stopped before reporting unavailable runtime", err)
	case <-ctx.Done():
		t.Fatal("Runner did not connect")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Runner did not stop")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("Rejected native bytes were executed")
	}
}

func TestRuntimeDownloadUsesMachineTLSAndResumesExactRange(t *testing.T) {
	payload := []byte("immutable-pinned-runtime")
	digest := strings.Repeat("1", 64)
	requests := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/api/daemon/runtime/"+digest || r.Header.Get("Authorization") != "Bearer private-runner-credential" || r.Header.Get("Cookie") != "" || r.Header.Get("Origin") != "" {
			t.Error("Runtime download crossed authentication boundaries")
		}
		if r.Header.Get("Range") != "bytes=5-" {
			t.Error("Partial runtime was not resumed")
		}
		w.Header().Set("Content-Range", fmt.Sprintf("bytes 5-%d/%d", len(payload)-1, len(payload)))
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(payload[5:])
	}))
	defer server.Close()
	config := runtimeDownloadConfig(t, server)
	file, err := os.CreateTemp(t.TempDir(), "runtime-")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	_, _ = file.Write(payload[:5])
	if err := DownloadRuntime(context.Background(), config, digest, int64(len(payload)), file, nil); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(file.Name())
	if err != nil || !bytes.Equal(got, payload) {
		t.Fatal("Resumed archive differs", err)
	}
	if err := DownloadRuntime(context.Background(), config, digest, int64(len(payload)), file, nil); err != nil || requests != 1 {
		t.Fatal("Complete archive was downloaded again", requests, err)
	}
}

func TestRuntimeDownloadRejectsRedirectRangeAndUntrustedTLS(t *testing.T) {
	for _, scenario := range []string{"redirect", "range", "untrusted", "size"} {
		t.Run(scenario, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch scenario {
				case "redirect":
					http.Redirect(w, r, "https://another-origin.invalid/runtime", http.StatusFound)
				case "range":
					w.Header().Set("Content-Range", "bytes 2-4/5")
					w.WriteHeader(http.StatusPartialContent)
					_, _ = w.Write([]byte("bad"))
				default:
					_, _ = w.Write([]byte("wrong-size"))
				}
			}))
			defer server.Close()
			config := runtimeDownloadConfig(t, server)
			if scenario == "untrusted" {
				config.CAFile = ""
			}
			file, err := os.CreateTemp(t.TempDir(), "runtime-")
			if err != nil {
				t.Fatal(err)
			}
			defer file.Close()
			if err := DownloadRuntime(context.Background(), config, strings.Repeat("1", 64), 5, file, nil); err == nil {
				t.Fatal("Invalid runtime transfer was accepted")
			}
			info, _ := file.Stat()
			if info.Size() != 0 {
				t.Fatal("Rejected transfer wrote bytes")
			}
		})
	}
}
