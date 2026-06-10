# Hookrail

Self-hostable webhook delivery service in Go: at-least-once delivery with retries,
exponential backoff, idempotency, HMAC signing, dead-letter queues, and full
observability. PostgreSQL is the source of truth; Redis Streams is the hot path.

**Status: pre-release, under active development.** Design doc and measured
reliability numbers land with the first tagged release.

License: Apache-2.0
