package admin

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/mit112/hookrail/internal/httpx"
	"github.com/mit112/hookrail/internal/store"
)

type principalCtxKey struct{}

// withPrincipal stashes the resolved principal so handlers can reuse it instead
// of re-authenticating (e.g. whoami).
func withPrincipal(ctx context.Context, p principal) context.Context {
	return context.WithValue(ctx, principalCtxKey{}, p)
}

func principalFrom(ctx context.Context) (principal, bool) {
	p, ok := ctx.Value(principalCtxKey{}).(principal)
	return p, ok
}

// Role is an ordered privilege level on the admin API: viewer < operator < admin.
type Role int

const (
	RoleViewer Role = iota
	RoleOperator
	RoleAdmin
)

func (r Role) String() string {
	switch r {
	case RoleViewer:
		return "viewer"
	case RoleOperator:
		return "operator"
	case RoleAdmin:
		return "admin"
	default:
		return "unknown"
	}
}

// ParseRole maps a stored role-string to a Role. ok=false for anything else.
func ParseRole(s string) (Role, bool) {
	switch s {
	case "viewer":
		return RoleViewer, true
	case "operator":
		return RoleOperator, true
	case "admin":
		return RoleAdmin, true
	default:
		return 0, false
	}
}

// RouteInfo is the public (method, pattern, minRole) view of an admin route.
type RouteInfo struct {
	Method  string
	Pattern string
	MinRole Role
}

// adminRoute couples a RouteInfo with its handler for registration.
type adminRoute struct {
	RouteInfo
	handler http.HandlerFunc
}

// routes is the single source of truth for registration, policy, and tests.
// Every admin /v1 route MUST appear here; Handler() wraps each in authz, so a
// route cannot ship unguarded and the policy cannot drift from registration.
func (s *Server) routes() []adminRoute {
	return []adminRoute{
		{RouteInfo{"GET", "/v1/endpoints", RoleViewer}, s.listEndpoints},
		{RouteInfo{"POST", "/v1/endpoints", RoleAdmin}, s.createEndpoint},
		{RouteInfo{"GET", "/v1/endpoints/{id}", RoleViewer}, s.getEndpoint},
		{RouteInfo{"PATCH", "/v1/endpoints/{id}", RoleAdmin}, s.patchEndpoint},
		{RouteInfo{"DELETE", "/v1/endpoints/{id}", RoleAdmin}, s.deleteEndpoint},
		{RouteInfo{"POST", "/v1/endpoints/{id}/rotate-secret", RoleAdmin}, s.rotateSecret},
		{RouteInfo{"POST", "/v1/subscriptions", RoleAdmin}, s.createSubscription},
		{RouteInfo{"GET", "/v1/subscriptions", RoleViewer}, s.listSubscriptions},
		{RouteInfo{"GET", "/v1/subscriptions/{id}", RoleViewer}, s.getSubscription},
		{RouteInfo{"PATCH", "/v1/subscriptions/{id}", RoleAdmin}, s.patchSubscription},
		{RouteInfo{"DELETE", "/v1/subscriptions/{id}", RoleAdmin}, s.deleteSubscription},
		{RouteInfo{"GET", "/v1/dlq", RoleViewer}, s.listDLQ},
		{RouteInfo{"POST", "/v1/dlq/{delivery_id}/replay", RoleOperator}, s.replayDLQ},
		{RouteInfo{"GET", "/v1/deliveries", RoleViewer}, s.listDeliveries},
		{RouteInfo{"GET", "/v1/deliveries/{id}", RoleViewer}, s.getDelivery},
		{RouteInfo{"POST", "/v1/deliveries/{id}/skip", RoleOperator}, s.skipDelivery},
		{RouteInfo{"GET", "/v1/ordered-keys", RoleViewer}, s.listOrderedKeys},
		{RouteInfo{"GET", "/v1/whoami", RoleViewer}, s.whoami},
		// Token management (handlers stubbed in M2, real in M3). Admin-only.
		{RouteInfo{"POST", "/v1/admin-tokens", RoleAdmin}, s.createAdminToken},
		{RouteInfo{"GET", "/v1/admin-tokens", RoleAdmin}, s.listAdminTokens},
		{RouteInfo{"DELETE", "/v1/admin-tokens/{id}", RoleAdmin}, s.revokeAdminToken},
	}
}

// RouteTable exposes the policy (no handlers) for tests and introspection.
func (s *Server) RouteTable() []RouteInfo {
	rs := s.routes()
	out := make([]RouteInfo, len(rs))
	for i, r := range rs {
		out[i] = r.RouteInfo
	}
	return out
}

type principal struct {
	role    Role
	source  string // "env" | "token"
	tokenID string
}

// resolvePrincipal authenticates the request. The returned int is the HTTP
// status to emit on failure (0 = authenticated). 401 = bad/missing/revoked,
// 503 = DB error during lookup (NOT an auth decision).
func (s *Server) resolvePrincipal(r *http.Request) (principal, int) {
	raw, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	if !ok || raw == "" {
		return principal{}, http.StatusUnauthorized
	}
	// Break-glass: constant-time compare against the env admin token.
	got := digest(raw)
	if subtleConstantTimeEq(got, s.tokenDigest) {
		return principal{role: RoleAdmin, source: "env"}, 0
	}
	// DB-backed scoped token.
	id, roleStr, err := s.store.LookupAdminToken(r.Context(), raw)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return principal{}, http.StatusUnauthorized
		}
		return principal{}, http.StatusServiceUnavailable
	}
	role, valid := ParseRole(roleStr)
	if !valid { // defensive: a row whose role violates the enum
		return principal{}, http.StatusServiceUnavailable
	}
	return principal{role: role, source: "token", tokenID: id}, 0
}

// authz wraps a handler with authentication + a per-route minimum-role check.
func (s *Server) authz(min Role, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		p, status := s.resolvePrincipal(r)
		switch status {
		case 0:
			// Authenticated — fall through to the role check below.
		case http.StatusUnauthorized:
			httpx.Problem(w, status, "invalid credentials", "Authorization: Bearer <admin token> required")
			return
		default:
			// 503 and ANY unexpected status fail closed (never reach a handler).
			httpx.Problem(w, http.StatusServiceUnavailable, "not ready", "could not verify credentials")
			return
		}
		if p.role < min {
			httpx.Problem(w, http.StatusForbidden, "insufficient role", "requires role "+min.String())
			return
		}
		ctx := withPrincipal(r.Context(), p)
		// Audit trail: every privileged mutation (create/rotate/delete/replay/skip,
		// token management) is recorded with the acting principal, target, and
		// result — a webhook gateway that can rewrite endpoint secrets and
		// replay/skip deliveries must attribute those actions (design §8, README
		// "Audit trail"). Reads are not audited. The recorded status covers both
		// successful and rejected attempts.
		switch r.Method {
		case http.MethodPost, http.MethodPatch, http.MethodDelete:
			rec := &auditRecorder{ResponseWriter: w, status: http.StatusOK}
			next(rec, r.WithContext(ctx))
			slog.Info("admin mutation",
				"actor_source", p.source,
				"actor_token_id", p.tokenID,
				"role", p.role.String(),
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
			)
			return
		}
		next(w, r.WithContext(ctx))
	}
}

// auditRecorder captures the response status code for the audit log. Handlers
// on this surface only write JSON/Problem bodies (no streaming/hijack), so
// wrapping the ResponseWriter loses no behavior. Defaults to 200 because
// net/http treats an unwritten header as 200 OK on the first Write.
type auditRecorder struct {
	http.ResponseWriter
	status int
}

func (a *auditRecorder) WriteHeader(code int) {
	a.status = code
	a.ResponseWriter.WriteHeader(code)
}
