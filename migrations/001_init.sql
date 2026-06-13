CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE IF NOT EXISTS users (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    username TEXT NOT NULL UNIQUE,
    password_hash TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE IF NOT EXISTS sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_sessions_token_hash ON sessions(token_hash);
CREATE INDEX IF NOT EXISTS idx_sessions_expires_at ON sessions(expires_at);

CREATE TABLE IF NOT EXISTS endpoints (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NULL,
    url TEXT NOT NULL,
    http_method TEXT NOT NULL DEFAULT 'GET',
    headers JSONB NOT NULL DEFAULT '[]'::jsonb,
    request_body_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    request_body TEXT NOT NULL DEFAULT '',
    ping_interval_seconds INTEGER NOT NULL,
    deactivate_after_seconds INTEGER NULL,
    notify_condition JSONB NOT NULL,
    notify_once BOOLEAN NOT NULL DEFAULT TRUE,
    notification_template TEXT NOT NULL,
    screenshot_on_match BOOLEAN NOT NULL DEFAULT FALSE,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    state TEXT NOT NULL DEFAULT 'unknown',
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_checked_at TIMESTAMPTZ NULL,
    next_run_at TIMESTAMPTZ NULL,
    deactivate_at TIMESTAMPTZ NULL,
    last_status_code INTEGER NULL,
    last_response_length BIGINT NULL,
    last_error TEXT NULL,
    last_duration_ms INTEGER NULL,
    baseline_status_code INTEGER NULL,
    baseline_response_length BIGINT NULL,
    notified_at TIMESTAMPTZ NULL,
    deactivated_reason TEXT NULL,
    locked_until TIMESTAMPTZ NULL,
    version BIGINT NOT NULL DEFAULT 1
);

CREATE INDEX IF NOT EXISTS idx_endpoints_active_next_run
ON endpoints(active, next_run_at)
WHERE active = TRUE;

CREATE INDEX IF NOT EXISTS idx_endpoints_active_deactivate_at
ON endpoints(deactivate_at)
WHERE active = TRUE AND deactivate_at IS NOT NULL;

CREATE INDEX IF NOT EXISTS idx_endpoints_created_at
ON endpoints(created_at DESC);

CREATE INDEX IF NOT EXISTS idx_endpoints_updated_at
ON endpoints(updated_at DESC);

CREATE INDEX IF NOT EXISTS idx_endpoints_state
ON endpoints(state);

CREATE TABLE IF NOT EXISTS tags (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    name TEXT NOT NULL UNIQUE,
    color TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (char_length(name) BETWEEN 1 AND 16),
    CHECK (name ~ '^[a-z0-9][a-z0-9_-]{0,15}$'),
    CHECK (color IN ('slate', 'blue', 'teal', 'green', 'amber', 'rose', 'violet', 'gray'))
);

CREATE TABLE IF NOT EXISTS endpoint_tags (
    endpoint_id UUID NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    tag_id UUID NOT NULL REFERENCES tags(id) ON DELETE RESTRICT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (endpoint_id, tag_id)
);

CREATE INDEX IF NOT EXISTS idx_endpoint_tags_tag
ON endpoint_tags(tag_id, endpoint_id);

CREATE TABLE IF NOT EXISTS endpoint_checks (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    endpoint_id UUID NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    started_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ NOT NULL,
    status_code INTEGER NULL,
    response_length BIGINT NULL,
    duration_ms INTEGER NOT NULL,
    error TEXT NULL,
    truncated BOOLEAN NOT NULL DEFAULT FALSE,
    condition_matched BOOLEAN NOT NULL DEFAULT FALSE,
    notification_event_id UUID NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_endpoint_checks_endpoint_created
ON endpoint_checks(endpoint_id, created_at DESC);

CREATE TABLE IF NOT EXISTS notification_settings (
    id INTEGER PRIMARY KEY DEFAULT 1,
    telegram_enabled BOOLEAN NOT NULL DEFAULT FALSE,
    telegram_api_key_encrypted TEXT NULL,
    telegram_chat_id TEXT NULL,
    telegram_parse_mode TEXT NOT NULL DEFAULT 'Markdown',
    timezone TEXT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (id = 1)
);

INSERT INTO notification_settings (id)
VALUES (1)
ON CONFLICT (id) DO NOTHING;

CREATE TABLE IF NOT EXISTS notification_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    endpoint_id UUID NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'pending',
    message TEXT NOT NULL,
    error TEXT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    sent_at TIMESTAMPTZ NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_notification_events_status_created
ON notification_events(status, created_at);

CREATE TABLE IF NOT EXISTS screenshot_attempts (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    endpoint_id UUID NOT NULL REFERENCES endpoints(id) ON DELETE CASCADE,
    endpoint_check_id UUID NOT NULL REFERENCES endpoint_checks(id) ON DELETE CASCADE,
    notification_event_id UUID NULL REFERENCES notification_events(id) ON DELETE SET NULL,
    status TEXT NOT NULL DEFAULT 'pending',
    error TEXT NULL,
    image_path TEXT NULL,
    image_content_type TEXT NULL,
    image_size_bytes BIGINT NULL,
    capture_started_at TIMESTAMPTZ NULL,
    capture_finished_at TIMESTAMPTZ NULL,
    telegram_sent_at TIMESTAMPTZ NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_screenshot_attempts_pending
ON screenshot_attempts(status, created_at);

CREATE INDEX IF NOT EXISTS idx_screenshot_attempts_endpoint_check
ON screenshot_attempts(endpoint_check_id, created_at DESC);
