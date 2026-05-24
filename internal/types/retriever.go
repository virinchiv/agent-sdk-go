package types

// RetrieverToolParamQuery is the tool/JSON parameter name for the query sent to a retriever.
const RetrieverToolParamQuery = "query"

// RetrieverDocFormat is the printf format used to render a single [interfaces.Document] for LLM context.
// Arguments: 1-based index (int), content (string), source (string), score (float64).
const RetrieverDocFormat = "[%d] %s\n(source: %s, score: %.2f)\n\n"

// Default retriever settings.
const (
	DefaultTopK         = 5
	DefaultMinScore     = 0.75
	DefaultScheme       = "http"
	DefaultContentField = "content"
	DefaultSourceField  = "source"
)

// RetrieverMode selects how registered retrievers participate in agent runs.
// String values are stable for configuration (see pkg/agent.WithRetrieverMode).
type RetrieverMode string

const (
	// RetrieverModeAgentic is the default: the agent decides when to query retrievers (e.g. via tools).
	RetrieverModeAgentic RetrieverMode = "agentic"
	// RetrieverModePrefetch runs retrievers before the first LLM call and injects context up front.
	RetrieverModePrefetch RetrieverMode = "prefetch"
	// RetrieverModeHybrid combines prefetch with agentic retrieval during the run.
	RetrieverModeHybrid RetrieverMode = "hybrid"
)
