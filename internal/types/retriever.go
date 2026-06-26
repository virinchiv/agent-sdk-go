package types

import (
	"fmt"
	"strings"
)

// RetrieverToolNamePrefix is the tool name prefix for agentic retriever tools (see [RetrieverToolName]).
const RetrieverToolNamePrefix = "retriever_"

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

// RetrieverToolName returns the registered tool name for a retriever key (e.g. "kb" → "retriever_kb").
// Returns "" when retrieverKey is empty after trim.
func RetrieverToolName(retrieverName string) string {
	name := strings.TrimSpace(retrieverName)
	if name == "" {
		return ""
	}
	return RetrieverToolNamePrefix + name
}

// RetrieverNameFromToolName extracts the retriever name from a retriever tool name.
// Returns ok false when toolName does not use [RetrieverToolNamePrefix] or the key is empty.
func RetrieverNameFromToolName(toolName string) (name string, ok bool) {
	toolName = strings.TrimSpace(toolName)
	if toolName == "" || !strings.HasPrefix(toolName, RetrieverToolNamePrefix) {
		return "", false
	}
	name = strings.TrimSpace(toolName[len(RetrieverToolNamePrefix):])
	if name == "" {
		return "", false
	}
	return name, true
}

// RetrieverToolDisplayName returns the human-readable tool name for a retriever key.
func RetrieverToolDisplayName(retrieverKey string) string {
	key := strings.TrimSpace(retrieverKey)
	if key == "" {
		return ""
	}
	return fmt.Sprintf("%s Retriever Tool", key)
}

func RetrieverToolParamQueryValue(args map[string]any) (string, error) {
	query, ok := args[RetrieverToolParamQuery].(string)
	if !ok {
		return "", fmt.Errorf("retriever tool: %q parameter required", RetrieverToolParamQuery)
	}
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("retriever tool: %q must be non-empty", RetrieverToolParamQuery)
	}
	return query, nil
}
