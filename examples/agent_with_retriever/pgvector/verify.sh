#!/usr/bin/env bash
# Quick checks for pgvector sample data and similarity scores (no agent/Temporal).
#
# Usage (from this directory, after ./setup.sh):
#   ./verify.sh
#   ./verify.sh "What is the return policy?"
set -euo pipefail

CONTAINER_NAME="${PGVECTOR_CONTAINER_NAME:-pgvector}"
PG_USER="${PGVECTOR_USER:-postgres}"
PG_DB="${PGVECTOR_DB:-vectordb}"
PG_TABLE="${PGVECTOR_TABLE:-documents}"
QUERY="${1:-What is the return policy?}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${SCRIPT_DIR}/../../.env"

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
  command -v "$1" >/dev/null 2>&1 || { echo "error: need $1" >&2; exit 1; }
}

resolve_openai_api_key() {
  [[ -n "${OPENAI_APIKEY:-}" ]] && return 0
  if key="$(read_env_value OPENAI_APIKEY "$ENV_FILE" 2>/dev/null)" && [[ -n "$key" ]]; then
    export OPENAI_APIKEY="$key"; return 0
  fi
  if key="$(read_env_value EMBEDDING_APIKEY "$ENV_FILE" 2>/dev/null)" && [[ -n "$key" ]]; then
    export OPENAI_APIKEY="$key"; return 0
  fi
  if key="$(read_env_value LLM_APIKEY "$ENV_FILE" 2>/dev/null)" && [[ -n "$key" ]]; then
    export OPENAI_APIKEY="$key"; return 0
  fi
  echo "error: set OPENAI_APIKEY or EMBEDDING_APIKEY" >&2
  exit 1
}

resolve_embedding_base_url() {
  [[ -n "${EMBEDDING_BASEURL:-}" ]] && return 0
  if url="$(read_env_value EMBEDDING_BASEURL "$ENV_FILE" 2>/dev/null)" && [[ -n "$url" ]]; then
    export EMBEDDING_BASEURL="$url"; return 0
  fi
  if url="$(read_env_value LLM_BASEURL "$ENV_FILE" 2>/dev/null)" && [[ -n "$url" ]]; then
    export EMBEDDING_BASEURL="$url"; return 0
  fi
  export EMBEDDING_BASEURL="https://api.openai.com/v1"
}

embed_text() {
  local text="$1" body response
  body=$(jq -n --arg input "$text" --arg model "${EMBEDDING_MODEL:-text-embedding-3-small}" \
    '{input: $input, model: $model}')
  response=$(curl -sf "${EMBEDDING_BASEURL%/}/embeddings" \
    -H "Authorization: Bearer ${OPENAI_APIKEY}" \
    -H "Content-Type: application/json" \
    -d "$body")
  echo "$response" | jq -c '.data[0].embedding'
}

require_cmd docker
require_cmd curl
require_cmd jq
resolve_openai_api_key
resolve_embedding_base_url

echo "=== row count ==="
docker exec "$CONTAINER_NAME" psql -U "$PG_USER" -d "$PG_DB" -t -c "SELECT COUNT(*) FROM ${PG_TABLE};"

echo "=== top matches (no min_score filter) for: ${QUERY} ==="
vec="$(embed_text "$QUERY")"
docker exec "$CONTAINER_NAME" psql -U "$PG_USER" -d "$PG_DB" -c \
  "SELECT source, LEFT(content, 60) AS preview,
          ROUND((1 - (embedding <=> '${vec}'::vector))::numeric, 4) AS score
   FROM ${PG_TABLE}
   ORDER BY embedding <=> '${vec}'::vector
   LIMIT 5;"

echo ""
echo "If expected docs are missing, lower PGVECTOR_MIN_SCORE in examples/.env (example default 0.35)"
echo "If COUNT is 0, re-run ./setup.sh"
