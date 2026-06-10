// Package queue is the Redis Streams hot path (§3.2). It is deliberately
// dumb: messages carry delivery_id ONLY, payloads live in PG, and Redis is
// never the owner of scheduling state — loss here is repaired by the sweeper.
package queue

import (
	"context"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

type Queue struct {
	rdb    *redis.Client
	stream string
	group  string
}

type Msg struct {
	ID         string // stream entry id, used for XACK
	DeliveryID string
}

// New accepts either a "redis://host:port" URL or a bare "host:port".
func New(addr, stream, group string) (*Queue, error) {
	var opts *redis.Options
	if strings.HasPrefix(addr, "redis://") {
		var err error
		opts, err = redis.ParseURL(addr)
		if err != nil {
			return nil, err
		}
	} else {
		opts = &redis.Options{Addr: addr}
	}
	return &Queue{rdb: redis.NewClient(opts), stream: stream, group: group}, nil
}

func (q *Queue) Close() { _ = q.rdb.Close() }

func (q *Queue) Ping(ctx context.Context) error { return q.rdb.Ping(ctx).Err() }

// EnsureGroup creates stream+group, tolerating "already exists".
func (q *Queue) EnsureGroup(ctx context.Context) error {
	err := q.rdb.XGroupCreateMkStream(ctx, q.stream, q.group, "$").Err()
	if err != nil && strings.Contains(err.Error(), "BUSYGROUP") {
		return nil
	}
	return err
}

// Publish XADDs a delivery id with approximate MAXLEN trimming (§5 retention).
func (q *Queue) Publish(ctx context.Context, deliveryID string) error {
	return q.rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: q.stream,
		MaxLen: 100_000,
		Approx: true,
		Values: map[string]any{"delivery_id": deliveryID},
	}).Err()
}

// Read blocks up to `block` for new messages for this consumer (XREADGROUP ">").
func (q *Queue) Read(ctx context.Context, consumer string, count int, block time.Duration) ([]Msg, error) {
	res, err := q.rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group: q.group, Consumer: consumer,
		Streams: []string{q.stream, ">"},
		Count:   int64(count), Block: block,
	}).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return flatten(res), nil
}

// Autoclaim steals messages idle longer than minIdle from dead consumers (PEL recovery).
func (q *Queue) Autoclaim(ctx context.Context, consumer string, minIdle time.Duration, count int) ([]Msg, error) {
	msgs, _, err := q.rdb.XAutoClaim(ctx, &redis.XAutoClaimArgs{
		Stream: q.stream, Group: q.group, Consumer: consumer,
		MinIdle: minIdle, Start: "0-0", Count: int64(count),
	}).Result()
	if err != nil {
		return nil, err
	}
	out := make([]Msg, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, toMsg(m))
	}
	return out, nil
}

func (q *Queue) Ack(ctx context.Context, msgID string) error {
	return q.rdb.XAck(ctx, q.stream, q.group, msgID).Err()
}

// PendingCount returns the PEL size (used by tests and metrics).
func (q *Queue) PendingCount(ctx context.Context) (int64, error) {
	p, err := q.rdb.XPending(ctx, q.stream, q.group).Result()
	if err != nil {
		return 0, err
	}
	return p.Count, nil
}

func flatten(res []redis.XStream) []Msg {
	var out []Msg
	for _, s := range res {
		for _, m := range s.Messages {
			out = append(out, toMsg(m))
		}
	}
	return out
}

func toMsg(m redis.XMessage) Msg {
	id, _ := m.Values["delivery_id"].(string)
	return Msg{ID: m.ID, DeliveryID: id}
}
