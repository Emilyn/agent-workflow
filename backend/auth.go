package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/mail"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	sessionCookie   = "aw_session"
	sessionLifetime = 30 * 24 * time.Hour
	minPassword     = 10
	maxPassword     = 200
)

// passwordIterations is a variable so tests can use a cheaper setting.
var passwordIterations = pbkdf2Iterations

var roleRank = map[string]int{"member": 1, "admin": 2, "owner": 3}

type ctxKey int

const (
	userKey ctxKey = iota
	reqIDKey
)

// ---------- rate limiting ----------

// limiter allows n events per window per key. It lives in this process's
// memory, so with several server instances the effective limit is n per
// instance; move it to Redis or Postgres if you need an exact global limit.
type limiter struct {
	mu     sync.Mutex
	n      int
	window time.Duration
	hits   map[string][]time.Time
	calls  int
}

func newLimiter(n int, window time.Duration) *limiter {
	return &limiter{n: n, window: window, hits: map[string][]time.Time{}}
}

func (l *limiter) allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	cut := now.Add(-l.window)
	kept := l.hits[key][:0]
	for _, t := range l.hits[key] {
		if t.After(cut) {
			kept = append(kept, t)
		}
	}
	if len(kept) >= l.n {
		l.hits[key] = kept
		return false
	}
	l.hits[key] = append(kept, now)
	if l.calls++; l.calls%500 == 0 { // drop idle keys now and then
		for k, v := range l.hits {
			if len(v) == 0 || v[len(v)-1].Before(cut) {
				delete(l.hits, k)
			}
		}
	}
	return true
}

func (s *Server) clientIP(r *http.Request) string {
	if s.cfg.TrustProxy {
		// The last entry is the one our own proxy appended; earlier ones can be forged by the client.
		parts := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
		if ip := strings.TrimSpace(parts[len(parts)-1]); ip != "" {
			return ip
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// ---------- middleware ----------

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}

func requestID() string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// withMiddleware adds a request id, structured access logs, security headers and CSRF protection.
func (s *Server) withMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		id := requestID()
		w.Header().Set("X-Request-Id", id)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src https://fonts.gstatic.com; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'self'; form-action 'self'")
		if strings.HasPrefix(r.URL.Path, "/api/") {
			w.Header().Set("Cache-Control", "no-store")
		}
		rec := &statusRecorder{ResponseWriter: w, status: 200}

		if !s.sameOrigin(r) {
			writeError(rec, http.StatusForbidden, "cross-site request blocked")
		} else {
			next.ServeHTTP(rec, r.WithContext(context.WithValue(r.Context(), reqIDKey, id)))
		}
		if strings.HasPrefix(r.URL.Path, "/api/") && r.URL.Path != "/api/health" {
			s.log.Info("request", "id", id, "method", r.Method, "path", r.URL.Path, "status", rec.status,
				"ms", time.Since(start).Milliseconds())
		}
	})
}

// sameOrigin blocks cross-site writes. Browsers send Origin on cross-origin and
// on all non-GET requests; if it is present it must name this host.
func (s *Server) sameOrigin(r *http.Request) bool {
	switch r.Method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true // non-browser clients (curl, tests) do not send it
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host == r.Host
}

// ---------- sessions ----------

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, userID string) error {
	token := randomToken()
	exp := time.Now().Add(sessionLifetime)
	if err := s.repo.CreateSession(r.Context(), userID, tokenHash(token), exp); err != nil {
		return err
	}
	http.SetCookie(w, &http.Cookie{
		Name: sessionCookie, Value: token, Path: "/", Expires: exp, MaxAge: int(sessionLifetime.Seconds()),
		HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (s *Server) currentUser(r *http.Request) (User, error) {
	c, err := r.Cookie(sessionCookie)
	if err != nil || c.Value == "" {
		return User{}, errNotFound
	}
	return s.repo.SessionUser(r.Context(), tokenHash(c.Value))
}

// authed wraps a handler that needs a signed-in user.
func (s *Server) authed(h func(http.ResponseWriter, *http.Request, User)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		u, err := s.currentUser(r)
		if errors.Is(err, errNotFound) {
			writeError(w, http.StatusUnauthorized, "Please sign in.")
			return
		}
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		h(w, r, u)
	}
}

// wsCtx is what a workspace handler knows about the caller.
type wsCtx struct {
	User User
	WS   string
	Role string
}

// inWorkspace wraps a handler that works on one workspace. The caller must be a
// member with at least the given role. Non-members get a 404 so workspace ids
// cannot be probed.
func (s *Server) inWorkspace(min string, h func(http.ResponseWriter, *http.Request, wsCtx)) http.HandlerFunc {
	return s.authed(func(w http.ResponseWriter, r *http.Request, u User) {
		ws := r.PathValue("ws")
		role, err := s.repo.Role(r.Context(), ws, u.ID)
		if errors.Is(err, errNotFound) {
			writeError(w, http.StatusNotFound, "no such workspace")
			return
		}
		if err != nil {
			s.serverError(w, r, err)
			return
		}
		if roleRank[role] < roleRank[min] {
			writeError(w, http.StatusForbidden, "Only "+min+"s and above can do that.")
			return
		}
		h(w, r, wsCtx{User: u, WS: ws, Role: role})
	})
}

// ---------- account endpoints ----------

type meResponse struct {
	User       User            `json:"user"`
	Workspaces []WorkspaceInfo `json:"workspaces"`
}

func (s *Server) me(w http.ResponseWriter, r *http.Request, u User) {
	list, err := s.repo.WorkspacesFor(r.Context(), u.ID)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, meResponse{User: u, Workspaces: list})
}

type credentialsRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	Name     string `json:"name"`
}

func (s *Server) signup(w http.ResponseWriter, r *http.Request) {
	if !s.cfg.AllowSignup {
		writeError(w, http.StatusForbidden, "Sign-up is closed on this server.")
		return
	}
	var req credentialsRequest
	if !decode(w, r, &req) {
		return
	}
	if !s.signups.allow(s.clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "Too many sign-ups from here. Try again later.")
		return
	}
	req.Name = strings.TrimSpace(req.Name)
	addr, err := mail.ParseAddress(strings.TrimSpace(req.Email))
	switch {
	case err != nil || addr.Address != strings.TrimSpace(req.Email) || len(addr.Address) > 254:
		writeError(w, http.StatusBadRequest, "Enter a valid email address.")
		return
	case req.Name == "" || len([]rune(req.Name)) > maxNameLen:
		writeError(w, http.StatusBadRequest, "Enter your name (up to 80 characters).")
		return
	case len(req.Password) < minPassword || len(req.Password) > maxPassword:
		writeError(w, http.StatusBadRequest, "Use a password of at least 10 characters.")
		return
	}
	hash, err := hashPasswordN(req.Password, passwordIterations)
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	u, err := s.repo.CreateUser(r.Context(), addr.Address, req.Name, hash)
	if errors.Is(err, errEmailTaken) {
		writeError(w, http.StatusConflict, "An account with that email already exists. Sign in instead.")
		return
	}
	if err != nil {
		s.serverError(w, r, err)
		return
	}
	if _, err := s.repo.CreateWorkspace(r.Context(), u.ID, u.Name+"’s workspace"); err != nil {
		s.serverError(w, r, err)
		return
	}
	if err := s.startSession(w, r, u.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.me(w, r, u)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var req credentialsRequest
	if !decode(w, r, &req) {
		return
	}
	email := strings.ToLower(strings.TrimSpace(req.Email))
	if !s.logins.allow(s.clientIP(r)+"|"+email) || !s.loginsIP.allow(s.clientIP(r)) {
		writeError(w, http.StatusTooManyRequests, "Too many attempts. Wait a few minutes and try again.")
		return
	}
	u, err := s.repo.UserByEmail(r.Context(), email)
	hash := s.dummyHash // always do the same work, so timing does not reveal which emails exist
	if err == nil {
		hash = u.passwordHash
	} else if !errors.Is(err, errNotFound) {
		s.serverError(w, r, err)
		return
	}
	ok := checkPassword(req.Password, hash)
	if err != nil || !ok {
		writeError(w, http.StatusUnauthorized, "Incorrect email or password.")
		return
	}
	if err := s.startSession(w, r, u.ID); err != nil {
		s.serverError(w, r, err)
		return
	}
	s.me(w, r, u)
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		_ = s.repo.DeleteSession(r.Context(), tokenHash(c.Value))
	}
	http.SetCookie(w, &http.Cookie{Name: sessionCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteLaxMode})
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) serverError(w http.ResponseWriter, r *http.Request, err error) {
	id, _ := r.Context().Value(reqIDKey).(string)
	s.log.Error("server error", "id", id, "path", r.URL.Path, "err", err)
	writeError(w, http.StatusInternalServerError, "Something went wrong on the server (reference "+id+").")
}

var _ = slog.LevelInfo
