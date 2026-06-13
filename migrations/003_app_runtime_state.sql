CREATE TABLE IF NOT EXISTS app_runtime_state (
    id INTEGER PRIMARY KEY DEFAULT 1,
    last_seen_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CHECK (id = 1)
);
