# Agent with retriever (`agent_with_retriever`)

Examples that wire a **vector retriever** into **agent-sdk-go**. Pick **one backend** per run.

| Backend | Directory | Guide |
|---------|-----------|--------|
| Weaviate | [`weaviate/`](weaviate/) | [`weaviate/README.md`](weaviate/README.md) |
| PostgreSQL + pgvector | [`pgvector/`](pgvector/) | [`pgvector/README.md`](pgvector/README.md) |

Shared sample data: [`common/sample-documents.json`](common/sample-documents.json).

## Prerequisites

- **Temporal** — [`temporal-setup.md`](../../temporal-setup.md)
- **LLM** — `LLM_APIKEY`, `LLM_MODEL` in `examples/.env` ([`env.sample`](../env.sample))
- **Vector store** — set up via `./setup.sh` in the backend folder you choose

## Quick start

```bash
cd examples
cp env.sample .env
# Edit .env: LLM keys and backend vars (see env.sample)

# Weaviate
cd agent_with_retriever/weaviate && ./setup.sh && cd ../..
go run ./agent_with_retriever/weaviate "What is the return policy?"

# pgvector
cd agent_with_retriever/pgvector && ./setup.sh && cd ../..
go run ./agent_with_retriever/pgvector "What is the return policy?"
```

Cleanup: `./cleanup.sh` in the backend folder when done.

## Retriever modes

Set `RETRIEVER_MODE` in `.env` (default `agentic`):

| Mode | Behavior |
|------|----------|
| `agentic` | Retriever exposed as a tool; LLM decides when to search |
| `prefetch` | Search runs once before the first LLM call; context injected into system prompt |
| `hybrid` | Prefetch and retriever tools |

```bash
RETRIEVER_MODE=prefetch go run ./agent_with_retriever/weaviate "What is the return policy?"
```

## Troubleshooting

| Issue | Where to look |
|-------|----------------|
| Weaviate setup, search, vectorizer | [`weaviate/README.md`](weaviate/README.md#troubleshooting) |
| pgvector setup, embeddings, `minScore` | [`pgvector/README.md`](pgvector/README.md#troubleshooting) |

**Common checks (all examples):**

- **Temporal** running — see [`temporal-setup.md`](../../temporal-setup.md)
- **`examples/.env`** — `LLM_APIKEY`, `LLM_MODEL`, and backend vars from [`env.sample`](../env.sample)
- **Vector store up** — `./setup.sh` in `weaviate/` or `pgvector/` before `go run`
- **Retriever mode** — `RETRIEVER_MODE=agentic|prefetch|hybrid` in `.env`
- **Debug** — `LOG_LEVEL=debug go run ./agent_with_retriever/<backend> "..."`

**pgvector + Anthropic/Gemini chat:** set `EMBEDDING_APIKEY` (OpenAI) in `.env`; chat `LLM_APIKEY` is not used for embeddings.

**Clean restart a backend:**

```bash
cd agent_with_retriever/weaviate   # or pgvector
./cleanup.sh && ./setup.sh
```
