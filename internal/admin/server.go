// internal/admin/server.go
package admin

import (
	"context"

	"github.com/mit112/hookrail/internal/ratelimit"
	"github.com/mit112/hookrail/internal/ssrf"
	"github.com/mit112/hookrail/internal/store"
)

// Publisher is the post-replay best-effort XADD (design §4.1).
type Publisher interface {
	Publish(ctx context.Context, deliveryID string) error
	Ping(ctx context.Context) error
}

// Server holds everything the admin handlers need. Routing is added in Task 4.
//
//nolint:unused // fields are intentional — wired in Task 4+
type Server struct {
	store       *store.Store
	queue       Publisher
	masterKey   [32]byte
	policy      ssrf.Policy
	limits      *ratelimit.Registry
	tokenDigest [32]byte
}
