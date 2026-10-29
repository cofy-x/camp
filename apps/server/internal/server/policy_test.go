package server

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHostAndOriginPolicy(t *testing.T) {
	for _, tc := range []struct {
		name, addr, host, origin, fetchSite, method string
		allowed, trusted                            []string
		want                                        int
	}{
		{name: "local read", host: "localhost:3001", method: "GET", want: 204},
		{name: "local IPv4", host: "127.0.0.2:3001", method: "GET", want: 204},
		{name: "local IPv6", host: "[::1]:3001", method: "GET", want: 204},
		{name: "untrusted host read", host: "attacker.example:3001", method: "GET", want: 403},
		{name: "untrusted same origin", host: "attacker.example:3001", origin: "http://attacker.example:3001", method: "POST", want: 403},
		{name: "same origin", host: "localhost:3001", origin: "http://localhost:3001", fetchSite: "same-origin", method: "POST", want: 204},
		{name: "Vite proxy", host: "127.0.0.1:5173", origin: "http://127.0.0.1:5173", fetchSite: "same-origin", method: "POST", want: 204},
		{name: "other local app", host: "localhost:3001", origin: "http://localhost:49999", method: "POST", want: 403},
		{name: "cross site without origin", host: "localhost:3001", fetchSite: "cross-site", method: "POST", want: 403},
		{name: "trusted exact origin", host: "localhost:3001", origin: "http://localhost:5173", fetchSite: "cross-site", method: "POST", trusted: []string{"http://localhost:5173"}, want: 204},
		{name: "trusted origin different port", host: "localhost:3001", origin: "http://localhost:5174", method: "POST", trusted: []string{"http://localhost:5173"}, want: 403},
		{name: "cross scheme", host: "localhost:3001", origin: "https://localhost:3001", fetchSite: "same-origin", method: "POST", want: 403},
		{name: "invalid origin path", host: "localhost:3001", origin: "http://localhost:3001/path", method: "POST", want: 403},
		{name: "invalid origin scheme", host: "localhost:3001", origin: "ftp://localhost:3001", method: "POST", want: 403},
		{name: "nonbrowser", host: "localhost:3001", method: "POST", want: 204},
		{name: "explicit listen hostname", addr: "desktop.example:3001", host: "desktop.example:3001", method: "GET", want: 204},
		{name: "explicit expanded IPv6", addr: "[2001:0db8:0:0:0:0:0:1]:3001", host: "[2001:db8::1]:3001", method: "GET", want: 204},
		{name: "explicit listen IP", addr: "192.0.2.1:3001", host: "192.0.2.1:3001", method: "GET", want: 204},
		{name: "wildcard listener rejects arbitrary hostname", addr: "0.0.0.0:3001", host: "desktop.example:3001", method: "GET", want: 403},
		{name: "configured hostname", addr: "0.0.0.0:3001", host: "DESKTOP.example:3001", allowed: []string{"desktop.example"}, method: "GET", want: 204},
		{name: "invalid port", host: "localhost:99999", method: "GET", want: 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.addr == "" {
				tc.addr = "127.0.0.1:3001"
			}
			p, err := newPolicy(Config{Addr: tc.addr, AllowedHosts: tc.allowed, TrustedOrigins: tc.trusted})
			if err != nil {
				t.Fatal(err)
			}
			h := p.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) }))
			r := httptest.NewRequest(tc.method, "http://localhost:3001/api/test", nil)
			r.Host = tc.host
			if tc.origin != "" {
				r.Header.Set("Origin", tc.origin)
			}
			if tc.fetchSite != "" {
				r.Header.Set("Sec-Fetch-Site", tc.fetchSite)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != tc.want {
				t.Fatalf("got %d want %d: %s", w.Code, tc.want, w.Body)
			}
			for _, header := range []string{"X-Frame-Options", "Content-Security-Policy", "X-Content-Type-Options", "Cache-Control"} {
				if w.Header().Get(header) == "" {
					t.Fatalf("missing shared header %s", header)
				}
			}
		})
	}
}

func TestInvalidPolicyConfiguration(t *testing.T) {
	for _, cfg := range []Config{
		{Addr: "invalid"},
		{Addr: "127.0.0.1:3001", AllowedHosts: []string{"*"}},
		{Addr: "127.0.0.1:3001", AllowedHosts: []string{"a..example"}}, {Addr: "127.0.0.1:3001", AllowedHosts: []string{"localhost:3001"}},
		{Addr: "127.0.0.1:3001", AllowedHosts: []string{"http://localhost"}},
		{Addr: "127.0.0.1:3001", TrustedOrigins: []string{"http://localhost/path"}},
	} {
		if _, err := newPolicy(cfg); err == nil {
			t.Fatalf("accepted invalid config %+v", cfg)
		}
	}
}

func TestDuplicateOriginIsRejected(t *testing.T) {
	p, err := newPolicy(Config{Addr: "127.0.0.1:3001"})
	if err != nil {
		t.Fatal(err)
	}
	h := p.handler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { t.Fatal("duplicate Origin reached handler") }))
	r := httptest.NewRequest("POST", "http://localhost:3001/api/test", nil)
	r.Header.Add("Origin", "http://localhost:3001")
	r.Header.Add("Origin", "http://localhost:3001")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Fatalf("status=%d", w.Code)
	}
}

func TestPublicOriginUsesConfiguredHTTPSWithoutForwardedHeaders(t *testing.T) {
	for _, origin := range []string{"http://camp.example", "https://camp.example/path", "https://user@camp.example", "https://camp.example?x=1"} {
		if _, err := newPolicy(Config{Addr: "127.0.0.1:0", PublicOrigin: origin}); err == nil {
			t.Fatalf("accepted invalid public origin %q", origin)
		}
	}
	policy, err := newPolicy(Config{Addr: "127.0.0.1:0", PublicOrigin: "https://camp.example"})
	if err != nil {
		t.Fatal(err)
	}
	handler := policy.handler(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(204) }))
	for _, test := range []struct {
		host, origin string
		want         int
	}{{"camp.example", "https://camp.example", 204}, {"camp.example", "http://camp.example", 403}, {"localhost", "https://camp.example", 403}} {
		request := httptest.NewRequest("POST", "http://"+test.host+"/api/example", nil)
		request.Header.Set("Origin", test.origin)
		request.Header.Set("X-Forwarded-Proto", "https")
		request.Header.Set("X-Forwarded-Host", "camp.example")
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code != test.want {
			t.Fatalf("%s %s: %d", test.host, test.origin, response.Code)
		}
	}
}
