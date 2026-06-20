-- Schema for the pgvector example (applied by docker/pgvector/seed.sh).
-- Embedding dimension must match EMBEDDING_OPENAI_MODEL (text-embedding-3-small → 1536).

CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS documents (
  id         bigserial PRIMARY KEY,
  content    text NOT NULL,
  source     text NOT NULL,
  embedding  vector(1536) NOT NULL
);

CREATE INDEX IF NOT EXISTS documents_embedding_idx
  ON documents USING hnsw (embedding vector_cosine_ops);

-- Long-term memory table for agent_with_memory/pgvector (no seed rows — filled by agent runs).
CREATE TABLE IF NOT EXISTS agent_memories (
  id          UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  text        TEXT NOT NULL,
  kind        TEXT NOT NULL DEFAULT '',
  user_id     TEXT,
  tenant_id   TEXT,
  agent_id    TEXT,
  scope_tags  TEXT[] NOT NULL DEFAULT '{}',
  metadata    JSONB NOT NULL DEFAULT '{}',
  expires_at  TIMESTAMPTZ,
  created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
  embedding   vector(1536)
);

CREATE INDEX IF NOT EXISTS agent_memories_embedding_idx
  ON agent_memories USING hnsw (embedding vector_cosine_ops);
