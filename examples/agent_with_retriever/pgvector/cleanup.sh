#!/usr/bin/env bash
# Stop and remove the local pgvector Postgres Docker container for this example.
#
# Usage (from this directory):
#   ./cleanup.sh
#
# Environment:
#   PGVECTOR_CONTAINER_NAME   default pgvector
set -euo pipefail

CONTAINER_NAME="${PGVECTOR_CONTAINER_NAME:-pgvector}"

if ! command -v docker >/dev/null 2>&1; then
  echo "error: docker is required but not installed" >&2
  exit 1
fi

if docker ps -a --format '{{.Names}}' | grep -qx "$CONTAINER_NAME"; then
  echo "Stopping and removing '${CONTAINER_NAME}'..."
  docker rm -f "$CONTAINER_NAME" >/dev/null
  echo "Done."
else
  echo "No container named '${CONTAINER_NAME}' found."
fi
