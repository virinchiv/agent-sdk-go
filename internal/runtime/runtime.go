// Package runtime defines internal execution contracts for agent backends (Temporal, in-process, etc.).
// SDK users do not import this package; pkg/agent wires implementations.
// If a file also imports the standard library "runtime" package, alias one import (e.g. agentrt ".../internal/runtime").
package runtime

import (
	"context"
	"errors"
	"time"

	"github.com/agenticenv/agent-sdk-go/internal/eventbus"
	"github.com/agenticenv/agent-sdk-go/internal/events"
	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

//go:generate mockgen -destination=./mocks/mock_runtime.go -package=mocks github.com/agenticenv/agent-sdk-go/internal/runtime Runtime
//go:generate mockgen -destination=./mocks/mock_worker_runtime.go -package=mocks github.com/agenticenv/agent-sdk-go/internal/runtime WorkerRuntime
//go:generate mockgen -destination=./mocks/mock_event_bus_runtime.go -package=mocks github.com/agenticenv/agent-sdk-go/internal/runtime EventBusRuntime

// ErrApprovalNotSupported is returned by Runtime.Approve when the runtime does not use token-based approval.
var ErrApprovalNotSupported = errors.New("runtime: approval not supported")

// Runtime executes agent runs against a backend.
type Runtime interface {
	// Execute runs one execution and returns the result. The agent package supplies approval via ExecuteRequest when needed.
	// Use WithTimeout or a context with deadline to avoid blocking.
	// When using conversation, pass the conversation ID on the request; agent and worker must use the same ID.
	// Agent identity is on req.AgentSpec.Name when AgentSpec is set.
	Execute(ctx context.Context, req *ExecuteRequest) (*types.AgentRunResult, error)

	// ExecuteStream starts the run and returns a channel of AgentEvent. Streams RUN_* lifecycle,
	// streaming assistant/tool/reasoning events, and CUSTOM approvals until RUN_FINISHED or RUN_ERROR ends the stream.
	// Delegated workflows may emit their own RUN_FINISHED; semantics for "root" completion are defined in pkg/agent.
	// After the terminal lifecycle event the channel may stay open briefly, then closes.
	// For approvals (tool or delegation), receive CUSTOM (AgentEventTypeCustom) events and use the agent
	// package approval path (e.g. OnApproval with the token from the custom payload).
	// When using conversation, pass the conversation ID on the request.
	// Agent identity is on req.AgentSpec.Name when AgentSpec is set.
	ExecuteStream(ctx context.Context, req *ExecuteRequest) (<-chan events.AgentEvent, error)

	// Approve completes a pending tool approval when the runtime uses out-of-band approval
	// (e.g. Temporal CompleteActivity). Returns ErrApprovalNotSupported if not applicable.
	Approve(ctx context.Context, approvalToken string, status types.ApprovalStatus) error

	// Close closes the runtime and releases resources.
	Close()
}

// WorkerRuntime is [Runtime] plus optional in-process task-queue polling (e.g. Temporal worker).
// [AgentWorker] and embedded local workers type-assert [Runtime] to WorkerRuntime for Start/Stop;
// backends that only act as clients implement [Runtime] but not this interface.
type WorkerRuntime interface {
	Runtime
	// Start begins polling; it typically blocks until Stop is called or ctx is cancelled.
	Start(ctx context.Context) error
	// Stop stops polling and releases worker resources.
	Stop()
}

// EventBusRuntime extends [Runtime] with in-process event bus access for sub-agent delegation and
// streaming fan-in. SDK backends (e.g. Temporal) implement it; [pkg/agent] asserts to it when wiring
// the agent tree. Custom [Runtime] implementations need only implement [Runtime] unless they participate
// in that fan-in.
type EventBusRuntime interface {
	Runtime
	SetEventBus(eventbus eventbus.EventBus)
	GetEventBus() eventbus.EventBus
}

// SubAgentToolParamQuery is the tool/JSON parameter name for the query sent to a sub-agent.
const SubAgentToolParamQuery = "query"

// SubAgentSpec describes one sub-agent in the delegation tree passed from pkg/agent to a runtime.
// The runtime builds its own internal routing structures from this tree; no runtime-specific fields
// are present here. ToolName is the sanitised tool name derived from Name and used as the map key.
type SubAgentSpec struct {
	Name     string  // human-readable agent name
	ToolName string  // tool name used to invoke this sub-agent (key in runtime route maps)
	Runtime  Runtime // the sub-agent's runtime instance
	Children []*SubAgentSpec
}

// AgentSpec describes agent identity and structured-output preferences for one run.
// It is attached to [ExecuteRequest.AgentSpec] so custom Runtime implementations can read name, prompts,
// and response format without importing pkg/agent.
type AgentSpec struct {
	// Name is a human-readable label (may include spaces). Runtimes may sanitize it when embedding in workflow IDs.
	Name           string
	Description    string
	SystemPrompt   string
	ResponseFormat *interfaces.ResponseFormat
}

// AgentExecution groups per-run execution inputs for custom Runtime implementations. Sub-structs
// stay stable so callers do not depend on a single flat blob that might be reshaped later.
// Temporal-backed runtimes typically use worker-local configuration for activities; this is a snapshot.
type AgentExecution struct {
	LLM        AgentLLM
	Tools      AgentTools
	Retrievers AgentRetrievers
	Session    AgentSession
	Limits     AgentLimits
}

// AgentRetrievers holds the retriever instances and mode for prefetch and hybrid RAG.
type AgentRetrievers struct {
	// Retrievers is the list of retriever instances registered with the agent.
	Retrievers []interfaces.Retriever
	// Mode is the retriever mode (agentic, prefetch, hybrid).
	Mode types.RetrieverMode
}

// LLMSampling is the runtime package name for per-run sampling options.
// It aliases [types.LLMSampling] so callers share one shape today; a distinct runtime type may replace this alias if the public runtime API needs different fields later.
type LLMSampling = types.LLMSampling

// AgentLLM is the LLM client and sampling overrides for this run.
type AgentLLM struct {
	Client   interfaces.LLMClient
	Sampling *LLMSampling
}

// AgentTools is registered tools, optional registry, and approval policy for this run.
type AgentTools struct {
	Tools          []interfaces.Tool
	Registry       interfaces.ToolRegistry
	ApprovalPolicy interfaces.AgentToolApprovalPolicy
}

// AgentSession is conversation storage and how many messages to include in LLM context.
type AgentSession struct {
	Conversation     interfaces.Conversation
	ConversationSize int
}

// AgentLimits caps iteration and wall-clock behavior for this run.
type AgentLimits struct {
	MaxIterations   int
	Timeout         time.Duration
	ApprovalTimeout time.Duration
}

// ExecuteRequest carries one execution request from Agent to Runtime.
//
// AgentSpec and AgentExecution are populated by pkg/agent from its configuration so implementations
// can read identity (including agent name on AgentSpec.Name), prompts, LLM, tools, and policies
// for this run. Implementations may ignore fields they do not use.
type ExecuteRequest struct {
	UserPrompt string `json:"user_prompt"`
	// RunOptions is the per-call options forwarded from pkg/agent (e.g. conversation session). May be nil.
	// Runtimes must use [base.GetConversationID] to safely extract the conversation ID rather than
	// accessing the nested fields directly, so the nil-check is centralised.
	RunOptions       *types.AgentRunOptions `json:"run_options,omitempty"`
	StreamingEnabled bool                   `json:"streaming_enabled"`
	// EventTypes filters streamed events; empty means default (implementation-defined, often all types).
	EventTypes       []events.AgentEventType `json:"event_types,omitempty"`
	SubAgents        []*SubAgentSpec         `json:"sub_agents,omitempty"`
	MaxSubAgentDepth int                     `json:"max_sub_agent_depth"`

	ApprovalHandler types.ApprovalHandler `json:"approval_handler"`

	// AgentSpec is identity and output-format metadata for this run (name, description, system prompt, response format).
	AgentSpec *AgentSpec `json:"agent_spec"`
	// AgentExecution is LLM, tools, conversation, sampling, and policy for this run.
	AgentExecution *AgentExecution `json:"agent_execution"`
}
