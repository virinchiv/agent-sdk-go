package types

// AgentRunResult is the structured result of a completed run (content, model, metadata).
type AgentRunResult struct {
	Content   string         `json:"content"`
	AgentName string         `json:"agent_name"`
	Model     string         `json:"model"`
	Metadata  map[string]any `json:"metadata"`
	// Usage is the sum of token usage across all LLM calls in this run (when reported by the provider).
	Usage *LLMUsage `json:"usage,omitempty"`
}

// AgentRunAsyncResult is the single outcome from AgentRunAsync. After the channel closes, Err is non-nil
// on failure; otherwise Result is non-nil.
type AgentRunAsyncResult struct {
	Result *AgentRunResult
	Error  error
}

// AgentMode distinguishes how the agent is driven: human-in-the-loop versus self-directed runs.
// The string value is stable for configuration and fingerprints (see pkg/agent.WithAgentMode).
type AgentMode string

const (
	// AgentModeInteractive is the default: the agent expects user turns, approvals, or other
	// interactive signals between steps when the product requires them.
	AgentModeInteractive AgentMode = "interactive"
	// AgentModeAutonomous indicates a run where the agent proceeds without blocking on user input
	// for each step (subject to tool policy and limits).
	AgentModeAutonomous AgentMode = "autonomous"
)

// ToolExecutionMode specifies how tools are executed in parallel or sequentially.
type AgentToolExecutionMode string

const (
	// AgentToolExecutionModeParallel specifies that tools are executed in parallel.
	AgentToolExecutionModeParallel AgentToolExecutionMode = "parallel"
	// AgentToolExecutionModeSequential specifies that tools are executed sequentially.
	AgentToolExecutionModeSequential AgentToolExecutionMode = "sequential"
)
