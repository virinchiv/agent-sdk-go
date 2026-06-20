package base

import (
	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

const scopeKeyParentAgentID = "parent_agent_id"

// LLMResult is the result of a successful LLM call.
// Content holds the assistant text; ToolCalls holds any tool invocations resolved against
// the registered tools list (NeedsApproval pre-computed from the approval policy).
type LLMResult struct {
	Content   string
	ToolCalls []ToolCallRequest
	Usage     *interfaces.LLMUsage
}

// ToolCallRequest describes one tool call returned by the LLM.
// NeedsApproval is pre-computed from the tool approval policy so orchestration loops
// (local agent loop, temporal workflow) do not need to re-evaluate the policy.
type ToolCallRequest struct {
	ToolCallID      string
	ToolName        string
	ToolDisplayName string
	ToolKind        types.ToolKind
	Args            map[string]any
	NeedsApproval   bool
}

// AuthorizeResult is the outcome of a programmatic tool authorization check.
// When Allowed is false, Reason carries the denial message for logging/events.
type AuthorizeResult struct {
	Allowed bool
	Reason  string
}

// RetrieverResult is the outcome of ExecuteRetrievers (prefetch / hybrid pre-loop).
type RetrieverResult struct {
	Context        string
	TotalSearches  int64
	FailedSearches int64
}

// MemoryResult is the outcome of ExecuteMemoryRecall.
type MemoryResult struct {
	Context       string
	TotalRecalls  int64
	FailedRecalls int64
}
