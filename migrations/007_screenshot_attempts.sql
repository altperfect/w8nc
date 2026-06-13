ALTER TABLE endpoints
    ADD COLUMN IF NOT EXISTS screenshot_on_match BOOLEAN NOT NULL DEFAULT FALSE;

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
