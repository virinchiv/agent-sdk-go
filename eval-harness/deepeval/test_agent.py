"""DeepEval pytest suite for the Go eval harness."""

from deepeval import assert_test
from deepeval.metrics import ToolCorrectnessMetric
from deepeval.test_case import LLMTestCase, ToolCall

from harness import DEFAULT_PROMPT, StubJudge, run_agent, tools_called

EXPECTED_TOOLS = [
    ToolCall(name="eval_tool_1"),
    ToolCall(name="eval_tool_2"),
    ToolCall(name="eval_tool_3"),
]


def test_agent_completes_with_telemetry():
    """Assert on agent SDK run output: content, llm_usage, and telemetry."""
    agent_res = run_agent()

    assert agent_res["content"] == "eval complete"
    assert agent_res["llm_usage"]["total_tokens"] > 0

    run_telemetry = agent_res["telemetry"]["run"]
    tools_telemetry = agent_res["telemetry"]["tools"]

    assert run_telemetry["finish_reason"] == "complete"
    assert tools_telemetry["failed_calls"] == 0
    assert tools_telemetry["total_calls"] == 3
    assert set(tools_called(agent_res)) == {
        "eval_tool_1",
        "eval_tool_2",
        "eval_tool_3",
    }


def test_agent_tool_correctness():
    """ToolCorrectnessMetric using tools_called from telemetry.breakdown."""
    agent_res = run_agent()
    called = [ToolCall(name=name) for name in tools_called(agent_res)]

    test_case = LLMTestCase(
        input=DEFAULT_PROMPT,
        actual_output=agent_res["content"],
        tools_called=called,
        expected_tools=EXPECTED_TOOLS,
    )

    metric = ToolCorrectnessMetric(
        model=StubJudge(),
        threshold=1.0,
        strict_mode=True,
        should_exact_match=True,
        include_reason=True,
        async_mode=False,
    )
    assert_test(test_case, [metric])
