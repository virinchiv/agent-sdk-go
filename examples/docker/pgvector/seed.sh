#!/usr/bin/env bash
# Seed pgvector after compose service is up (schema + sample documents).
set -euo pipefail

CONTAINER_NAME="${PGVECTOR_CONTAINER_NAME:-pgvector}"
PG_USER="${PGVECTOR_USER:-postgres}"
PG_PASSWORD="${PGVECTOR_PASSWORD:-secret}"
PG_DB="${PGVECTOR_DB:-vectordb}"
PG_TABLE="${PGVECTOR_TABLE:-documents}"
EMBEDDING_OPENAI_MODEL="${EMBEDDING_OPENAI_MODEL:-text-embedding-3-small}"
READY_TIMEOUT_SEC="${PGVECTOR_READY_TIMEOUT_SEC:-120}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
EXAMPLES_DIR="${SCRIPT_DIR}/../.."
ENV_FILE="${EXAMPLES_DIR}/.env"
ROOT_ENV_FILE="${EXAMPLES_DIR}/../.env"
DOCS_FILE="${SCRIPT_DIR}/sample-documents.json"
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
  command -v "$1" >/dev/null || { echo "error: '$1' is required" >&2; exit 1; }
}

sql_escape() {
  printf '%s' "$1" | sed "s/'/''/g"
}

resolve_embedding_api_key() {
  local f key
  if [[ -n "${EMBEDDING_OPENAI_APIKEY:-}" ]]; then
    return 0
  fi
  for f in "$ENV_FILE" "$ROOT_ENV_FILE"; do
    if key="$(read_env_value EMBEDDING_OPENAI_APIKEY "$f" 2>/dev/null)" && [[ -n "$key" ]]; then
      export EMBEDDING_OPENAI_APIKEY="$key"
      return 0
    fi
  done
  echo "error: EMBEDDING_OPENAI_APIKEY is required (examples/.env or environment)" >&2
  exit 1
}

resolve_embedding_base_url() {
  if [[ -n "${EMBEDDING_OPENAI_BASEURL:-}" ]]; then
    return 0
  fi
  if url="$(read_env_value EMBEDDING_OPENAI_BASEURL "$ENV_FILE" 2>/dev/null)" && [[ -n "$url" ]]; then
    export EMBEDDING_OPENAI_BASEURL="$url"
    return 0
  fi
  if url="$(read_env_value LLM_BASEURL "$ENV_FILE" 2>/dev/null)" && [[ -n "$url" ]]; then
    export EMBEDDING_OPENAI_BASEURL="$url"
    return 0
  fi
  export EMBEDDING_OPENAI_BASEURL="https://api.openai.com/v1"
}

wait_for_postgres() {
  local deadline=$((SECONDS + READY_TIMEOUT_SEC))
  echo "Waiting for Postgres in '${CONTAINER_NAME}'..."
  while (( SECONDS < deadline )); do
    if docker exec "$CONTAINER_NAME" pg_isready -U "$PG_USER" -d "$PG_DB" >/dev/null 2>&1; then
      echo "Postgres is ready."
      return 0
    fi
    sleep 2
  done
  echo "error: Postgres not ready within ${READY_TIMEOUT_SEC}s (docker logs pgvector)" >&2
  exit 1
}

embed_text() {
  local text="$1" body response
  body=$(jq -n --arg input "$text" --arg model "$EMBEDDING_OPENAI_MODEL" '{input: $input, model: $model}')
  response=$(curl -sf "${EMBEDDING_OPENAI_BASEURL%/}/embeddings" \
    -H "Authorization: Bearer ${EMBEDDING_OPENAI_APIKEY}" \
    -H "Content-Type: application/json" \
    -d "$body")
  echo "$response" | jq -c '.data[0].embedding'
}

require_cmd docker
require_cmd curl
require_cmd jq
resolve_embedding_api_key
resolve_embedding_base_url
wait_for_postgres

echo "Applying schema from setup.sql..."
docker exec -i "$CONTAINER_NAME" psql -U "$PG_USER" -d "$PG_DB" -v ON_ERROR_STOP=1 < "$SQL_FILE"

echo "Clearing existing rows in ${PG_TABLE}..."
docker exec "$CONTAINER_NAME" psql -U "$PG_USER" -d "$PG_DB" -v ON_ERROR_STOP=1 \
  -c "TRUNCATE ${PG_TABLE} RESTART IDENTITY;" >/dev/null

count=0
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
