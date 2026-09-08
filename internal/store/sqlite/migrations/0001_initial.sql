-- Amici initial schema.
--
-- Two conventions run through the whole schema:
--
--   * Identifiers are random 26-character strings, never autoincrementing
--     integers. Sequential ids would let anyone holding one walk the whole
--     set, which is the enumeration Amici exists to prevent.
--
--   * Secrets are stored only as hashes: session tokens, invite codes and
--     passwords. A stolen copy of this database cannot be replayed into
--     anybody's account or friend list.
--
-- Timestamps are stored as RFC3339 UTC strings so the database is legible
-- when someone is debugging at 2am with nothing but the sqlite3 shell.

CREATE TABLE accounts (
    id                 TEXT PRIMARY KEY,
    handle             TEXT NOT NULL,
    display_name       TEXT NOT NULL,
    email              TEXT NOT NULL,
    email_norm         TEXT NOT NULL,
    password_hash      TEXT NOT NULL,
    role               TEXT NOT NULL,
    status             TEXT NOT NULL,
    birth_date         TEXT NOT NULL,
    colourway          TEXT NOT NULL DEFAULT 'limonata',
    bio                TEXT NOT NULL DEFAULT '',
    reachable_by_email INTEGER NOT NULL DEFAULT 1,
    canvas_disabled    INTEGER NOT NULL DEFAULT 0,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL
) STRICT;

-- Handles and email addresses are unique, and the index is on the normalised
-- column so that lookups are exact and case-insensitive in one step.
CREATE UNIQUE INDEX accounts_handle_uq ON accounts (handle);
CREATE UNIQUE INDEX accounts_email_norm_uq ON accounts (email_norm);

CREATE TABLE sessions (
    id          TEXT PRIMARY KEY,
    account_id  TEXT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    token_hash  TEXT NOT NULL,
    created_at  TEXT NOT NULL,
    last_seen_at TEXT NOT NULL,
    expires_at  TEXT NOT NULL,
    user_agent  TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE UNIQUE INDEX sessions_token_hash_uq ON sessions (token_hash);
CREATE INDEX sessions_account_idx ON sessions (account_id);
CREATE INDEX sessions_expires_idx ON sessions (expires_at);

-- Friendships are symmetric. Storing the pair in sorted order lets the
-- primary key enforce "at most one friendship between two people" instead of
-- that rule living in application code where it can be forgotten.
CREATE TABLE friendships (
    account_a  TEXT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    account_b  TEXT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    PRIMARY KEY (account_a, account_b),
    CHECK (account_a < account_b)
) STRICT;

CREATE INDEX friendships_b_idx ON friendships (account_b);

CREATE TABLE friend_requests (
    id           TEXT PRIMARY KEY,
    from_id      TEXT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    to_id        TEXT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    state        TEXT NOT NULL,
    origin       TEXT NOT NULL,
    note         TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    responded_at TEXT,
    CHECK (from_id <> to_id),
    CHECK (state IN ('pending', 'accepted', 'declined', 'cancelled')),
    CHECK (origin IN ('email', 'invite'))
) STRICT;

-- At most one request may be in flight between two people in one direction.
-- Resolved requests are kept for history, so the uniqueness has to be partial.
CREATE UNIQUE INDEX friend_requests_pending_uq
    ON friend_requests (from_id, to_id)
    WHERE state = 'pending';
CREATE INDEX friend_requests_to_idx ON friend_requests (to_id, state);
CREATE INDEX friend_requests_from_idx ON friend_requests (from_id, state);
CREATE INDEX friend_requests_sent_at_idx ON friend_requests (from_id, created_at);

CREATE TABLE blocks (
    blocker_id TEXT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    blocked_id TEXT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    PRIMARY KEY (blocker_id, blocked_id),
    CHECK (blocker_id <> blocked_id)
) STRICT;

CREATE INDEX blocks_blocked_idx ON blocks (blocked_id);

-- Invites are the second and only other route to a friend request. The code
-- itself is never stored, only an HMAC of it keyed with the application
-- secret, so the 2^58 code space cannot be attacked offline from a dump.
CREATE TABLE invites (
    id             TEXT PRIMARY KEY,
    owner_id       TEXT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    code_hash      TEXT NOT NULL,
    label          TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    expires_at     TEXT NOT NULL,
    redeemed_at    TEXT,
    redeemed_by_id TEXT REFERENCES accounts (id) ON DELETE SET NULL,
    revoked_at     TEXT
) STRICT;

CREATE UNIQUE INDEX invites_code_hash_uq ON invites (code_hash);
CREATE INDEX invites_owner_idx ON invites (owner_id, created_at);
CREATE INDEX invites_expires_idx ON invites (expires_at);

CREATE TABLE posts (
    id         TEXT PRIMARY KEY,
    author_id  TEXT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    body       TEXT NOT NULL,
    visibility TEXT NOT NULL,
    created_at TEXT NOT NULL,
    edited_at  TEXT,
    -- There is no 'public' visibility, and adding one would need a migration
    -- and a conversation, which is exactly the friction we want.
    CHECK (visibility IN ('friends', 'only_me'))
) STRICT;

-- The feed is strictly reverse-chronological, so this composite index is the
-- whole query planner story. No ranking table, no score column, nothing to
-- tune. What you posted is what your friends see, in the order you posted it.
CREATE INDEX posts_author_created_idx ON posts (author_id, created_at DESC, id DESC);
CREATE INDEX posts_created_idx ON posts (created_at DESC, id DESC);

CREATE TABLE comments (
    id         TEXT PRIMARY KEY,
    post_id    TEXT NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
    author_id  TEXT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    body       TEXT NOT NULL,
    created_at TEXT NOT NULL,
    edited_at  TEXT
) STRICT;

CREATE INDEX comments_post_idx ON comments (post_id, created_at, id);

CREATE TABLE reactions (
    post_id    TEXT NOT NULL REFERENCES posts (id) ON DELETE CASCADE,
    account_id TEXT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    kind       TEXT NOT NULL,
    created_at TEXT NOT NULL,
    PRIMARY KEY (post_id, account_id),
    CHECK (kind IN ('love', 'hug', 'celebrate', 'laugh', 'care', 'thanks'))
) STRICT;

CREATE INDEX reactions_post_idx ON reactions (post_id, kind);

-- Both the member's source and the sanitiser's output are kept. Source so the
-- editor can show back exactly what they typed; rendered so that serving a
-- profile is a plain read and so a future tightening of the sanitiser can be
-- replayed over stored sources as a migration.
CREATE TABLE canvases (
    account_id        TEXT PRIMARY KEY REFERENCES accounts (id) ON DELETE CASCADE,
    html_source       TEXT NOT NULL DEFAULT '',
    css_source        TEXT NOT NULL DEFAULT '',
    html_rendered     TEXT NOT NULL DEFAULT '',
    css_rendered      TEXT NOT NULL DEFAULT '',
    sanitiser_version INTEGER NOT NULL DEFAULT 0,
    updated_at        TEXT NOT NULL
) STRICT;

CREATE INDEX canvases_version_idx ON canvases (sanitiser_version);

-- Support and developer accounts have real power over other people's
-- accounts. Every use of it is written down here and shown back to them.
CREATE TABLE audit_events (
    id         TEXT PRIMARY KEY,
    actor_id   TEXT NOT NULL,
    action     TEXT NOT NULL,
    subject_id TEXT NOT NULL DEFAULT '',
    detail     TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
) STRICT;

CREATE INDEX audit_created_idx ON audit_events (created_at DESC);
CREATE INDEX audit_actor_idx ON audit_events (actor_id, created_at DESC);

-- Reports are the only path by which member content reaches support. Nobody
-- at Amici browses feeds looking for things to act on.
CREATE TABLE reports (
    id           TEXT PRIMARY KEY,
    reporter_id  TEXT NOT NULL REFERENCES accounts (id) ON DELETE CASCADE,
    subject_kind TEXT NOT NULL,
    subject_id   TEXT NOT NULL,
    reason       TEXT NOT NULL,
    state        TEXT NOT NULL,
    created_at   TEXT NOT NULL,
    resolved_at  TEXT,
    resolved_by  TEXT REFERENCES accounts (id) ON DELETE SET NULL,
    resolution   TEXT NOT NULL DEFAULT '',
    CHECK (state IN ('open', 'resolved', 'dismissed')),
    CHECK (subject_kind IN ('post', 'comment', 'canvas', 'account'))
) STRICT;

CREATE INDEX reports_state_idx ON reports (state, created_at);
