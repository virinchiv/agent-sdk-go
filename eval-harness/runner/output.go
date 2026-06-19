package main

import "github.com/agenticenv/agent-sdk-go/pkg/agent"

// Output is a JSON-friendly view of an agent run for eval harness tools.
type Output struct {
	Content   string                `json:"content"`
	LLMUsage  *agent.LLMUsage       `json:"llm_usage,omitempty"`
	Telemetry *agent.AgentTelemetry `json:"telemetry,omitempty"`
}

// OutputFromResult maps an AgentRunResult into Output for assertions or CLI JSON output.
func OutputFromResult(result *agent.AgentRunResult) *Output {
	if result == nil {
		return nil
	}
	return &Output{
		Content:   result.Content,
		LLMUsage:  result.LLMUsage,
		Telemetry: result.Telemetry,
	}
}
