CREATE EXTENSION IF NOT EXISTS pg_trgm;
CREATE INDEX IF NOT EXISTS idx_channels_name_trgm ON channels USING gin (name gin_trgm_ops);
