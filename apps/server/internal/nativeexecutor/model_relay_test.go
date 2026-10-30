package nativeexecutor

import (
	"context"
	"encoding/pem"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func TestNativeRelayFreezesRouteAuthorityAndNeverFollowsRedirects(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/attempt/one/v1/messages" || r.Header.Get("X-Api-Key") != "synthetic-server-capability" || r.Header.Get("Authorization") != "" {
			t.Error("relay authority or route changed")
		}
		body, _ := io.ReadAll(r.Body)
		if string(body) == "redirect" {
			w.Header().Set("Location", "https://unrelated.invalid/credentials")
			w.WriteHeader(http.StatusTemporaryRedirect)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "data: confirmed\n\n")
	}))
	defer upstream.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: upstream.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	relay, err := openModelRelay(context.Background(), upstream.URL+"/attempt/one", "synthetic-server-capability", ca)
	if err != nil {
		t.Fatal(err)
	}
	defer relay.close()
	request := func(method, path, token, body string) (int, string) {
		t.Helper()
		r, err := http.NewRequest(method, relay.endpoint()+path, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		r.Header.Set("X-Api-Key", token)
		response, err := http.DefaultClient.Do(r)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, _ := io.ReadAll(response.Body)
		return response.StatusCode, string(data)
	}
	for _, path := range []string{"/", "/v1/messages?target=https://unrelated.invalid", "/other/v1/messages"} {
		if status, _ := request("POST", path, relay.token, "{}"); status != http.StatusForbidden {
			t.Fatal("unexpected relay route", path, status)
		}
	}
	if status, _ := request("POST", "/v1/messages", "wrong", "{}"); status != http.StatusForbidden {
		t.Fatal("unscoped request admitted")
	}
	if calls.Load() != 0 {
		t.Fatal("invalid relay requests reached the Server")
	}
	status, body := request("POST", "/v1/messages", relay.token, "{}")
	if status != 200 || body != "data: confirmed\n\n" || calls.Load() != 1 {
		t.Fatal("stream changed", status, body, calls.Load())
	}
	status, body = request("POST", "/v1/messages", relay.token, "redirect")
	if status != http.StatusBadGateway || strings.Contains(body, "unrelated.invalid") || calls.Load() != 2 {
		t.Fatal("redirect escaped or request replayed", status, body, calls.Load())
	}
	for _, base := range []string{"http://remote.invalid:1", "https://user:password@host.invalid", "https://host.invalid/?forward=1"} {
		if other, err := openModelRelay(context.Background(), base, "capability", ""); err == nil {
			other.close()
			t.Fatal("unsafe upstream accepted", base)
		}
	}
	local, err := openModelRelay(context.Background(), "http://127.0.0.1:1/attempt", "local-capability", "")
	if err != nil {
		t.Fatal("the enrolled localhost origin became unusable", err)
	}
	_ = local.close()
}
