package nativeexecutor

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// modelRelay holds one attempt's Server capability outside the native DSH
// sandbox. Its only route is a non-retrying POST to that frozen model endpoint.
type modelRelay struct {
	server    *http.Server
	listener  net.Listener
	transport *http.Transport
	token     string
}

func openModelRelay(ctx context.Context, baseURL, capability, caFile string) (*modelRelay, error) {
	target, err := url.Parse(baseURL)
	if err != nil || target.Host == "" || target.User != nil || target.RawQuery != "" || target.Fragment != "" || capability == "" {
		return nil, errors.New("The native model relay requires one authenticated origin and path")
	}
	ip := net.ParseIP(target.Hostname())
	if target.Scheme != "https" && (target.Scheme != "http" || target.Hostname() != "localhost" && (ip == nil || !ip.IsLoopback())) {
		return nil, errors.New("The native model relay requires HTTPS except on localhost")
	}
	target.Path = strings.TrimRight(target.Path, "/") + "/v1/messages"
	tlsConfig := &tls.Config{MinVersion: tls.VersionTLS12}
	if caFile != "" {
		data, err := os.ReadFile(caFile)
		if err != nil {
			return nil, err
		}
		roots, err := x509.SystemCertPool()
		if err != nil {
			roots = x509.NewCertPool()
		}
		if !roots.AppendCertsFromPEM(data) {
			return nil, errors.New("The native model relay CA is invalid")
		}
		tlsConfig.RootCAs = roots
	}
	transport := &http.Transport{Proxy: nil, TLSClientConfig: tlsConfig, ForceAttemptHTTP2: true, DialContext: (&net.Dialer{Timeout: 10 * time.Second, KeepAlive: 30 * time.Second}).DialContext, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 30 * time.Second, IdleConnTimeout: 30 * time.Second, MaxIdleConnsPerHost: 1}
	client := &http.Client{Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return nil, err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	relay := &modelRelay{listener: listener, transport: transport, token: hex.EncodeToString(secret[:])}
	relay.server = &http.Server{ReadHeaderTimeout: 5 * time.Second, IdleTimeout: 10 * time.Second, MaxHeaderBytes: 8 << 10, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		auth := r.Header.Get("X-Api-Key")
		if auth == "" {
			auth = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		if r.Method != http.MethodPost || r.URL.Path != "/v1/messages" || r.URL.RawQuery != "" || r.Header.Get("Origin") != "" || subtle.ConstantTimeCompare([]byte(auth), []byte(relay.token)) != 1 {
			http.Error(w, "Model capability is unavailable", http.StatusForbidden)
			return
		}
		body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 2<<20))
		if err != nil {
			http.Error(w, "Model request exceeds its limit", http.StatusRequestEntityTooLarge)
			return
		}
		request, err := http.NewRequestWithContext(r.Context(), http.MethodPost, target.String(), strings.NewReader(string(body)))
		if err != nil {
			http.Error(w, "Model request failed", http.StatusBadGateway)
			return
		}
		request.GetBody = nil // never make this POST replayable
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-Api-Key", capability)
		for _, header := range []string{"Anthropic-Version", "Anthropic-Beta", "Accept"} {
			if value := r.Header.Get(header); value != "" {
				request.Header.Set(header, value)
			}
		}
		response, err := client.Do(request)
		if err != nil {
			http.Error(w, "Model connection interrupted", http.StatusBadGateway)
			return
		}
		defer response.Body.Close()
		if response.StatusCode >= 300 && response.StatusCode < 400 {
			http.Error(w, "Model redirects are not permitted", http.StatusBadGateway)
			return
		}
		if contentType := response.Header.Get("Content-Type"); contentType != "" {
			w.Header().Set("Content-Type", contentType)
		}
		w.WriteHeader(response.StatusCode)
		controller := http.NewResponseController(w)
		buffer := make([]byte, 16<<10)
		for {
			n, readErr := response.Body.Read(buffer)
			if n > 0 {
				_ = controller.SetWriteDeadline(time.Now().Add(10 * time.Second))
				if _, err := w.Write(buffer[:n]); err != nil {
					return
				}
				_ = controller.Flush()
			}
			if readErr != nil {
				return
			}
		}
	})}
	go func() { _ = relay.server.Serve(listener) }()
	return relay, nil
}

func (r *modelRelay) endpoint() string { return "http://" + r.listener.Addr().String() }
func (r *modelRelay) close() error {
	r.transport.CloseIdleConnections()
	return r.server.Close()
}
