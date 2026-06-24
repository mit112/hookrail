package queue

import (
	"errors"
	"testing"

	"github.com/redis/go-redis/v9"
)

func TestNewWithClient_UsesProvidedClient(t *testing.T) {
	rdb := redis.NewClient(&redis.Options{Addr: "x:6379"})
	defer func() { _ = rdb.Close() }()
	q := NewWithClient(rdb, "s", "g")
	if q == nil || q.stream != "s" || q.group != "g" {
		t.Fatal("NewWithClient did not wire fields")
	}
	if q.MaxLen != 100_000 {
		t.Fatalf("MaxLen=%d want 100000", q.MaxLen)
	}
}

func TestIsNoGroup(t *testing.T) {
	if !IsNoGroup(errors.New("NOGROUP No such key 'hookrail:deliveries' or consumer group 'deliverers'")) {
		t.Fatal("should detect NOGROUP")
	}
	if IsNoGroup(errors.New("some other error")) {
		t.Fatal("false positive")
	}
	if IsNoGroup(nil) {
		t.Fatal("nil must be false")
	}
}
