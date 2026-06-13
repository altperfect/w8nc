ALTER TABLE endpoints
ADD COLUMN IF NOT EXISTS deactivate_after_seconds INTEGER NULL;

ALTER TABLE endpoints
ADD COLUMN IF NOT EXISTS deactivate_at TIMESTAMPTZ NULL;

CREATE INDEX IF NOT EXISTS idx_endpoints_active_deactivate_at
ON endpoints(deactivate_at)
WHERE active = TRUE AND deactivate_at IS NOT NULL;
