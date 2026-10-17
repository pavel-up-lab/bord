CREATE TABLE schema_migrations (
    version INTEGER NOT NULL
);

CREATE TABLE foundation_check (
    id INTEGER PRIMARY KEY,
    created_at TEXT NOT NULL
);

CREATE TABLE id_sequences (
    kind TEXT PRIMARY KEY,
    value INTEGER NOT NULL CHECK (value >= 0)
);

CREATE TABLE boards (
    id INTEGER PRIMARY KEY,
    slug TEXT NOT NULL UNIQUE,
    title TEXT NOT NULL,
    enabled INTEGER NOT NULL DEFAULT 1 CHECK (enabled IN (0, 1)),
    created_at TEXT NOT NULL
);

CREATE TABLE anonymous_sessions (
    id TEXT PRIMARY KEY,
    fingerprint BLOB NOT NULL UNIQUE,
    created_at TEXT NOT NULL,
    last_seen_at TEXT NOT NULL
);

CREATE TABLE attachments (
    id TEXT PRIMARY KEY,
    state TEXT NOT NULL CHECK (state IN ('prepared', 'attached', 'deleted')),
    media_type TEXT NOT NULL,
    size_bytes INTEGER NOT NULL CHECK (size_bytes >= 0),
    sha256 BLOB NOT NULL CHECK (length(sha256) = 32),
    width INTEGER NOT NULL CHECK (width > 0),
    height INTEGER NOT NULL CHECK (height > 0),
    safe_filename TEXT NOT NULL,
    created_at TEXT NOT NULL,
    attached_at TEXT,
    deleted_at TEXT
);

CREATE TABLE threads (
    id INTEGER PRIMARY KEY,
    board_id INTEGER NOT NULL REFERENCES boards(id) ON DELETE RESTRICT,
    status TEXT NOT NULL CHECK (status IN ('open', 'closed', 'archived')),
    created_at TEXT NOT NULL,
    bumped_at TEXT NOT NULL
);

CREATE INDEX threads_catalog_idx ON threads(board_id, bumped_at DESC, id DESC);

CREATE TABLE posts (
    id INTEGER PRIMARY KEY,
    thread_id INTEGER NOT NULL REFERENCES threads(id) ON DELETE RESTRICT,
    reply_to_id INTEGER REFERENCES posts(id) ON DELETE RESTRICT,
    author_id TEXT NOT NULL,
    body TEXT NOT NULL,
    attachment_id TEXT UNIQUE REFERENCES attachments(id) ON DELETE RESTRICT,
    status TEXT NOT NULL CHECK (status IN ('visible', 'deleted', 'hidden')),
    created_at TEXT NOT NULL,
    deleted_at TEXT,
	 hidden_body TEXT,
	 hidden_attachment_id TEXT REFERENCES attachments(id) ON DELETE RESTRICT,
    CHECK ((status = 'visible' AND (length(trim(body)) > 0 OR attachment_id IS NOT NULL)) OR status <> 'visible'),
    CHECK (status = 'visible' OR (body = '' AND attachment_id IS NULL))
);

CREATE INDEX posts_thread_idx ON posts(thread_id, id);

CREATE TABLE post_delete_tokens (
    post_id INTEGER PRIMARY KEY REFERENCES posts(id) ON DELETE CASCADE,
    verifier BLOB NOT NULL,
    params_version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL
);

CREATE TABLE idempotency_keys (
    operation TEXT NOT NULL,
    key TEXT NOT NULL,
    request_hash BLOB NOT NULL CHECK (length(request_hash) = 32),
    response BLOB NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT,
    PRIMARY KEY (operation, key)
);

CREATE INDEX idempotency_expiry_idx ON idempotency_keys(expires_at);

CREATE TABLE event_outbox (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    schema_version INTEGER NOT NULL CHECK (schema_version > 0),
    event_type TEXT NOT NULL,
    board_id INTEGER REFERENCES boards(id) ON DELETE RESTRICT,
    thread_id INTEGER REFERENCES threads(id) ON DELETE RESTRICT,
    post_id INTEGER REFERENCES posts(id) ON DELETE RESTRICT,
    payload BLOB NOT NULL,
    created_at TEXT NOT NULL,
    published_at TEXT
);

CREATE INDEX event_outbox_pending_idx ON event_outbox(published_at, id);

CREATE TABLE request_subjects (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    ip_lookup BLOB NOT NULL,
    ip_prefix BLOB,
    encrypted_ip BLOB,
    nonce BLOB,
    key_id TEXT,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    legal_hold INTEGER NOT NULL DEFAULT 0 CHECK (legal_hold IN (0, 1)),
    post_id INTEGER REFERENCES posts(id) ON DELETE SET NULL
);

CREATE INDEX request_subjects_lookup_idx ON request_subjects(ip_lookup, created_at);
CREATE INDEX request_subjects_expiry_idx ON request_subjects(expires_at);
CREATE INDEX request_subjects_post_idx ON request_subjects(post_id);

CREATE TABLE used_challenges (
    id TEXT PRIMARY KEY,
    expires_at TEXT NOT NULL
);

CREATE INDEX used_challenges_expiry_idx ON used_challenges(expires_at);

CREATE TABLE reports (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    post_id INTEGER NOT NULL REFERENCES posts(id) ON DELETE RESTRICT,
    subject_id INTEGER REFERENCES request_subjects(id) ON DELETE SET NULL,
    reason TEXT NOT NULL,
    created_at TEXT NOT NULL,
    UNIQUE (post_id, subject_id, reason)
);

CREATE TABLE bans (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    subject_kind TEXT NOT NULL CHECK (subject_kind IN ('ip', 'prefix', 'lookup', 'session')),
    subject_value BLOB NOT NULL,
    board_id INTEGER REFERENCES boards(id) ON DELETE RESTRICT,
    operation TEXT,
    reason TEXT NOT NULL,
    created_at TEXT NOT NULL,
    expires_at TEXT NOT NULL,
    revoked_at TEXT
);

CREATE INDEX bans_match_idx ON bans(subject_kind, subject_value, expires_at);

CREATE TABLE admin_identities (
    id TEXT PRIMARY KEY,
    role TEXT NOT NULL,
    credential_verifier BLOB NOT NULL,
    params_version INTEGER NOT NULL DEFAULT 1,
    created_at TEXT NOT NULL,
    expires_at TEXT,
    revoked_at TEXT
);

CREATE TABLE moderation_actions (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    admin_id TEXT REFERENCES admin_identities(id) ON DELETE SET NULL,
    action TEXT NOT NULL,
    target_type TEXT,
    target_id TEXT,
    reason TEXT,
    outcome TEXT NOT NULL,
    request_id TEXT NOT NULL,
    payload BLOB NOT NULL DEFAULT '{}',
    created_at TEXT NOT NULL
);
