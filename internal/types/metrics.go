package types

// Metric name constants for the agent SDK. All runtime implementations emit these names
// so dashboards and alerts work regardless of the underlying execution engine.
//
// Counters: call Metrics.IncrementCounter. Histograms: call Metrics.RecordHistogram.
// Latency histograms are in milliseconds; token histograms are raw counts.
const (
	// Agent API — emitted by Agent.Run / Agent.RunAsync.
	MetricRunStarted    = "agent.run.started"
	MetricRunCompleted  = "agent.run.completed"
	MetricRunFailed     = "agent.run.failed"
	MetricRunDurationMs = "agent.run.duration_ms"

	// Agent API — emitted by Agent.Stream (dispatch phase only).
	MetricStreamStarted    = "agent.stream.started"
	MetricStreamDispatched = "agent.stream.dispatched"
	MetricStreamFailed     = "agent.stream.failed"
	MetricStreamDurationMs = "agent.stream.duration_ms"

	// Runtime — emitted per LLM Generate / GenerateStream call.
	MetricLLMCallStarted   = "agent.llm.call.started"
	MetricLLMCallCompleted = "agent.llm.call.completed"
	MetricLLMCallFailed    = "agent.llm.call.failed"

	// Runtime — token usage, recorded when provider returns non-nil LLMUsage.
	MetricLLMTokensInput  = "agent.llm.tokens.input"
	MetricLLMTokensOutput = "agent.llm.tokens.output"

	// Runtime — LLM wall-clock latency.
	MetricLLMLatencyMs = "agent.llm.latency_ms"

	// Runtime — emitted per tool.Execute call.
	MetricToolCallStarted   = "agent.tool.call.started"
	MetricToolCallCompleted = "agent.tool.call.completed"
	MetricToolCallFailed    = "agent.tool.call.failed"

	// Runtime — tool wall-clock latency.
	MetricToolLatencyMs = "agent.tool.latency_ms"

	// Runtime — emitted per retriever.Search call (prefetch and hybrid modes).
	MetricRetrieverCallStarted   = "agent.retriever.call.started"
	MetricRetrieverCallCompleted = "agent.retriever.call.completed"
	MetricRetrieverCallFailed    = "agent.retriever.call.failed"

	// Runtime — retriever search wall-clock latency.
	MetricRetrieverLatencyMs = "agent.retriever.latency_ms"

	// Attribute keys used on both metrics and spans.
	MetricAttrModel     = "model"
	MetricAttrProvider  = "provider"
	MetricAttrTool      = "tool"
	MetricAttrRetriever = "retriever"
)
