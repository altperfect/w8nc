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
