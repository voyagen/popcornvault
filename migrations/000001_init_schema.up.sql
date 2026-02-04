-- sources: one per M3U URL
CREATE TABLE IF NOT EXISTS sources (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL UNIQUE,
    source_type SMALLINT NOT NULL,
    url TEXT,
    use_tvg_id BOOLEAN DEFAULT true,
    user_agent TEXT,
    enabled BOOLEAN DEFAULT true,
    last_updated TIMESTAMPTZ,
    created_at TIMESTAMPTZ DEFAULT NOW()
);

-- groups: category per source (e.g. group-title from M3U)
CREATE TABLE IF NOT EXISTS groups (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    image TEXT,
    source_id BIGINT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    UNIQUE(name, source_id)
);
CREATE INDEX IF NOT EXISTS idx_groups_source_id ON groups(source_id);

-- channels: one per stream entry
CREATE TABLE IF NOT EXISTS channels (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(255) NOT NULL,
    image TEXT,
    url TEXT NOT NULL,
    media_type SMALLINT NOT NULL,
    source_id BIGINT NOT NULL REFERENCES sources(id) ON DELETE CASCADE,
    group_id BIGINT REFERENCES groups(id) ON DELETE SET NULL,
    favorite BOOLEAN DEFAULT false,
    UNIQUE(name, source_id, url)
);
CREATE INDEX IF NOT EXISTS idx_channels_source_id ON channels(source_id);
CREATE INDEX IF NOT EXISTS idx_channels_group_id ON channels(group_id);

-- channel_http_headers: optional HTTP headers per channel (from EXTVLCOPT)
CREATE TABLE IF NOT EXISTS channel_http_headers (
    id BIGSERIAL PRIMARY KEY,
    channel_id BIGINT NOT NULL REFERENCES channels(id) ON DELETE CASCADE UNIQUE,
    referrer TEXT,
    user_agent TEXT,
    http_origin TEXT,
    ignore_ssl BOOLEAN DEFAULT false
);
