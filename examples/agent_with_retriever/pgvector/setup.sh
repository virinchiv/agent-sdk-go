#!/usr/bin/env bash
# One-shot pgvector setup for the agent_with_retriever/pgvector example:
#   - starts PostgreSQL with pgvector in Docker (or reuses existing container)
#   - waits until Postgres is ready
#   - applies setup.sql (extension, table, index)
#   - embeds sample-documents.json via OpenAI-compatible API and inserts rows
#
# Usage (from this directory):
#   ./setup.sh
#
# Teardown: ./cleanup.sh
#
# Environment:
#   OPENAI_APIKEY / LLM_APIKEY from env or examples/.env (required for embeddings)
#   EMBEDDING_MODEL          default text-embedding-3-small (1536 dimensions)
#   EMBEDDING_BASEURL        default LLM_BASEURL from .env or https://api.openai.com/v1
#   PGVECTOR_CONTAINER_NAME  default pgvector
#   PGVECTOR_PORT            default 5432
set -euo pipefail

CONTAINER_NAME="${PGVECTOR_CONTAINER_NAME:-pgvector}"
PG_IMAGE="${PGVECTOR_IMAGE:-pgvector/pgvector:pg16}"
PG_PORT="${PGVECTOR_PORT:-5432}"
PG_USER="${PGVECTOR_USER:-postgres}"
PG_PASSWORD="${PGVECTOR_PASSWORD:-secret}"
PG_DB="${PGVECTOR_DB:-vectordb}"
PG_TABLE="${PGVECTOR_TABLE:-documents}"
EMBEDDING_MODEL="${EMBEDDING_MODEL:-text-embedding-3-small}"
READY_TIMEOUT_SEC="${PGVECTOR_READY_TIMEOUT_SEC:-120}"

PG_DSN="postgres://${PG_USER}:${PG_PASSWORD}@localhost:${PG_PORT}/${PG_DB}?sslmode=disable"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${SCRIPT_DIR}/../../.env"
DOCS_FILE="${SCRIPT_DIR}/../common/sample-documents.json"
SQL_FILE="${SCRIPT_DIR}/setup.sql"

read_env_value() {
  local key="$1" file="$2"
  [[ -f "$file" ]] || return 1
  local line
  line="$(grep -E "^${key}=" "$file" | tail -1 || true)"
  [[ -n "$line" ]] || return 1
  line="${line#${key}=}"
  line="${line%$'\r'}"
  line="${line#\"}"; line="${line%\"}"
  line="${line#\'}"; line="${line%\'}"
  printf '%s' "$line"
}

require_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "error: '$1' is required but not installed" >&2
    exit 1
  fi
}

sql_escape() {
  printf '%s' "$1" | sed "s/'/''/g"
}

resolve_openai_api_key() {
  if [[ -n "${OPENAI_APIKEY:-}" ]]; then
    return 0
  fi
  if key="$(read_env_value OPENAI_APIKEY "$ENV_FILE" 2>/dev/null)" && [[ -n "$key" ]]; then
    export OPENAI_APIKEY="$key"
    echo "Using OPENAI_APIKEY from ${ENV_FILE}"
    return 0
  fi
  if key="$(read_env_value EMBEDDING_APIKEY "$ENV_FILE" 2>/dev/null)" && [[ -n "$key" ]]; then
    export OPENAI_APIKEY="$key"
    echo "Using EMBEDDING_APIKEY from ${ENV_FILE}"
    return 0
  fi
  if key="$(read_env_value LLM_APIKEY "$ENV_FILE" 2>/dev/null)" && [[ -n "$key" ]]; then
    export OPENAI_APIKEY="$key"
    echo "Using LLM_APIKEY from ${ENV_FILE} for embeddings"
    return 0
  fi
  echo "error: set OPENAI_APIKEY / EMBEDDING_APIKEY / LLM_APIKEY for embedding seed data" >&2
  exit 1
}

resolve_embedding_base_url() {
  if [[ -n "${EMBEDDING_BASEURL:-}" ]]; then
    return 0
  fi
  if url="$(read_env_value EMBEDDING_BASEURL "$ENV_FILE" 2>/dev/null)" && [[ -n "$url" ]]; then
    export EMBEDDING_BASEURL="$url"
    return 0
  fi
  if url="$(read_env_value LLM_BASEURL "$ENV_FILE" 2>/dev/null)" && [[ -n "$url" ]]; then
    export EMBEDDING_BASEURL="$url"
    return 0
  fi
  export EMBEDDING_BASEURL="https://api.openai.com/v1"
}

container_running() {
  docker ps --format '{{.Names}}' | grep -qx "$CONTAINER_NAME"
}

container_exists() {
  docker ps -a --format '{{.Names}}' | grep -qx "$CONTAINER_NAME"
}

wait_for_postgres() {
  local deadline=$((SECONDS + READY_TIMEOUT_SEC))
  echo "Waiting for Postgres in '${CONTAINER_NAME}' (timeout ${READY_TIMEOUT_SEC}s)..."
  while (( SECONDS < deadline )); do
    if docker exec "$CONTAINER_NAME" pg_isready -U "$PG_USER" -d "$PG_DB" >/dev/null 2>&1; then
      echo "Postgres is ready."
      return 0
    fi
    sleep 2
  done
  echo "error: Postgres did not become ready within ${READY_TIMEOUT_SEC}s" >&2
  echo "Check logs: docker logs ${CONTAINER_NAME}" >&2
  exit 1
}

start_postgres() {
  if container_running; then
    echo "Container '${CONTAINER_NAME}' is already running."
    return 0
  fi
  if container_exists; then
    echo "Starting existing container '${CONTAINER_NAME}'..."
    docker start "$CONTAINER_NAME" >/dev/null
    return 0
  fi

  echo "Creating and starting '${CONTAINER_NAME}' (${PG_IMAGE})..."
  docker run -d --name "$CONTAINER_NAME" \
    -e POSTGRES_PASSWORD="$PG_PASSWORD" \
    -e POSTGRES_DB="$PG_DB" \
    -p "${PG_PORT}:5432" \
    "$PG_IMAGE" >/dev/null
}

apply_schema() {
  if [[ ! -f "$SQL_FILE" ]]; then
    echo "error: missing ${SQL_FILE}" >&2
    exit 1
  fi
  echo "Applying schema from setup.sql..."
  docker exec -i "$CONTAINER_NAME" psql -U "$PG_USER" -d "$PG_DB" -v ON_ERROR_STOP=1 < "$SQL_FILE"
}

embed_text() {
  local text="$1"
  local body response
  body=$(jq -n --arg input "$text" --arg model "$EMBEDDING_MODEL" '{input: $input, model: $model}')
  response=$(curl -sf "${EMBEDDING_BASEURL%/}/embeddings" \
    -H "Authorization: Bearer ${OPENAI_APIKEY}" \
    -H "Content-Type: application/json" \
    -d "$body")
  echo "$response" | jq -c '.data[0].embedding'
}

seed_documents() {
  if [[ ! -f "$DOCS_FILE" ]]; then
    echo "error: missing ${DOCS_FILE}" >&2
    exit 1
  fi

  echo "Clearing existing rows in ${PG_TABLE}..."
  docker exec "$CONTAINER_NAME" psql -U "$PG_USER" -d "$PG_DB" -v ON_ERROR_STOP=1 \
    -c "TRUNCATE ${PG_TABLE} RESTART IDENTITY;" >/dev/null

  local count=0 row content source vec content_sql source_sql
  while IFS= read -r row; do
    content="$(echo "$row" | jq -r '.content')"
    source="$(echo "$row" | jq -r '.source')"
    echo "Embedding document $((count + 1)): ${source}"
    vec="$(embed_text "$content")"
    if [[ -z "$vec" || "$vec" == "null" ]]; then
      echo "error: empty embedding for ${source}" >&2
      exit 1
    fi
    content_sql="$(sql_escape "$content")"
    source_sql="$(sql_escape "$source")"
    docker exec "$CONTAINER_NAME" psql -U "$PG_USER" -d "$PG_DB" -v ON_ERROR_STOP=1 \
      -c "INSERT INTO ${PG_TABLE} (content, source, embedding) VALUES ('${content_sql}', '${source_sql}', '${vec}'::vector);" >/dev/null
    count=$((count + 1))
  done < <(jq -c '.[]' "$DOCS_FILE")

  echo "Inserted ${count} documents from sample-documents.json"
}

require_cmd docker
require_cmd curl
require_cmd jq
resolve_openai_api_key
resolve_embedding_base_url
start_postgres
wait_for_postgres
apply_schema
seed_documents

cat <<EOF

Setup complete.

Add to examples/.env (if not already set):
  PGVECTOR_DSN=${PG_DSN}
  PGVECTOR_TABLE=${PG_TABLE}
  EMBEDDING_MODEL=${EMBEDDING_MODEL}
  PGVECTOR_MIN_SCORE=0.35

If LLM_PROVIDER is not openai, also set:
  EMBEDDING_APIKEY=sk-...   # OpenAI key for embeddings (not Anthropic/Gemini)

Verify data and similarity scores:
  ./verify.sh "What is the return policy?"

Next (from examples/):
  go run ./agent_with_retriever/pgvector "What is the return policy?"

Cleanup when finished:
  ./cleanup.sh
EOF
