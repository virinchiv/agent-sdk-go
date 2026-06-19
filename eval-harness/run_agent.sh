#!/usr/bin/env bash
# Eval harness wrapper: runs the Go runner and prints JSON to stdout.
# Args: $1 prompt (optional). Promptfoo also passes $2/$3 (JSON); ignored.

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
CONFIG="${ROOT}/eval-harness/runner/config.yaml"

cd "$ROOT"
if [[ -n "${1:-}" ]]; then
  exec go run ./eval-harness/runner -config "$CONFIG" -prompt "$1"
else
  exec go run ./eval-harness/runner -config "$CONFIG"
fi
