#!/usr/bin/env bash
# Seed Weaviate after compose service is up (schema + sample documents).
set -euo pipefail

WEAVIATE_URL="${WEAVIATE_URL:-http://localhost:8080}"
WEAVIATE_CLASS="${WEAVIATE_CLASS:-Document}"
READY_TIMEOUT_SEC="${WEAVIATE_READY_TIMEOUT_SEC:-120}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
EXAMPLES_DIR="${SCRIPT_DIR}/../.."
ENV_FILE="${EXAMPLES_DIR}/.env"
ROOT_ENV_FILE="${EXAMPLES_DIR}/../.env"
DOCS_FILE="${SCRIPT_DIR}/sample-documents.json"

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

wait_for_ready() {
  local deadline=$((SECONDS + READY_TIMEOUT_SEC))
  echo "Waiting for Weaviate at ${WEAVIATE_URL}..."
  while (( SECONDS < deadline )); do
    if curl -sf "${WEAVIATE_URL}/v1/.well-known/ready" >/dev/null 2>&1; then
      echo "Weaviate is ready."
      return 0
    fi
    sleep 2
  done
  echo "error: Weaviate not ready within ${READY_TIMEOUT_SEC}s (docker logs weaviate)" >&2
  exit 1
}

require_cmd curl
require_cmd jq
resolve_embedding_api_key
wait_for_ready

echo "Creating class ${WEAVIATE_CLASS}..."
curl -sf -X POST "${WEAVIATE_URL}/v1/schema" \
  -H 'Content-Type: application/json' \
  -d "{
    \"class\": \"${WEAVIATE_CLASS}\",
    \"vectorizer\": \"text2vec-openai\",
    \"properties\": [
      {\"name\": \"content\", \"dataType\": [\"text\"]},
      {\"name\": \"source\", \"dataType\": [\"text\"]}
    ]
  }" >/dev/null || true

count=0
while IFS= read -r row; do
  payload=$(jq -n \
    --arg class "$WEAVIATE_CLASS" \
    --arg content "$(echo "$row" | jq -r '.content')" \
    --arg source "$(echo "$row" | jq -r '.source')" \
    '{class: $class, properties: {content: $content, source: $source}}')
  if ! curl -sf -X POST "${WEAVIATE_URL}/v1/objects" \
    -H 'Content-Type: application/json' \
    -d "$payload" >/dev/null; then
    echo "error: failed to insert into Weaviate (recreate: task infra:weaviate:down && task infra:weaviate:up if API key changed)" >&2
    exit 1
  fi
  count=$((count + 1))
done < <(jq -c '.[]' "$DOCS_FILE")

echo "Inserted ${count} documents from sample-documents.json"

MEMORY_CLASS="${WEAVIATE_MEMORY_CLASS:-AgentMemory}"
echo "Creating class ${MEMORY_CLASS} (long-term memory)..."
curl -sf -X POST "${WEAVIATE_URL}/v1/schema" \
  -H 'Content-Type: application/json' \
  -d "{
    \"class\": \"${MEMORY_CLASS}\",
    \"vectorizer\": \"text2vec-openai\",
    \"properties\": [
      {\"name\": \"text\", \"dataType\": [\"text\"]},
      {\"name\": \"kind\", \"dataType\": [\"text\"]},
      {\"name\": \"user_id\", \"dataType\": [\"text\"]},
      {\"name\": \"tenant_id\", \"dataType\": [\"text\"]},
      {\"name\": \"agent_id\", \"dataType\": [\"text\"]},
      {\"name\": \"scope_tags\", \"dataType\": [\"text[]\"]},
      {\"name\": \"metadata\", \"dataType\": [\"text\"]},
      {\"name\": \"expires_at\", \"dataType\": [\"date\"]},
      {\"name\": \"created_at\", \"dataType\": [\"date\"]},
      {\"name\": \"updated_at\", \"dataType\": [\"date\"]}
    ]
  }" >/dev/null || true
