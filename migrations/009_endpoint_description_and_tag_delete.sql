ALTER TABLE endpoints
ADD COLUMN IF NOT EXISTS description TEXT NOT NULL DEFAULT '';

ALTER TABLE endpoints
ADD CONSTRAINT endpoints_description_length
CHECK (char_length(description) <= 200);

ALTER TABLE endpoint_tags
DROP CONSTRAINT IF EXISTS endpoint_tags_tag_id_fkey;

ALTER TABLE endpoint_tags
ADD CONSTRAINT endpoint_tags_tag_id_fkey
FOREIGN KEY (tag_id) REFERENCES tags(id) ON DELETE CASCADE;
