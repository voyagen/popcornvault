ALTER TABLE channels ADD COLUMN embedding vector(1024);

CREATE INDEX idx_channels_embedding_hnsw
    ON channels USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);
