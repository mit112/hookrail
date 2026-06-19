package dashboard

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/mit112/hookrail/internal/httpx"
)

type Server struct {
	cfg      Config
	sessions *Sessions
	pwDigest [32]byte
	thr      *throttle
	now      func() time.Time
}

func NewServer(cfg Config) *Server {
	return &Server{
		cfg:      cfg,
		sessions: NewSessions(cfg),
		pwDigest: sha256.Sum256([]byte(cfg.Password)),
		thr:      newThrottle(10, time.Minute),
		now:      time.Now,
	}
}

func (s *Server) cookieName() string { return "hk_dash" }

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
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			httpx.Problem(w, http.StatusRequestEntityTooLarge, "request too large", "request body exceeds 64 KiB")
			return
		}
		httpx.Problem(w, http.StatusBadRequest, "invalid body", `expected {"password": string}`)
		return
	}
	got := sha256.Sum256([]byte(body.Password))
	if subtle.ConstantTimeCompare(got[:], s.pwDigest[:]) != 1 {
		httpx.Problem(w, http.StatusUnauthorized, "invalid credentials", "wrong password")
		return
	}
	// Secure set dynamically via InsecureCookie flag — allowed by design.
	//nolint:gosec
	http.SetCookie(w, &http.Cookie{
		Name:     s.cookieName(),
		Value:    s.sessions.Issue(s.now()),
		Path:     "/",
		HttpOnly: true,
		Secure:   !s.cfg.InsecureCookie,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(s.cfg.SessionTTL.Seconds()),
	})
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]bool{"authenticated": true})
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

func (s *Server) handleSession(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]bool{"authenticated": true})
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
