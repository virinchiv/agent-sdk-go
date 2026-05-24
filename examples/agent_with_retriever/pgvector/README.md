# pgvector retriever example

This program uses [`pkg/retriever/pgvector`](../../../pkg/retriever/pgvector): queries are embedded with an **OpenAI-compatible API**, then searched in PostgreSQL with [**pgvector**](https://github.com/pgvector/pgvector).

Parent overview: [`../README.md`](../README.md).

## Quick setup

```bash
cd examples/agent_with_retriever/pgvector
chmod +x setup.sh cleanup.sh verify.sh
./setup.sh
```

Requires **Docker**, **curl**, **jq**, and an OpenAI-compatible key for embeddings (`EMBEDDING_APIKEY`, `OPENAI_APIKEY`, or `LLM_APIKEY` in `examples/.env`).

**[`setup.sh`](setup.sh)** starts Postgres, applies [`setup.sql`](setup.sql), embeds [`../common/sample-documents.json`](../common/sample-documents.json), and prints `PGVECTOR_DSN` for `.env`.

```bash
./cleanup.sh   # when finished
```

## Configure `.env`

From `examples/` (after `./setup.sh`):

```bash
# Temporal + LLM (required)
LLM_APIKEY=sk-...
LLM_MODEL=gpt-4o

# Postgres
PGVECTOR_DSN=postgres://postgres:secret@localhost:5432/vectordb?sslmode=disable
PGVECTOR_TABLE=documents
PGVECTOR_RETRIEVER_NAME=pgvector-kb

# Embeddings (must match ./setup.sh)
EMBEDDING_MODEL=text-embedding-3-small
EMBEDDING_APIKEY=sk-...              # required when LLM_PROVIDER is not openai
# PGVECTOR_MIN_SCORE=0.35            # example default; see env.sample

# Optional: agentic | prefetch | hybrid
RETRIEVER_MODE=agentic
```

Embeddings use **OpenAI** (or `EMBEDDING_*`). Chat can use another provider (e.g. Anthropic).

## Run the example

```bash
cd examples
go run ./agent_with_retriever/pgvector "What is the return policy?"
go run ./agent_with_retriever/pgvector "How long does standard shipping take in the US?"

RETRIEVER_MODE=prefetch go run ./agent_with_retriever/pgvector "What is the return policy?"

RETRIEVER_MODE=hybrid go run ./agent_with_retriever/pgvector "What are Pro and Enterprise support hours?"
```

Sample prompts match the customer-support articles in [`../common/sample-documents.json`](../common/sample-documents.json) (returns, shipping, warranty, support hours, etc.).

## Verify search (optional)

```bash
./verify.sh "What is the return policy?"
```

Shows row count and similarity scores without running the agent.

## Troubleshooting

### `no relevant documents found`

The retriever ran but no rows passed the similarity filter.

1. Check data and scores:
   ```bash
   ./verify.sh "What is the return policy?"
   ```
   - **`COUNT` is 0** → run `./setup.sh` again, or fix `PGVECTOR_DSN` in `examples/.env`.
   - **Rows exist but low `score`** → lower the threshold in `examples/.env`:
     ```bash
     PGVECTOR_MIN_SCORE=0.35
     ```
     Re-run the example (startup line shows `minScore: 0.35`).

2. **Embeddings key** — search uses OpenAI `/embeddings`, not your chat LLM. If `LLM_PROVIDER=anthropic` or `gemini`, set:
   ```bash
   EMBEDDING_APIKEY=sk-...
   EMBEDDING_BASEURL=https://api.openai.com/v1
   ```
   Re-run `./setup.sh` so stored vectors use the same model as queries.

3. **Prefetch / hybrid** — your full user message is embedded as the search query. Use a concrete question (e.g. *“What is the return policy?”*), not *“Summarize the knowledge base”*.

### `embedding config: ... LLM_PROVIDER=anthropic`

Set `EMBEDDING_APIKEY` (OpenAI-compatible) in `examples/.env`. `LLM_APIKEY` alone is not enough when chat uses Anthropic/Gemini.

### `PGVECTOR_DSN is required`

Copy `PGVECTOR_DSN` from `./setup.sh` output into `examples/.env`.

### `dimension mismatch` or SQL errors

`EMBEDDING_MODEL` must match `vector(1536)` in `setup.sql` (default `text-embedding-3-small`). After changing the model, `./cleanup.sh`, `./setup.sh`, and update `.env`.

### Connection / port errors

```bash
./cleanup.sh
./setup.sh
docker logs pgvector
docker ps
```

Port **5432** already in use → stop other Postgres or set `PGVECTOR_PORT` and update `PGVECTOR_DSN`.

### Weak or incomplete answers (prefetch)

Only documents above `PGVECTOR_MIN_SCORE` are injected. Run `./verify.sh` with your exact prompt; lower `PGVECTOR_MIN_SCORE` if needed docs are below the threshold.

### Debug logs

```bash
LOG_LEVEL=debug go run ./agent_with_retriever/pgvector "What is the return policy?"
```

Look for `pgvector search done` with `docs=0` vs embed/query errors.

### Clean reset

```bash
./cleanup.sh && ./setup.sh
./verify.sh "What is the return policy?"
```
