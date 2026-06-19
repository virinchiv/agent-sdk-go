"""Helpers for running the Go eval harness from DeepEval tests."""

from __future__ import annotations

import json
import subprocess
from pathlib import Path

from deepeval.models import DeepEvalBaseLLM

REPO_ROOT = Path(__file__).resolve().parents[2]
DEFAULT_PROMPT = "run eval check"


class StubJudge(DeepEvalBaseLLM):
    """Placeholder model for metrics that only use deterministic scoring."""

    def load_model(self):
        return self

    def generate(self, *args, **kwargs) -> str:
        raise RuntimeError("stub judge should not be invoked for deterministic metrics")

    async def a_generate(self, *args, **kwargs) -> str:
        raise RuntimeError("stub judge should not be invoked for deterministic metrics")

    def get_model_name(self, *args, **kwargs) -> str:
        return "stub-judge"


def run_agent(prompt: str = DEFAULT_PROMPT) -> dict:
    """Execute the eval harness runner and return parsed JSON output."""
    script = REPO_ROOT / "eval-harness" / "run_agent.sh"
    raw = subprocess.check_output([str(script), prompt], cwd=REPO_ROOT, text=True)
    return json.loads(raw)


def tools_called(agent_res: dict) -> list[str]:
    """Return tool names from telemetry breakdown."""
    breakdown = agent_res["telemetry"]["tools"]["breakdown"]
    return list(breakdown.keys())
