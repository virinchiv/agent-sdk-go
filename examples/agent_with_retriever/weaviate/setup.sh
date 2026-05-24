#!/usr/bin/env bash
# One-shot Weaviate setup for the agent_with_retriever/weaviate example:
#   - starts Docker (or reuses an existing weaviate container)
#   - waits until the API is ready
#   - creates schema and loads sample-documents.json
#
# Usage (from this directory):
#   ./setup.sh
#
# Teardown: ./cleanup.sh
#
# Environment:
#   OPENAI_APIKEY   required for text2vec-openai (falls back to LLM_APIKEY from examples/.env)
#   WEAVIATE_URL    default http://localhost:8080
#   WEAVIATE_CLASS  default Document
set -euo pipefail

CONTAINER_NAME="${WEAVIATE_CONTAINER_NAME:-weaviate}"
WEAVIATE_IMAGE="${WEAVIATE_IMAGE:-cr.weaviate.io/semitechnologies/weaviate:1.27.0}"
WEAVIATE_HTTP_PORT="${WEAVIATE_HTTP_PORT:-8080}"
WEAVIATE_GRPC_PORT="${WEAVIATE_GRPC_PORT:-50051}"
WEAVIATE_URL="${WEAVIATE_URL:-http://localhost:${WEAVIATE_HTTP_PORT}}"
WEAVIATE_CLASS="${WEAVIATE_CLASS:-Document}"
READY_TIMEOUT_SEC="${WEAVIATE_READY_TIMEOUT_SEC:-120}"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
ENV_FILE="${SCRIPT_DIR}/../../.env"
DOCS_FILE="${SCRIPT_DIR}/../common/sample-documents.json"

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

resolve_openai_api_key() {
  if [[ -n "${OPENAI_APIKEY:-}" ]]; then
    return 0
  fi
  if key="$(read_env_value OPENAI_APIKEY "$ENV_FILE" 2>/dev/null)" && [[ -n "$key" ]]; then
    export OPENAI_APIKEY="$key"
    echo "Using OPENAI_APIKEY from ${ENV_FILE}"
    return 0
  fi
  if key="$(read_env_value LLM_APIKEY "$ENV_FILE" 2>/dev/null)" && [[ -n "$key" ]]; then
    export OPENAI_APIKEY="$key"
    echo "Using LLM_APIKEY from ${ENV_FILE} for Weaviate text2vec-openai"
    return 0
  fi
  echo "error: set OPENAI_APIKEY (Weaviate vectorizer) or add OPENAI_APIKEY / LLM_APIKEY to ${ENV_FILE}" >&2
  exit 1
}

container_running() {
  docker ps --format '{{.Names}}' | grep -qx "$CONTAINER_NAME"
}

container_exists() {
  docker ps -a --format '{{.Names}}' | grep -qx "$CONTAINER_NAME"
}

wait_for_ready() {
  local deadline=$((SECONDS + READY_TIMEOUT_SEC))
  echo "Waiting for Weaviate at ${WEAVIATE_URL} (timeout ${READY_TIMEOUT_SEC}s)..."
  while (( SECONDS < deadline )); do
    if curl -sf "${WEAVIATE_URL}/v1/.well-known/ready" >/dev/null 2>&1; then
      echo "Weaviate is ready."
      return 0
    fi
    sleep 2
  done
  echo "error: Weaviate did not become ready within ${READY_TIMEOUT_SEC}s" >&2
  echo "Check logs: docker logs ${CONTAINER_NAME}" >&2
  exit 1
}

start_weaviate() {
  if container_running; then
    echo "Container '${CONTAINER_NAME}' is already running."
    return 0
  fi
  if container_exists; then
    echo "Starting existing container '${CONTAINER_NAME}'..."
    docker start "$CONTAINER_NAME" >/dev/null
    return 0
  fi

  echo "Creating and starting '${CONTAINER_NAME}' (${WEAVIATE_IMAGE})..."
  docker run -d --name "$CONTAINER_NAME" \
    -p "${WEAVIATE_HTTP_PORT}:8080" \
    -p "${WEAVIATE_GRPC_PORT}:50051" \
    -e AUTHENTICATION_ANONYMOUS_ACCESS_ENABLED=true \
    -e DEFAULT_VECTORIZER_MODULE=text2vec-openai \
    -e ENABLE_MODULES=text2vec-openai \
    -e OPENAI_APIKEY="${OPENAI_APIKEY}" \
    "$WEAVIATE_IMAGE" >/dev/null
}

seed_documents() {
  if [[ ! -f "$DOCS_FILE" ]]; then
    echo "error: missing ${DOCS_FILE}" >&2
    exit 1
  fi

  echo "Creating class ${WEAVIATE_CLASS} at ${WEAVIATE_URL} (ignored if it already exists)..."
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

  local count=0 row payload
  while IFS= read -r row; do
    payload=$(jq -n \
      --arg class "$WEAVIATE_CLASS" \
      --arg content "$(echo "$row" | jq -r '.content')" \
      --arg source "$(echo "$row" | jq -r '.source')" \
      '{class: $class, properties: {content: $content, source: $source}}')
    curl -sf -X POST "${WEAVIATE_URL}/v1/objects" \
      -H 'Content-Type: application/json' \
      -d "$payload" >/dev/null
    count=$((count + 1))
  done < <(jq -c '.[]' "$DOCS_FILE")

  echo "Inserted ${count} documents from sample-documents.json"
}

require_cmd docker
require_cmd curl
require_cmd jq
resolve_openai_api_key
start_weaviate
wait_for_ready
seed_documents

cat <<EOF

Setup complete.

Next (from examples/):
  go run ./agent_with_retriever/weaviate "What is the return policy?"

Cleanup when finished:
  ./cleanup.sh
EOF
