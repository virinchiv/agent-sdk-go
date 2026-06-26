package types

import "encoding/json"

// ToolSpec is the schema sent to the LLM for tool selection. Convert from Tool via ToolToSpec.
type ToolSpec struct {
	Name        string     `json:"name"`
	Description string     `json:"description"`
	Parameters  JSONSchema `json:"parameters"`
}

type JSONSchema map[string]any

func (s JSONSchema) MarshalJSON() ([]byte, error) {
	return json.Marshal(map[string]any(s))
}

// ToolKind classifies SDK-built tool wrappers. User-registered tools default to [ToolKindNative].
type ToolKind string

const (
	ToolKindNative    ToolKind = "native"
	ToolKindMCP       ToolKind = "mcp"
	ToolKindA2A       ToolKind = "a2a"
	ToolKindSubAgent  ToolKind = "sub_agent"
	ToolKindRetriever ToolKind = "retriever"
	ToolKindMemory    ToolKind = "memory"
)

// ToolKindProvider is implemented by SDK tool wrappers (MCP, A2A, sub-agent, retriever).
type ToolKindProvider interface {
	ToolKind() ToolKind
}

// KindOf returns the tool kind when t implements [ToolKindProvider], otherwise [ToolKindNative].
func KindOf(t any) ToolKind {
	if t == nil {
		return ToolKindNative
	}
	if k, ok := t.(ToolKindProvider); ok {
		if kind := k.ToolKind(); kind != "" {
			return kind
		}
	}
	return ToolKindNative
}

// CountsTowardToolTelemetry reports whether invocations of this kind belong in [ToolTelemetry].
func (k ToolKind) CountsTowardToolTelemetry() bool {
	switch k {
	case ToolKindSubAgent, ToolKindA2A, ToolKindRetriever:
		return false
	default:
		return true
	}
}

// HooksEligible reports whether [BeforeToolHook] and [AfterToolHook] run for this tool kind.
func (k ToolKind) HooksEligible() bool {
	return k == ToolKindNative || k == ToolKindMCP
}
