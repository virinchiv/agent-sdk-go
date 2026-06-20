package main

import "github.com/agenticenv/agent-sdk-go/pkg/agent"

// Output is a JSON-friendly view of an agent run for eval harness tools.
type Output struct {
	Content        string                `json:"content"`
	LLMUsage       *agent.LLMUsage       `json:"llm_usage,omitempty"`
	Telemetry      *agent.AgentTelemetry `json:"telemetry,omitempty"`
	MemoryScenario *MemoryScenarioOutput `json:"memory_scenario,omitempty"`
}

// MemoryScenarioOutput is JSON for two-run memory regression scenarios.
type MemoryScenarioOutput struct {
	Store  *Output `json:"store"`
	Recall *Output `json:"recall"`
}

// OutputFromResult maps a RunOutcome into Output for assertions or CLI JSON output.
func OutputFromResult(outcome *RunOutcome) *Output {
	if outcome == nil || outcome.Result == nil {
		return nil
	}
	output := OutputFromRunResult(outcome.Result)
	if outcome.MemoryScenario == nil {
		return output
	}
	output.MemoryScenario = &MemoryScenarioOutput{
		Store:  OutputFromRunResult(outcome.MemoryScenario.Store),
		Recall: OutputFromRunResult(outcome.MemoryScenario.Recall),
	}
	return output
}

// OutputFromRunResult maps an AgentRunResult into Output.
func OutputFromRunResult(result *agent.AgentRunResult) *Output {
	if result == nil {
		return nil
	}
	return &Output{
		Content:   result.Content,
		LLMUsage:  result.LLMUsage,
		Telemetry: result.Telemetry,
	}
}
