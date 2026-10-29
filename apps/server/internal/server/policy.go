package server

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type policy struct {
	hosts        map[string]bool
	csrf         *http.CrossOriginProtection
	trusted      map[string]bool
	publicHost   string
	publicOrigin string
}

func newPolicy(config Config) (*policy, error) {
	p := &policy{hosts: map[string]bool{"localhost": true}, csrf: http.NewCrossOriginProtection(), trusted: make(map[string]bool)}
	host, _, err := net.SplitHostPort(config.Addr)
	if err != nil {
		return nil, fmt.Errorf("invalid listen address: %w", err)
	}
	if host != "" {
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsUnspecified() {
			normalized, ok := configuredHost(host)
			if !ok {
				return nil, fmt.Errorf("invalid listen hostname %q", host)
			}
			p.hosts[normalized] = true
		}
	}
	for _, host := range config.AllowedHosts {
		normalized, ok := configuredHost(host)
		if !ok {
			return nil, fmt.Errorf("invalid allowed host %q: use a hostname or IP without a port", host)
		}
		p.hosts[normalized] = true
	}
	if config.PublicOrigin != "" {
		if !validOrigin(config.PublicOrigin) {
			return nil, fmt.Errorf("CAMP_PUBLIC_ORIGIN must be an HTTP(S) origin without a path")
		}
		u, _ := url.Parse(config.PublicOrigin)
		hostname, _ := requestHost(u.Host)
		if u.Scheme == "http" && hostname != "localhost" && !net.ParseIP(hostname).IsLoopback() {
			return nil, fmt.Errorf("CAMP_PUBLIC_ORIGIN requires HTTPS except on localhost")
		}
		p.publicHost = u.Host
		p.publicOrigin = config.PublicOrigin
		p.hosts[hostname] = true
		p.trusted[config.PublicOrigin] = true
		if err := p.csrf.AddTrustedOrigin(config.PublicOrigin); err != nil {
			return nil, err
		}
	}
	for _, origin := range config.TrustedOrigins {
		if !validOrigin(origin) {
			return nil, fmt.Errorf("invalid trusted origin %q: use an http(s) origin without a path", origin)
		}
		if err := p.csrf.AddTrustedOrigin(origin); err != nil {
			return nil, fmt.Errorf("invalid trusted origin %q: %w", origin, err)
		}
		p.trusted[origin] = true
	}
	p.csrf.SetDenyHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		policyError(w, http.StatusForbidden, "invalid_origin", "Cross-origin changes are not allowed.")
	}))
	return p, nil
}

func validOrigin(origin string) bool {
	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Opaque != "" || u.Path != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.RawFragment != "" || u.String() != origin {
		return false
	}
	_, ok := requestHost(u.Host)
	return ok
}

func configuredHost(host string) (string, bool) {
	if ip := net.ParseIP(host); ip != nil {
		return ip.String(), true
	}
	if host == "" || strings.ContainsAny(host, " /\\\t\r\n?#@:") {
		return "", false
	}
	host = strings.ToLower(strings.TrimSuffix(host, "."))
	if host == "" || len(host) > 253 {
		return "", false
	}
	for label := range strings.SplitSeq(host, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for _, ch := range label {
			if !(ch >= 'a' && ch <= 'z') && !(ch >= '0' && ch <= '9') && ch != '-' {
				return "", false
			}
		}
	}
	return host, true
}

func requestHost(authority string) (string, bool) {
	if authority == "" || strings.ContainsAny(authority, " /\\\t\r\n?#@") {
		return "", false
	}
	u, err := url.Parse("http://" + authority)
	if err != nil || u.Host != authority || u.User != nil || u.Hostname() == "" {
		return "", false
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return "", false
		}
	} else if strings.HasSuffix(authority, ":") {
		return "", false
	}
	host := strings.ToLower(strings.TrimSuffix(u.Hostname(), "."))
	if ip := net.ParseIP(host); ip != nil {
		host = ip.String()
	}
	return host, true
}

func (p *policy) handler(next http.Handler) http.Handler {
	protected := p.csrf.Handler(next)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		if p.publicHost != "" && r.Host != p.publicHost {
			policyError(w, 403, "invalid_host", "Use Camp's configured public host.")
			return
		}
		host, ok := requestHost(r.Host)
		ip := net.ParseIP(host)
		if !ok || (!p.hosts[host] && (ip == nil || !ip.IsLoopback())) {
			policyError(w, http.StatusForbidden, "invalid_host", "This HTTP host is not allowed.")
			return
		}
		if origins := r.Header.Values("Origin"); len(origins) > 0 {
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}
			expectedOrigin := scheme + "://" + r.Host
			if p.publicOrigin != "" {
				expectedOrigin = p.publicOrigin
			}
			if len(origins) != 1 || !validOrigin(origins[0]) || (!p.trusted[origins[0]] && origins[0] != expectedOrigin) {
				policyError(w, http.StatusForbidden, "invalid_origin", "Cross-origin changes are not allowed.")
				return
			}
		}
		protected.ServeHTTP(w, r)
	})
}

func policyError(w http.ResponseWriter, status int, code, message string) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = fmt.Fprintf(w, `{"error":%q,"code":%q}`+"\n", message, code)
}
