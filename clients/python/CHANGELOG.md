# Changelog

## 0.1.0 (unreleased)
- Initial release: sync + async producer client (`send_event`, `get_event`),
  automatic idempotency keys, retry/backoff with `Retry-After`, typed RFC-7807 errors,
  and a receiver-side `verify_signature` helper.
