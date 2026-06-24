// Package redisclient builds a *redis.Client in either plain mode (bare Addr or
// redis:// URL) or Sentinel/failover mode (master discovery via a Sentinel quorum).
// Sentinel mode is selected iff SentinelAddrs is non-empty and wins over Addr.
package redisclient

import (
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

// Options is the unified Redis connection config, derived from app config.
type Options struct {
	Addr          string        // plain mode (REDIS_ADDR / RLRedisAddr); "host:port" or "redis://"
	SentinelAddrs []string      // failover mode (REDIS_SENTINEL_ADDRS); non-empty => Sentinel wins
	MasterName    string        // REDIS_MASTER_NAME; required in sentinel mode
	PoolSize      int           // 0 = go-redis default
	ReadTimeout   time.Duration // 0 = default
	WriteTimeout  time.Duration // 0 = default
}

// Sentinel reports whether failover mode is selected.
func (o Options) Sentinel() bool { return len(o.SentinelAddrs) > 0 }

// New returns a *redis.Client routing to the current master. In Sentinel mode it
// is a FailoverClient that discovers (and re-discovers on promotion) the master via
// the Sentinel quorum; in plain mode it is a direct client to Addr.
func New(o Options) (*redis.Client, error) {
	if o.Sentinel() {
		if o.MasterName == "" {
			return nil, fmt.Errorf("redisclient: MasterName required in sentinel mode")
		}
		return redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName:    o.MasterName,
			SentinelAddrs: o.SentinelAddrs,
			PoolSize:      o.PoolSize,
			ReadTimeout:   o.ReadTimeout,
			WriteTimeout:  o.WriteTimeout,
		}), nil
	}
	var opts *redis.Options
	if strings.HasPrefix(o.Addr, "redis://") {
		var err error
		if opts, err = redis.ParseURL(o.Addr); err != nil {
			return nil, err
		}
	} else {
		opts = &redis.Options{Addr: o.Addr}
	}
	if o.PoolSize != 0 {
		opts.PoolSize = o.PoolSize
	}
	if o.ReadTimeout != 0 {
		opts.ReadTimeout = o.ReadTimeout
	}
	if o.WriteTimeout != 0 {
		opts.WriteTimeout = o.WriteTimeout
	}
	return redis.NewClient(opts), nil
}
