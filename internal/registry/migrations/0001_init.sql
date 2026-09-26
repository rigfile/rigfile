-- Rigfile registry schema, version 1. Every timestamp is timestamptz. Secrets (tokens, session ids, device codes) are
-- stored only as SHA-256 hashes.

CREATE TABLE users (
    id          bigserial PRIMARY KEY,
    github_id   bigint      NOT NULL UNIQUE,
    login       text        NOT NULL UNIQUE CHECK (login = lower(login) AND login ~ '^[a-z0-9](?:[a-z0-9-]{0,38})$'),
    name        text        NOT NULL DEFAULT '',
    avatar_url  text        NOT NULL DEFAULT '',
    is_admin    boolean     NOT NULL DEFAULT false,
    disabled_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE sessions (
    id_hash     bytea       PRIMARY KEY,
    user_id     bigint      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    csrf_secret bytea       NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    expires_at  timestamptz NOT NULL
);
CREATE INDEX sessions_user ON sessions(user_id);

CREATE TABLE api_tokens (
    id           bigserial   PRIMARY KEY,
    token_hash   bytea       NOT NULL UNIQUE,
    user_id      bigint      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    revoked_at   timestamptz,
    last_used_at timestamptz
);
CREATE INDEX api_tokens_user ON api_tokens(user_id);

CREATE TABLE device_codes (
    device_code_hash bytea       PRIMARY KEY,
    user_code        text        NOT NULL UNIQUE,
    user_id          bigint      REFERENCES users(id) ON DELETE CASCADE,
    status           text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','approved','denied')),
    created_at       timestamptz NOT NULL DEFAULT now(),
    expires_at       timestamptz NOT NULL,
    interval_s       integer     NOT NULL DEFAULT 5,
    last_poll_at     timestamptz
);

CREATE TABLE rigs (
    id          bigserial   PRIMARY KEY,
    owner       text        NOT NULL CHECK (owner ~ '^[a-z0-9](?:[a-z0-9-]{0,38})$'),
    name        text        NOT NULL CHECK (name  ~ '^[a-z0-9](?:[a-z0-9._-]{0,62})$'),
    description text        NOT NULL DEFAULT '',
    visibility  text        NOT NULL DEFAULT 'private' CHECK (visibility IN ('private','public')),
    created_by  bigint      NOT NULL REFERENCES users(id),
    created_at  timestamptz NOT NULL DEFAULT now(),
    removed_at  timestamptz,
    UNIQUE (owner, name)
);

CREATE TABLE versions (
    id             bigserial   PRIMARY KEY,
    rig_id         bigint      NOT NULL REFERENCES rigs(id) ON DELETE CASCADE,
    version        text        NOT NULL CHECK (version ~ '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$'),
    status         text        NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','published','rejected','yanked','removed')),
    tarball_sha256 text        NOT NULL CHECK (tarball_sha256 ~ '^[0-9a-f]{64}$'),
    size           bigint      NOT NULL,
    manifest_yaml  text        NOT NULL,
    description    text        NOT NULL DEFAULT '',
    readme         text        NOT NULL DEFAULT '',
    targets        text[]      NOT NULL DEFAULT '{}',
    needs_secrets  text[]      NOT NULL DEFAULT '{}',
    needs_logins   text[]      NOT NULL DEFAULT '{}',
    layers         text[]      NOT NULL DEFAULT '{}',
    scan_findings  jsonb       NOT NULL DEFAULT '[]',
    scan_warnings  jsonb       NOT NULL DEFAULT '[]',
    created_at     timestamptz NOT NULL DEFAULT now(),
    scanned_at     timestamptz,
    yanked_at      timestamptz,
    yank_reason    text        NOT NULL DEFAULT '',
    UNIQUE (rig_id, version)
);
CREATE INDEX versions_status ON versions(status);

CREATE TABLE version_files (
    version_id bigint  NOT NULL REFERENCES versions(id) ON DELETE CASCADE,
    path       text    NOT NULL,
    size       bigint  NOT NULL,
    sha256     text    NOT NULL,
    is_text    boolean NOT NULL,
    PRIMARY KEY (version_id, path)
);

CREATE TABLE stars (
    user_id    bigint      NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    rig_id     bigint      NOT NULL REFERENCES rigs(id) ON DELETE CASCADE,
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (user_id, rig_id)
);

CREATE TABLE jobs (
    id           bigserial   PRIMARY KEY,
    version_id   bigint      NOT NULL UNIQUE REFERENCES versions(id) ON DELETE CASCADE,
    state        text        NOT NULL DEFAULT 'queued' CHECK (state IN ('queued','running','done','failed')),
    attempts     integer     NOT NULL DEFAULT 0,
    run_after    timestamptz NOT NULL DEFAULT now(),
    locked_by    text,
    locked_until timestamptz,
    last_error   text        NOT NULL DEFAULT ''
);
CREATE INDEX jobs_ready ON jobs(state, run_after);

CREATE TABLE reports (
    id          bigserial   PRIMARY KEY,
    rig_id      bigint      REFERENCES rigs(id) ON DELETE SET NULL,
    rig_ref     text        NOT NULL,
    version     text        NOT NULL DEFAULT '',
    reporter_id bigint      REFERENCES users(id) ON DELETE SET NULL,
    reason      text        NOT NULL CHECK (reason IN ('malware','secret','illegal','abuse','impersonation','other')),
    details     text        NOT NULL DEFAULT '',
    status      text        NOT NULL DEFAULT 'open' CHECK (status IN ('open','actioned','dismissed')),
    created_at  timestamptz NOT NULL DEFAULT now(),
    handled_by  bigint      REFERENCES users(id) ON DELETE SET NULL,
    handled_at  timestamptz
);

CREATE TABLE audit_log (
    id          bigserial   PRIMARY KEY,
    at          timestamptz NOT NULL DEFAULT now(),
    actor_id    bigint,
    actor_login text        NOT NULL DEFAULT '',
    action      text        NOT NULL,
    target      text        NOT NULL DEFAULT '',
    detail      jsonb       NOT NULL DEFAULT '{}'
);
CREATE INDEX audit_at ON audit_log(at);
