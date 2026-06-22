CREATE TABLE admin_tokens (
  id         TEXT PRIMARY KEY,
  token_hash BYTEA NOT NULL UNIQUE,          -- SHA-256 of the plaintext token
  role       TEXT  NOT NULL CHECK (role IN ('viewer','operator','admin')),
  label      TEXT  NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  revoked_at TIMESTAMPTZ                      -- NULL = active (soft delete)
);
