-- Optional DDL for the pgvector example (run against PGVECTOR_DSN database).
-- Embedding dimension must match EMBEDDING_MODEL (text-embedding-3-small → 1536).

CREATE EXTENSION IF NOT EXISTS vector;

CREATE TABLE IF NOT EXISTS documents (
  id         bigserial PRIMARY KEY,
  content    text NOT NULL,
  source     text NOT NULL,
  embedding  vector(1536) NOT NULL
);

CREATE INDEX IF NOT EXISTS documents_embedding_idx
  ON documents USING hnsw (embedding vector_cosine_ops);
