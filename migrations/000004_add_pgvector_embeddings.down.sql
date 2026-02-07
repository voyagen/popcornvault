DROP INDEX IF EXISTS idx_channels_embedding_hnsw;
ALTER TABLE channels DROP COLUMN IF EXISTS embedding;
