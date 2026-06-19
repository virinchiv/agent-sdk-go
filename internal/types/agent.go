package types

// AgentRunOptions holds per-call options passed to [Agent.Run], [Agent.RunAsync], and [Agent.Stream].
// A nil pointer is valid and means no options (no conversation, default behaviour).
// Add new per-call knobs here as nested option structs; keep agent-level settings on [agentConfig].
type AgentRunOptions struct {
	// ConversationOptions selects a conversation session for this call.
	// Required when the agent was configured with WithConversation; must be nil otherwise.
	ConversationOptions *ConversationOptions `json:"conversation_options,omitempty"`
}

// ConversationOptions identifies a conversation session for one call.
// ID must be a non-empty, stable string that is the same across all turns of a session
// (e.g. a user or chat ID). The agent loads history for this ID before the LLM call
// and persists the new messages after it completes.
type ConversationOptions struct {
	ID string
}

// AgentRunResult is the structured result of a completed run (content, model, metadata).
type AgentRunResult struct {
	Content   string         `json:"content"`
	AgentName string         `json:"agent_name"`
	Model     string         `json:"model"`
	Metadata  map[string]any `json:"metadata"`
	// Usage is the sum of token usage across all LLM calls in this run (when reported by the provider).
	// Usage acts as the historical root for aggregated token counters.
	LLMUsage *LLMUsage `json:"llm_usage,omitempty"`

	// Telemetry contains the strongly typed nested metrics domain payload.
	Telemetry *AgentTelemetry `json:"telemetry,omitempty"`
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
