#!/usr/bin/env bash
# Memory eval harness: enables memory store_recall on the shared config.yaml.
# Usage: run_agent_memory.sh [ondemand|always]

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CONFIG="${ROOT}/eval-harness/runner/config.yaml"
MODE="${1:-ondemand}"

cd "$ROOT"
exec go run ./eval-harness/runner -config "$CONFIG" -memory -memory-store-mode "$MODE"
