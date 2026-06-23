package dashboard

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mit112/hookrail/internal/httpx"
)

type Server struct {
	cfg      Config
	sessions *Sessions
	usersPtr atomic.Pointer[Users]
	attest   *attestState // role-token attestation gate (M3/M4)
	thr      *throttle
	log      *slog.Logger
	now      func() time.Time
}

func NewServer(cfg Config) *Server {
	s := &Server{
		cfg:      cfg,
		sessions: NewSessions(cfg),
		attest:   newAttestState(),
		thr:      newThrottle(10, time.Minute),
		log:      slog.Default(),
		now:      time.Now,
	}
	s.usersPtr.Store(cfg.Users)
	return s
}

func (s *Server) cookieName() string { return "hk_dash" }

// currentUsers returns the live user set (swapped atomically on reload, M5).
func (s *Server) currentUsers() *Users { return s.usersPtr.Load() }

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	if r.Header.Get("Content-Type") != "application/json" {
		httpx.Problem(w, http.StatusUnsupportedMediaType, "bad content-type", "application/json required")
		return
	}
	if !s.thr.allow(clientIP(r), s.now()) {
		httpx.Problem(w, http.StatusTooManyRequests, "too many attempts", "slow down")
		return
	}
	var body struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			httpx.Problem(w, http.StatusRequestEntityTooLarge, "request too large", "request body exceeds 64 KiB")
			return
		}
		httpx.Problem(w, http.StatusBadRequest, "invalid body", `expected {"username": string, "password": string}`)
		return
	}
	role, ok := s.currentUsers().Verify(body.Username, body.Password)
	if !ok {
		s.log.Info("dashboard login failed", "user", body.Username, "ip", clientIP(r))
		httpx.Problem(w, http.StatusUnauthorized, "invalid credentials", "wrong username or password")
		return
	}
	// Secure set dynamically via InsecureCookie flag — allowed by design.
	//nolint:gosec
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName(),
		Value:    s.sessions.Issue(s.now(), body.Username),
		Path:     "/",
		HttpOnly: true,
		Secure:   !s.cfg.InsecureCookie,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(s.cfg.SessionTTL.Seconds()),
	})
	s.log.Info("dashboard login ok", "user", body.Username, "role", role.String())
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"authenticated": true, "role": role.String(), "username": body.Username})
}

func (s *Server) handleLogout(w http.ResponseWriter, _ *http.Request) {
	// Secure set dynamically via InsecureCookie flag — allowed by design.
	//nolint:gosec
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName(),
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   !s.cfg.InsecureCookie,
		SameSite: http.SameSiteStrictMode,
	})
	w.WriteHeader(http.StatusNoContent)
}

// subject validates the session cookie and returns the authenticated username.
func (s *Server) subject(r *http.Request) (string, bool) {
	c, err := r.Cookie(s.cookieName())
	if err != nil {
		return "", false
	}
	return s.sessions.Valid(c.Value, s.now())
}

func (s *Server) handleSession(w http.ResponseWriter, r *http.Request) {
	sub, ok := s.subject(r)
	if !ok {
		httpx.Problem(w, http.StatusUnauthorized, "unauthenticated", "no valid session")
		return
	}
	role, ok := s.currentUsers().RoleOf(sub)
	if !ok { // user removed since the cookie was issued
		httpx.Problem(w, http.StatusUnauthorized, "unauthenticated", "user no longer exists")
		return
	}
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]any{"authenticated": true, "role": role.String(), "username": sub})
}

// throttle: fixed-window per key.
type throttle struct {
	mu     sync.Mutex
	limit  int
	window time.Duration
	hits   map[string]struct {
		count int
		start time.Time
	}
}

func newThrottle(limit int, window time.Duration) *throttle {
	return &throttle{
		limit:  limit,
		window: window,
		hits: map[string]struct {
			count int
			start time.Time
		}{},
	}
}

func (t *throttle) allow(key string, now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	e := t.hits[key]
	if now.Sub(e.start) > t.window {
		e.count, e.start = 0, now
	}
	e.count++
	t.hits[key] = e
	return e.count <= t.limit
}
