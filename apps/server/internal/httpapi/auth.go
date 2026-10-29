package httpapi

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/cofy-x/camp/apps/server/internal/storage"
	"github.com/cofy-x/camp/apps/server/internal/team"
)

const sessionCookie = "camp_session"

type loginWindow struct {
	start time.Time
	count int
}
type authLimiter struct {
	mu      sync.Mutex
	windows map[string]loginWindow
}

func (l *authLimiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.windows == nil {
		l.windows = map[string]loginWindow{}
	}
	now := time.Now()
	if len(l.windows) > 4096 {
		for k, w := range l.windows {
			if now.Sub(w.start) > time.Minute {
				delete(l.windows, k)
			}
		}
		if len(l.windows) > 4096 {
			return false
		}
	}
	window := l.windows[key]
	if now.Sub(window.start) >= time.Minute {
		window = loginWindow{start: now}
	}
	window.count++
	l.windows[key] = window
	return window.count <= 12
}

// EnableTeam wraps all API endpoints, including unknown routes and downloads.
// Configure it before serving. Raw New handlers remain useful for isolated
// handler tests. The serving boundary always enables authentication.
func (a *API) EnableTeam(service *team.Service, trustedOrigins []string, publicOrigins ...string) {
	a.team = service
	if len(publicOrigins) > 0 {
		a.publicOrigin = publicOrigins[0]
	}
	a.authOrigins = map[string]bool{}
	for _, origin := range trustedOrigins {
		a.authOrigins[origin] = true
	}
	a.handler = a.protect(a.handler)
}
func (a *API) protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if r.TLS == nil && !strings.HasPrefix(a.publicOrigin, "https://") && r.URL.Path != "/api/health" {
			peer, _, err := net.SplitHostPort(r.RemoteAddr)
			host := r.Host
			if parsed, _, err := net.SplitHostPort(host); err == nil {
				host = parsed
			}
			host = strings.Trim(host, "[]")
			if err != nil || !net.ParseIP(peer).IsLoopback() || (host != "localhost" && !net.ParseIP(host).IsLoopback()) {
				writeError(w, 403, "https_required", "Team access requires HTTPS outside localhost.")
				return
			}
		}
		public := r.URL.Path == "/api/health" || r.URL.Path == "/api/auth/status" || r.URL.Path == "/api/auth/setup" || r.URL.Path == "/api/auth/login" || r.URL.Path == "/api/auth/accept-invite"
		if r.Method != "GET" && r.Method != "HEAD" && r.Method != "OPTIONS" {
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}
			expectedOrigin := scheme + "://" + r.Host
			if a.publicOrigin != "" {
				expectedOrigin = a.publicOrigin
			}
			origins := r.Header.Values("Origin")
			if len(origins) != 1 || origins[0] != expectedOrigin && !a.authOrigins[origins[0]] {
				writeError(w, 403, "invalid_origin", "Use Camp from its configured origin.")
				return
			}
		}
		if public {
			next.ServeHTTP(w, r)
			return
		}
		cookie, e := r.Cookie(sessionCookie)
		if e != nil {
			writeError(w, 401, "unauthenticated", "Sign in to Camp.")
			return
		}
		u, e := a.team.Authenticate(r.Context(), cookie.Value)
		if e != nil {
			a.fail(w, r, e)
			return
		}
		ctx := storage.WithUser(r.Context(), u)
		if r.URL.Path == "/api/workspace/events" {
			var stop func()
			ctx, stop = a.team.WatchSession(ctx, cookie.Value)
			defer stop()
			if _, e = a.team.Authenticate(ctx, cookie.Value); e != nil {
				a.fail(w, r, e)
				return
			}
			monitor, done := context.WithCancel(ctx)
			defer done()
			go func() {
				ticker := time.NewTicker(time.Second)
				defer ticker.Stop()
				for {
					select {
					case <-monitor.Done():
						return
					case <-ticker.C:
						a.team.RevalidateSession(monitor, cookie.Value)
					}
				}
			}()
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
func (a *API) authRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/auth/status", func(w http.ResponseWriter, r *http.Request) {
		if a.team == nil {
			writeError(w, 503, "auth_unavailable", "Team authentication is not configured.")
			return
		}
		required, e := a.team.SetupRequired(r.Context())
		if e != nil {
			a.fail(w, r, e)
			return
		}
		var user *storage.User
		if cookie, e := r.Cookie(sessionCookie); e == nil {
			if u, e := a.team.Authenticate(r.Context(), cookie.Value); e == nil {
				user = &u
			}
		}
		profile, e := a.store.GetTeam(r.Context())
		if e != nil {
			a.fail(w, r, e)
			return
		}
		a.write(w, 200, map[string]any{"setupRequired": required, "user": user, "team": profile})
	})
	for _, action := range []string{"setup", "login", "accept-invite"} {
		mux.HandleFunc("POST /api/auth/"+action, func(w http.ResponseWriter, r *http.Request) {
			if a.team == nil {
				writeError(w, 503, "auth_unavailable", "Team authentication is not configured.")
				return
			}
			host, _, _ := net.SplitHostPort(r.RemoteAddr)
			if !a.authLimiter.allow(host) {
				w.Header().Set("Retry-After", "60")
				writeError(w, 429, "rate_limited", "Too many sign-in attempts. Try again in a minute.")
				return
			}
			var in struct {
				TeamName string `json:"teamName"`
				Token    string `json:"token"`
				Username string `json:"username"`
				Name     string `json:"name"`
				Password string `json:"password"`
			}
			if !decode(w, r, &in, 8192) {
				return
			}
			var user storage.User
			var token string
			var err error
			switch action {
			case "setup":
				user, token, err = a.team.Setup(r.Context(), in.Token, in.Username, in.Name, in.Password, in.TeamName)
			case "login":
				user, token, err = a.team.Login(r.Context(), in.Username, in.Password)
			case "accept-invite":
				user, token, err = a.team.AcceptInvite(r.Context(), in.Token, in.Username, in.Name, in.Password)
			}
			if err != nil {
				a.fail(w, r, err)
				return
			}
			http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: token, Path: "/", HttpOnly: true, Secure: r.TLS != nil || strings.HasPrefix(a.publicOrigin, "https://"), SameSite: http.SameSiteStrictMode, MaxAge: int(team.SessionLifetime / time.Second)})
			a.write(w, 200, map[string]any{"user": user})
		})
	}
	mux.HandleFunc("POST /api/auth/logout", func(w http.ResponseWriter, r *http.Request) {
		var in struct{}
		if !decode(w, r, &in, 1024) {
			return
		}
		cookie, e := r.Cookie(sessionCookie)
		if e == nil {
			e = a.team.Logout(r.Context(), cookie.Value)
		}
		if e != nil {
			a.fail(w, r, e)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", HttpOnly: true, Secure: r.TLS != nil || strings.HasPrefix(a.publicOrigin, "https://"), SameSite: http.SameSiteStrictMode, MaxAge: -1})
		a.write(w, 200, map[string]bool{"ok": true})
	})
}
func (a *API) authorizeRoute(route string, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if a.team == nil {
			localRoomHandler(next)(w, r)
			return
		}
		u, ok := storage.UserFromContext(r.Context())
		if !ok {
			writeError(w, 401, "unauthenticated", "Sign in to Camp.")
			return
		}
		admin := route == "POST /api/resources" || route == "PATCH /api/resources/{id}"
		if admin && u.Role != "admin" {
			writeError(w, 403, "forbidden", "An administrator is required.")
			return
		}
		if route == "POST /api/runs/{id}/cancel" && u.Role != "admin" {
			run, e := a.store.GetRun(r.Context(), r.PathValue("id"))
			if e != nil {
				a.fail(w, r, e)
				return
			}
			if run.RequestedByUserID != u.ID {
				writeError(w, 403, "forbidden", "You can cancel your own agent work.")
				return
			}
		}
		next(w, r)
	}
}
func (a *API) admin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, ok := storage.UserFromContext(r.Context())
		if !ok || u.Role != "admin" {
			writeError(w, 403, "forbidden", "An administrator is required.")
			return
		}
		next(w, r)
	}
}
