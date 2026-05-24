# Weaviate retriever example

This program uses [`pkg/retriever/weaviate`](../../../pkg/retriever/weaviate): Weaviate embeds queries via **nearText** (no client-side embedding).

Parent overview: [`../README.md`](../README.md).

## Quick setup

```bash
cd examples/agent_with_retriever/weaviate
chmod +x setup.sh cleanup.sh
export OPENAI_APIKEY=sk-your-key   # or set in examples/.env
./setup.sh
```

Requires **Docker**, **curl**, **jq**, and an OpenAI API key for Weaviate’s `text2vec-openai` module.

**[`setup.sh`](setup.sh)** starts Weaviate, creates the schema, and loads [`../common/sample-documents.json`](../common/sample-documents.json).

```bash
./cleanup.sh   # when finished
```

## Configure `.env`

From `examples/`:

```bash
# Temporal + LLM (required)
LLM_APIKEY=sk-...
LLM_MODEL=gpt-4o

# Weaviate (defaults shown)
WEAVIATE_HOST=localhost:8080
WEAVIATE_SCHEME=http
WEAVIATE_CLASS=Document
WEAVIATE_RETRIEVER_NAME=weaviate-kb

# Optional: agentic | prefetch | hybrid
RETRIEVER_MODE=agentic
```

Weaviate uses **OpenAI** inside Docker for vectors. Chat can use another provider (e.g. Anthropic).

## Run the example

```bash
cd examples
go run ./agent_with_retriever/weaviate "What is the return policy?"
go run ./agent_with_retriever/weaviate "How long does standard shipping take in the US?"

RETRIEVER_MODE=prefetch go run ./agent_with_retriever/weaviate "What is the return policy?"

RETRIEVER_MODE=hybrid go run ./agent_with_retriever/weaviate "What are Pro and Enterprise support hours?"
```

Prompts match articles in [`../common/sample-documents.json`](../common/sample-documents.json).

## Troubleshooting

### `OPENAI_APIKEY` error from setup.sh

Weaviate’s `text2vec-openai` module needs an OpenAI key in the container:

```bash
export OPENAI_APIKEY=sk-your-key
./setup.sh
```

Or add `OPENAI_APIKEY` / `LLM_APIKEY` to `examples/.env` before running `./setup.sh`.

### Connection refused on `:8080`

Weaviate is not running or `WEAVIATE_HOST` is wrong.

```bash
docker ps
./setup.sh
curl -s http://localhost:8080/v1/.well-known/ready
```

### Empty search or `no relevant documents found`

1. Re-seed the sample KB: `./setup.sh`
2. Confirm class name matches `.env`: `WEAVIATE_CLASS=Document`
3. Optional: lower certainty — `WEAVIATE_MIN_SCORE=0.5` in `.env` (SDK default is **0.75**)
4. List objects:
   ```bash
   curl -s "http://localhost:8080/v1/objects?class=Document&limit=5"
   ```

### Vectorizer / OpenAI errors in logs

`OPENAI_APIKEY` must be set when the container starts. Fix and recreate:

```bash
./cleanup.sh
export OPENAI_APIKEY=sk-your-key
./setup.sh
docker logs weaviate
```

### Port already in use (`8080` or `50051`)

Another process or old container is using the port:

```bash
./cleanup.sh
./setup.sh
```

### Answers ignore the knowledge base

- Run `./setup.sh` and confirm objects exist (curl above).
- **Agentic mode** — LLM must call `retriever_weaviate-kb`; try **prefetch** to force retrieval:
  ```bash
  RETRIEVER_MODE=prefetch go run ./agent_with_retriever/weaviate "What is the return policy?"
  ```
- Check `WEAVIATE_HOST`, `WEAVIATE_CLASS`, and `WEAVIATE_SCHEME` in `.env`.

### Prefetch / hybrid returns little context

Prefetch searches with your **exact user message**. Use topic questions aligned with the sample KB (returns, shipping, warranty, etc.).

### Debug logs

```bash
LOG_LEVEL=debug go run ./agent_with_retriever/weaviate "What is the return policy?"
docker logs weaviate
```

### Clean reset

```bash
./cleanup.sh && ./setup.sh
```
