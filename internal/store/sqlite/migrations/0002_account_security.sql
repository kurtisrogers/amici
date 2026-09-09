-- Account security: confirmed addresses, password reset, a second factor,
-- self-service closure, and rate limit counters that survive a restart.
--
-- Note what this migration deliberately does not do: it does not backfill
-- email_confirmed_at from created_at. Marking every existing address as
-- confirmed would paper over exactly the hole the column exists to close,
-- which is that an account registered with somebody else's address quietly
-- receives the friend requests meant for its real owner. Existing members are
-- asked to confirm, and until they do they are unreachable by email address.
-- Invite codes still work for them, so nobody is cut off in the meantime.

ALTER TABLE accounts ADD COLUMN email_confirmed_at TEXT;
ALTER TABLE accounts ADD COLUMN pending_email TEXT NOT NULL DEFAULT '';
ALTER TABLE accounts ADD COLUMN totp_secret TEXT NOT NULL DEFAULT '';
ALTER TABLE accounts ADD COLUMN totp_confirmed_at TEXT;
ALTER TABLE accounts ADD COLUMN closed_at TEXT;

-- The sweeper looks for accounts whose grace period has run out, so the
-- column it filters on is worth an index even though the table is small.
CREATE INDEX accounts_closed_at_idx ON accounts (closed_at);

-- One table for every single-use secret, keyed by purpose.
--
-- A token is stored as a SHA-256 hash of a 32-byte random value, in the same
-- way as a session. That entropy leaves nothing for a slow hash to protect,
-- and it means a stolen copy of this file contains no working password reset
-- links.
--
-- Purpose is part of the lookup rather than something the caller checks after
-- reading the row. A confirmation link that could be replayed as a password
-- reset would be a complete account takeover, and the difference between the
-- two is one WHERE clause.
CREATE TABLE account_tokens (
    id          TEXT PRIMARY KEY,
    account_id  TEXT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    purpose     TEXT NOT NULL,
    token_hash  TEXT NOT NULL,
    -- The address the token was sent to. For a change of address this is the
    -- new address; for the rest it is a record of where the link went, so a
    -- link minted before an address changed cannot be redeemed after it.
    email       TEXT NOT NULL DEFAULT '',
    attempts    INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL,
    expires_at  TEXT NOT NULL,
    consumed_at TEXT,
    CHECK (purpose IN ('email_confirm', 'password_reset', 'email_change', 'two_factor'))
) STRICT;

CREATE UNIQUE INDEX account_tokens_hash_uq ON account_tokens (token_hash);
CREATE INDEX account_tokens_account_idx ON account_tokens (account_id, purpose, created_at);
CREATE INDEX account_tokens_expires_idx ON account_tokens (expires_at);

-- Recovery codes stand in for the authenticator app. Like invite codes they
-- are stored as a keyed HMAC rather than a bare hash, because their entropy
-- is a code a person can write down rather than 256 random bits, and the key
-- is not in this file.
CREATE TABLE recovery_codes (
    id         TEXT PRIMARY KEY,
    account_id TEXT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    code_hash  TEXT NOT NULL,
    created_at TEXT NOT NULL,
    used_at    TEXT
) STRICT;

CREATE UNIQUE INDEX recovery_codes_hash_uq ON recovery_codes (code_hash);
CREATE INDEX recovery_codes_account_idx ON recovery_codes (account_id, used_at);

-- Fixed-window rate limit counters.
--
-- These used to live in a map in the process, which meant a restart handed
-- whoever was guessing a fresh budget and a second instance would have kept
-- its own tally. The keys are opaque strings built by the service layer; some
-- of them contain a client address, so the table is swept rather than kept.
CREATE TABLE rate_limits (
    key      TEXT PRIMARY KEY,
    count    INTEGER NOT NULL,
    reset_at TEXT NOT NULL
) STRICT;

CREATE INDEX rate_limits_reset_idx ON rate_limits (reset_at);
