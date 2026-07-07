package temporal

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/agenticenv/agent-sdk-go/internal/events"
	agentrt "github.com/agenticenv/agent-sdk-go/internal/runtime"
	"github.com/agenticenv/agent-sdk-go/internal/runtime/base"
	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/google/uuid"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

var (
	// Heartbeat for long LLM stream / tool execute: fail stuck attempts soon after worker loss (<< StartToClose).
	agentLongActivityHeartbeatTimeout  time.Duration = 30 * time.Second
	agentLongActivityHeartbeatInterval time.Duration = 10 * time.Second

	agentToolApprovalActivityMaxAttempts int32 = 1

	sendEventActivityTaskTimeout time.Duration = 15 * time.Second
	sendEventActivityMaxAttempts int32         = 1

	// updateWorkflowEventRPCTimeout caps UpdateWorkflow for normal events (Accepted). When the event worker
	// or process is gone, fail fast instead of blocking until sendEventActivityTaskTimeout. Must be < sendEventActivityTaskTimeout.
	updateWorkflowEventRPCTimeout = 10 * time.Second
	// updateWorkflowApprovalRPCTimeout caps UpdateWorkflow when posting approval to the event pipeline (Completed).
	// Only the "deliver to event workflow handler" phase; must be far below approval activity StartToClose.
	updateWorkflowApprovalRPCTimeout = 30 * time.Second

	// AgentWorkflow uses ContinueAsNew when Temporal execution history crosses these bounds (see loop below).
	// Checked after each tool round (when tool results are appended). Not evaluated on the “LLM returned no tools”
	// exit path in the same iteration. Byte limit is tighter than the event pipeline because LLM payloads are large.
	agentWorkflowHistoryLength    = 10_000
	agentWorkflowHistorySizeBytes = 20_000_000
)

// User-facing tool results when approval is required.
const (
	msgToolRejected            = "Tool execution was rejected by the user."
	msgToolApprovalUnavailable = "Tool approval could not be completed because the event stream is unavailable; continuing without running the tool."
	msgToolUnauthorized        = "Tool execution was denied by authorization policy."
)

// SendAgentEventResult is returned by SendAgentEventUpdateActivity. Fatal errors are returned as activity error;
// StreamUnavailable is a soft failure: workflow sets streamingUnavailable and continues.
type SendAgentEventResult struct {
	// StreamUnavailable is true when delivery failed in a way that likely means the stream is gone.
	StreamUnavailable bool `json:"stream_unavailable,omitempty"`
}

// sendAgentEventWorkflowUpdate sends one update to AgentEventWorkflow using UpdateWithStartWorkflow so the
// event workflow is started lazily on first use (no separate ExecuteWorkflow). USE_EXISTING applies the update
// when a run is already active. Bounded RPC deadline; errors are mapped to soft failure by callers.
// Use WorkflowUpdateStageAccepted for token traffic; WorkflowUpdateStageCompleted for approval so the handler
// has returned before AgentToolApprovalActivity blocks on ErrResultPending.
func (rt *TemporalRuntime) sendAgentEventWorkflowUpdate(ctx context.Context, eventWorkflowID, eventTaskQueue string, upd *AgentEventUpdate, stage client.WorkflowUpdateStage, rpcTimeout time.Duration) error {
	rpcCtx, cancel := context.WithTimeout(ctx, rpcTimeout)
	defer cancel()

	startOp := rt.temporalClient.NewWithStartWorkflowOperation(
		client.StartWorkflowOptions{
			ID:                       eventWorkflowID,
			TaskQueue:                eventTaskQueue,
			WorkflowIDConflictPolicy: enumspb.WORKFLOW_ID_CONFLICT_POLICY_USE_EXISTING,
			WorkflowIDReusePolicy:    enumspb.WORKFLOW_ID_REUSE_POLICY_ALLOW_DUPLICATE,
		},
		rt.AgentEventWorkflow,
	)
	_, err := rt.temporalClient.UpdateWithStartWorkflow(rpcCtx, client.UpdateWithStartWorkflowOptions{
		StartWorkflowOperation: startOp,
		UpdateOptions: client.UpdateWorkflowOptions{
			WorkflowID:   eventWorkflowID,
			UpdateName:   agentEventName,
			Args:         []interface{}{upd},
			WaitForStage: stage,
		},
	})
	return err
}

// AgentWorkflowInput is the input to AgentWorkflow. EventWorkflowID is set when streaming or approval is used.
// StreamingEnabled enables partial content streaming (from WithStream).
// ConversationID is set when conversation is used; workflow fetches messages and writes assistant/tool via activities.
// SubAgentDepth is 0 for a top-level user run; each child workflow increments it (runtime cap vs maxSubAgentDepth).
// SubAgentRoutes maps sub-agent tool name -> route; built from WithSubAgents when the run starts.
// MemoryScope is resolved before the workflow starts and passed through for recall/store activities.
// LocalChannelName is the in-process pub/sub channel name used for in-memory event fan-in across the
// delegation tree. Set once at the top level (agent_event_<main-workflow-id>) and propagated unchanged
// to all sub-agents. Contrast with EventWorkflowID which is used for out-of-process (remote) routing.
// EventTaskQueue is the Temporal task queue for AgentEventWorkflow (e.g. main TaskQueue + "-events"); required
// for UpdateWithStartWorkflow when EventWorkflowID is set.
// EventTypes is set by the SDK; a single "*" element means emit all event kinds (used for Stream).
// AgentFingerprint is the per-run digest (config + resolved tools). Caller and worker compute it at resolve time.
type AgentWorkflowInput struct {
	UserPrompt       string                   `json:"user_prompt,omitempty"`
	EventWorkflowID  string                   `json:"event_workflow_id,omitempty"`
	EventTaskQueue   string                   `json:"event_task_queue,omitempty"`
	LocalChannelName string                   `json:"local_channel_name,omitempty"`
	StreamingEnabled bool                     `json:"streaming_enabled,omitempty"`
	ConversationID   string                   `json:"conversation_id,omitempty"`
	AgentFingerprint string                   `json:"agent_fingerprint,omitempty"`
	RunID            string                   `json:"run_id,omitempty"`
	MemoryScope      interfaces.MemoryScope   `json:"memory_scope,omitempty"`
	EventTypes       []events.AgentEventType  `json:"event_types,omitempty"`
	SubAgentDepth    int                      `json:"sub_agent_depth,omitempty"`
	SubAgentRoutes   map[string]SubAgentRoute `json:"sub_agent_routes,omitempty"`
	MaxSubAgentDepth int                      `json:"max_sub_agent_depth,omitempty"`
	State            *AgentWorkflowState      `json:"state,omitempty"`
}

// AgentWorkflowState is the state of the agent workflow.
// It is used to store the state of the agent workflow on continue-as-new.
type AgentWorkflowState struct {
	Iteration int                   `json:"iteration"`
	Messages  []interfaces.Message  `json:"messages"`
	LLMUsage  *interfaces.LLMUsage  `json:"llm_usage,omitempty"`
	Telemetry *types.AgentTelemetry `json:"telemetry,omitempty"`
}

// AgentRetrieverInput is the input to AgentRetrieverActivity.
type AgentRetrieverInput struct {
	AgentFingerprint string `json:"agent_fingerprint,omitempty"`
	RunID            string `json:"run_id,omitempty"`
	UserPrompt       string `json:"user_prompt"`
}

// AgentRetrieverResult is the return value of AgentRetrieverActivity.
// RetrieverContext is the combined, formatted document context from all retrievers; empty when no
// documents were found. It is injected into the system prompt by AgentLLMActivity and AgentLLMStreamActivity.
type AgentRetrieverResult struct {
	RetrieverContext string `json:"retriever_context,omitempty"`
	TotalSearches    int64  `json:"total_searches,omitempty"`
	FailedSearches   int64  `json:"failed_searches,omitempty"`
}

// AgentMemoryRecallInput is the input to AgentMemoryRecallActivity.
type AgentMemoryRecallInput struct {
	AgentFingerprint string                 `json:"agent_fingerprint,omitempty"`
	RunID            string                 `json:"run_id,omitempty"`
	UserPrompt       string                 `json:"user_prompt"`
	MemoryScope      interfaces.MemoryScope `json:"memory_scope,omitempty"`
}

// AgentMemoryRecallResult is the return value of AgentMemoryRecallActivity.
type AgentMemoryRecallResult struct {
	MemoryContext string `json:"memory_context,omitempty"`
	TotalRecalls  int64  `json:"total_recalls,omitempty"`
	FailedRecalls int64  `json:"failed_recalls,omitempty"`
}

// AgentMemoryStoreInput is the input to AgentMemoryStoreActivity.
type AgentMemoryStoreInput struct {
	AgentFingerprint string                 `json:"agent_fingerprint,omitempty"`
	RunID            string                 `json:"run_id,omitempty"`
	MemoryScope      interfaces.MemoryScope `json:"memory_scope,omitempty"`
	Messages         []interfaces.Message   `json:"messages,omitempty"`
}

// AgentLLMInput is the input to AgentLLMActivity and AgentLLMStreamActivity.
// When ConversationID is set, the activity loads history from the store. MessageID is the assistant text id
// for TEXT_MESSAGE_* (and stream ordering with REASONING_*); the workflow sets it each turn.
// RetrieverContext is the pre-fetched RAG context from AgentRetrieverActivity (prefetch / hybrid modes).
// MemoryContext is the pre-fetched long-term memory context from AgentMemoryRecallActivity.
type AgentLLMInput struct {
	AgentName        string               `json:"agent_name,omitempty"`
	ConversationID   string               `json:"conversation_id,omitempty"`
	Messages         []interfaces.Message `json:"messages,omitempty"`
	SkipTools        bool                 `json:"skip_tools,omitempty"`
	AgentFingerprint string               `json:"agent_fingerprint,omitempty"`
	MessageID        string               `json:"message_id,omitempty"`
	EventWorkflowID  string               `json:"event_workflow_id,omitempty"`
	EventTaskQueue   string               `json:"event_task_queue,omitempty"`
	LocalChannelName string               `json:"local_channel_name,omitempty"`
	MemoryContext    string               `json:"memory_context,omitempty"`
	RetrieverContext string               `json:"retriever_context,omitempty"`
	RunID            string               `json:"run_id,omitempty"`
	Iteration        int                  `json:"iteration,omitempty"`
}

// AgentLLMResult is the return value of AgentLLMActivity. Workflow uses it to decide: return content or execute tools.
type AgentLLMResult struct {
	Content   string               `json:"content"`
	ToolCalls []ToolCallRequest    `json:"tool_calls"`
	Usage     *interfaces.LLMUsage `json:"usage,omitempty"`
}

// baseLLMResultToActivity converts a [base.LLMResult] (no JSON tags) to an [AgentLLMResult]
// (with JSON tags required for Temporal serialization). ToolCallRequests are copied field by field
// so the two types stay independent (temporal adds JSON tags, base does not).
func baseLLMResultToActivity(r *base.LLMResult) *AgentLLMResult {
	out := &AgentLLMResult{
		Content: r.Content,
		Usage:   base.CloneLLMUsage(r.Usage),
	}
	for _, tc := range r.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, ToolCallRequest{
			ToolCallID:      tc.ToolCallID,
			ToolName:        tc.ToolName,
			ToolDisplayName: tc.ToolDisplayName,
			ToolKind:        tc.ToolKind,
			Args:            tc.Args,
			NeedsApproval:   tc.NeedsApproval,
		})
	}
	return out
}

// ToolCallRequest is a tool invocation with approval flag. NeedsApproval is set by AgentLLMActivity.
type ToolCallRequest struct {
	ToolCallID      string         `json:"tool_call_id"` // from LLM; used to match tool results
	ToolName        string         `json:"tool_name"`
	ToolDisplayName string         `json:"tool_display_name,omitempty"`
	ToolKind        types.ToolKind `json:"tool_kind"`
	Args            map[string]any `json:"args"`
	NeedsApproval   bool           `json:"needs_approval"`
}

// agentToolCallInput bundles the workflow handle, per-iteration activity contexts, and emit plumbing for tool execution.
// Built once per sequential LLM tool round, or once per parallel branch (unique parallelSlot activity IDs).
type agentToolCallInput struct {
	wfCtx         workflow.Context
	input         AgentWorkflowInput
	messageID     string
	iteration     int
	emitEvent     func(events.AgentEvent) error
	authorizeCtx  workflow.Context
	approvalCtx   workflow.Context
	activityScope string
	policies      agentrt.ExecutionPolicies
}

// agentToolCallOutput is the output of executeAgentToolCall.
type agentToolCallOutput struct {
	msg                  interfaces.Message
	failed               bool // true: hard err or ExecuteTool err
	streamingUnavailable bool
}

// agentToolResult is one tool outcome collected for the conversation and telemetry.
type agentToolResult struct {
	message interfaces.Message
	failed  bool
}

// AgentToolExecuteInput is the input to AgentToolExecuteActivity.
type AgentToolExecuteInput struct {
	ToolName         string                 `json:"tool_name"`
	Args             map[string]any         `json:"args"`
	ConversationID   string                 `json:"conversation_id,omitempty"`
	Messages         []interfaces.Message   `json:"messages,omitempty"`
	ToolCallID       string                 `json:"tool_call_id,omitempty"`
	RunID            string                 `json:"run_id,omitempty"`
	Iteration        int                    `json:"iteration,omitempty"`
	AgentFingerprint string                 `json:"agent_fingerprint,omitempty"`
	MemoryScope      interfaces.MemoryScope `json:"memory_scope,omitempty"`
}

type AgentToolApprovalInput struct {
	AgentName        string         `json:"agent_name"`
	ToolCallID       string         `json:"tool_call_id"`
	ToolName         string         `json:"tool_name"`
	ToolDisplayName  string         `json:"tool_display_name,omitempty"`
	Args             map[string]any `json:"args"`
	EventWorkflowID  string         `json:"event_workflow_id"`
	EventTaskQueue   string         `json:"event_task_queue,omitempty"`
	LocalChannelName string         `json:"local_channel_name,omitempty"`
	SubAgentName     string         `json:"sub_agent_name,omitempty"`
	AgentFingerprint string         `json:"agent_fingerprint,omitempty"`
}

type AgentToolAuthorizeInput struct {
	ToolName         string         `json:"tool_name"`
	Args             map[string]any `json:"args"`
	ToolCallID       string         `json:"tool_call_id"`
	AgentFingerprint string         `json:"agent_fingerprint,omitempty"`
}

type AgentToolAuthorizeResult struct {
	Allowed bool   `json:"allowed"`
	Reason  string `json:"reason,omitempty"`
}

// SendAgentEventActivityInput is the payload for SendAgentEventUpdateActivity (workflow + activity).
type SendAgentEventActivityInput struct {
	EventWorkflowID string                `json:"event_workflow_id,omitempty"`
	EventTaskQueue  string                `json:"event_task_queue,omitempty"`
	EventType       events.AgentEventType `json:"event_type"`
	Update          *AgentEventUpdate     `json:"update"`
}

// AddConversationMessagesInput is the input to AddConversationMessagesActivity.
type AddConversationMessagesInput struct {
	ConversationID   string               `json:"conversation_id,omitempty"`
	Messages         []interfaces.Message `json:"messages,omitempty"`
	AgentFingerprint string               `json:"agent_fingerprint,omitempty"`
}

// AgentWorkflow runs the agent loop: LLM → tool calls → approval/execute → feed results back to LLM → repeat.
// Stops when LLM returns no tool calls, or max iterations reached.
// When Input.EventWorkflowID is set, sends agent events and approval requests to the event workflow.
// ContinueAsNew: when workflow history length or size (GetInfo) exceeds agentWorkflowHistory*, after tool
// results are merged into messages for that iteration; carries AgentWorkflowState (iteration + messages) forward.
func (rt *TemporalRuntime) AgentWorkflow(ctx workflow.Context, input AgentWorkflowInput) (*types.AgentRunResult, error) {
	logger := workflow.GetLogger(ctx)
	logger.Info("workflow: agent run started", "scope", "workflow")
	if n := len(input.SubAgentRoutes); n > 0 {
		logger.Debug("workflow: sub-agent routes snapshot",
			"scope", "workflow",
			"routeCount", n,
			"subAgentDepth", input.SubAgentDepth)
	}
	eventWorkflowID := input.EventWorkflowID
	eventTaskQueue := input.EventTaskQueue
	agentName := rt.AgentSpec.Name
	model := rt.AgentConfig.LLM.Client.GetModel()

	maxIter := rt.AgentConfig.Limits.MaxIterations
	policies := rt.executionPolicies()

	var activityIDSuffix string
	err := workflow.SideEffect(ctx, func(ctx workflow.Context) interface{} {
		return uuid.New().String()
	}).Get(&activityIDSuffix)
	if err != nil {
		return nil, err
	}

	llmActCtx := workflow.WithActivityOptions(ctx, execActivityOptions(policies.LLM, "AgentLLMActivity_"+activityIDSuffix, false))
	streamActCtx := workflow.WithActivityOptions(ctx, execActivityOptions(policies.LLM, "AgentLLMStreamActivity_"+activityIDSuffix, true))

	sendEventCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		ActivityID:          "SendAgentEventUpdateActivity_" + activityIDSuffix,
		StartToCloseTimeout: sendEventActivityTaskTimeout,
		RetryPolicy:         retryPolicy(sendEventActivityMaxAttempts),
	})
	convCtx := workflow.WithActivityOptions(ctx, execActivityOptions(policies.Conversation, "ConversationActivity_"+activityIDSuffix, false))
	retrieverActCtx := workflow.WithActivityOptions(ctx, execActivityOptions(policies.Retriever, "AgentRetrieverActivity_"+activityIDSuffix, false))
	memoryActCtx := workflow.WithActivityOptions(ctx, execActivityOptions(policies.Memory, "AgentMemoryActivity_"+activityIDSuffix, false))

	var streamingUnavailable bool
	// emitAgentEvent must use wfCtx (the coroutine that calls Get) for ExecuteActivity().Get — not the root
	// workflow ctx — or parallel tool branches panic: "wrong Context is used to do blocking call".
	emitAgentEvent := func(wfCtx workflow.Context, ev events.AgentEvent) error {
		if ev == nil {
			return nil
		}
		eventTypes := input.EventTypes
		if len(eventTypes) == 0 {
			return nil
		}
		if streamingUnavailable {
			return nil
		}
		emit := false
		for _, et := range eventTypes {
			if et == events.AgentEventAll {
				emit = true
				break
			}
			if et == ev.Type() {
				emit = true
				break
			}
		}
		if !emit {
			return nil
		}
		eventBytes, _ := ev.ToJSON()
		upd := &AgentEventUpdate{
			AgentName:        agentName,
			LocalChannelName: input.LocalChannelName,
			EventJSON:        json.RawMessage(eventBytes),
		}
		var res SendAgentEventResult
		actIn := SendAgentEventActivityInput{
			EventWorkflowID: eventWorkflowID,
			EventTaskQueue:  eventTaskQueue,
			EventType:       ev.Type(),
			Update:          upd,
		}
		if err := workflow.ExecuteActivity(sendEventCtx, rt.SendAgentEventUpdateActivity, actIn).Get(wfCtx, &res); err != nil {
			return err
		}
		if res.StreamUnavailable {
			streamingUnavailable = true
		}
		return nil
	}

	useStreaming := input.StreamingEnabled && rt.AgentConfig.LLM.Client.IsStreamSupported()

	// State restored after ContinueAsNew (iteration, messages, run telemetry).
	if input.State == nil {
		input.State = &AgentWorkflowState{
			Iteration: 0,
			Messages:  []interfaces.Message{{Role: interfaces.MessageRoleUser, Content: input.UserPrompt}},
		}
	}
	if input.State.Telemetry == nil {
		input.State.Telemetry = base.NewAgentTelemetry(workflow.Now(ctx))
	}
	telemetry := input.State.Telemetry

	llmUsage := input.State.LLMUsage

	messages := input.State.Messages

	memoryContext := ""
	if rt.MemoryConfigured() && rt.RecallEnabled() {
		logger.Debug("workflow: memory recall started", "scope", "workflow")
		var memoryResult AgentMemoryRecallResult
		if err := workflow.ExecuteActivity(memoryActCtx, rt.AgentMemoryRecallActivity, AgentMemoryRecallInput{
			AgentFingerprint: input.AgentFingerprint,
			RunID:            input.RunID,
			UserPrompt:       input.UserPrompt,
			MemoryScope:      input.MemoryScope,
		}).Get(memoryActCtx, &memoryResult); err != nil {
			if temporal.IsCanceledError(err) {
				return nil, err
			}
			return nil, err
		}
		memoryContext = memoryResult.MemoryContext
		telemetry.Storage.TotalMemoryRecalls += memoryResult.TotalRecalls
		telemetry.Storage.FailedMemoryRecalls += memoryResult.FailedRecalls
		logger.Debug("workflow: memory recall done", "scope", "workflow", "hasContext", memoryContext != "")
	}

	// Pre-fetch retrieval context once before the first LLM call (prefetch and hybrid modes).
	// The resulting retrieverContext is forwarded to every AgentLLMInput in the run so the LLM always
	// sees the retrieved documents in its system prompt, regardless of the number of iterations.
	retrieverContext := ""
	retrieverMode := rt.AgentConfig.Retrievers.Mode
	if (retrieverMode == types.RetrieverModePrefetch || retrieverMode == types.RetrieverModeHybrid) &&
		len(rt.AgentConfig.Retrievers.Retrievers) > 0 {
		logger.Debug("workflow: retriever prefetch started", "scope", "workflow", "retrieverMode", string(retrieverMode), "retrieverCount", len(rt.AgentConfig.Retrievers.Retrievers))
		retrieverInput := AgentRetrieverInput{
			AgentFingerprint: input.AgentFingerprint,
			RunID:            input.RunID,
			UserPrompt:       input.UserPrompt,
		}
		var retrieverResult AgentRetrieverResult
		if err := workflow.ExecuteActivity(retrieverActCtx, rt.AgentRetrieverActivity, retrieverInput).Get(retrieverActCtx, &retrieverResult); err != nil {
			if temporal.IsCanceledError(err) {
				return nil, err
			}
			return nil, err
		}
		retrieverContext = retrieverResult.RetrieverContext
		telemetry.Storage.TotalRetrieverSearches += retrieverResult.TotalSearches
		telemetry.Storage.FailedRetrieverSearches += retrieverResult.FailedSearches
		telemetry.Storage.PrefetchSearches += retrieverResult.TotalSearches
		logger.Debug("workflow: retriever prefetch done", "scope", "workflow", "hasContext", retrieverContext != "")
	}

	lastContent := ""
	var llmResult AgentLLMResult
	for iter := input.State.Iteration; iter < maxIter; iter++ {

		messageID := uuid.New().String()

		llmInput := AgentLLMInput{
			AgentName:        agentName,
			ConversationID:   input.ConversationID,
			Messages:         messages,
			AgentFingerprint: input.AgentFingerprint,
			MessageID:        messageID,
			RunID:            input.RunID,
			Iteration:        iter,
			EventWorkflowID:  eventWorkflowID,
			EventTaskQueue:   eventTaskQueue,
			LocalChannelName: input.LocalChannelName,
			MemoryContext:    memoryContext,
			RetrieverContext: retrieverContext,
		}

		if useStreaming {
			err = workflow.ExecuteActivity(streamActCtx, rt.AgentLLMStreamActivity, llmInput).Get(streamActCtx, &llmResult)
		} else {
			err = workflow.ExecuteActivity(llmActCtx, rt.AgentLLMActivity, llmInput).Get(llmActCtx, &llmResult)
		}
		if err != nil {
			if temporal.IsCanceledError(err) {
				return nil, err
			}
			return nil, err
		}

		telemetry.Run.TotalLLMCalls++
		llmUsage = base.MergeLLMUsage(llmUsage, llmResult.Usage)

		if len(llmResult.ToolCalls) == 0 {
			// Final response: accumulate assistant message for conversation
			messages = append(messages, interfaces.Message{Role: interfaces.MessageRoleAssistant, Content: llmResult.Content})
			lastContent = llmResult.Content
			break
		}

		if iter == maxIter-1 {
			logger.Info("workflow: max iterations reached, final LLM round without tools", "scope", "workflow", "iteration", iter)
			if useStreaming {
				llmInput.SkipTools = true
				err = workflow.ExecuteActivity(streamActCtx, rt.AgentLLMStreamActivity, llmInput).Get(streamActCtx, &llmResult)
			} else {
				llmInput.SkipTools = true
				err = workflow.ExecuteActivity(llmActCtx, rt.AgentLLMActivity, llmInput).Get(llmActCtx, &llmResult)
			}
			if err != nil {
				if temporal.IsCanceledError(err) {
					return nil, err
				}
				return nil, err
			}
			llmUsage = base.MergeLLMUsage(llmUsage, llmResult.Usage)
			messages = append(messages, interfaces.Message{Role: interfaces.MessageRoleAssistant, Content: llmResult.Content})
			lastContent = llmResult.Content
			telemetry.Run.TotalLLMCalls++
			telemetry.Run.FinishReason = types.FinishReasonMaxIterations
			break
		}

		// Accumulate assistant message for next iteration
		assistantMsg := interfaces.Message{
			Role:      interfaces.MessageRoleAssistant,
			Content:   llmResult.Content,
			ToolCalls: make([]*interfaces.ToolCall, len(llmResult.ToolCalls)),
		}
		for i, tr := range llmResult.ToolCalls {
			assistantMsg.ToolCalls[i] = &interfaces.ToolCall{
				ToolCallID: tr.ToolCallID,
				ToolName:   tr.ToolName,
				Args:       tr.Args,
			}
		}
		messages = append(messages, assistantMsg)

		var toolResults []agentToolResult

		toolExecMode := rt.ToolExecutionMode
		if toolExecMode == "" {
			toolExecMode = types.AgentToolExecutionModeParallel
		}
		switch toolExecMode {
		case types.AgentToolExecutionModeParallel:
			{
				logger.Info("workflow: tool execution (parallel)",
					"scope", "workflow",
					"executionMode", string(types.AgentToolExecutionModeParallel),
					"toolCount", len(llmResult.ToolCalls))

				futures := make([]workflow.Future, len(llmResult.ToolCalls))
				for i := range llmResult.ToolCalls {
					i := i
					tc := llmResult.ToolCalls[i]
					logger.Debug("workflow: parallel tool branch scheduled",
						"scope", "workflow",
						"toolIndex", i,
						"toolName", tc.ToolName,
						"toolCallID", tc.ToolCallID)
					fut, settable := workflow.NewFuture(ctx)
					futures[i] = fut

					workflow.Go(ctx, func(gCtx workflow.Context) {
						gLog := workflow.GetLogger(gCtx)
						gLog.Debug("workflow: parallel tool branch started",
							"scope", "workflow",
							"toolIndex", i,
							"toolName", tc.ToolName,
							"toolCallID", tc.ToolCallID)
						slot := strconv.Itoa(i)
						parallelInput := rt.newAgentToolCallInput(gCtx, input, activityIDSuffix, messageID, iter, emitAgentEvent, slot)
						toolOutput, runErr := rt.executeAgentToolCall(parallelInput, tc, streamingUnavailable)
						if runErr != nil {
							gLog.Debug("workflow: parallel tool branch finished with error",
								"scope", "workflow",
								"toolIndex", i,
								"toolName", tc.ToolName,
								"toolCallID", tc.ToolCallID,
								"error", runErr)
							settable.Set(nil, runErr)
							return
						}
						// executeAgentToolCall returns (nil, err) or (non-nil *agentToolCallOutput, nil) only.
						gLog.Debug("workflow: parallel tool branch finished ok",
							"scope", "workflow",
							"toolIndex", i,
							"toolName", tc.ToolName,
							"toolCallID", tc.ToolCallID)
						settable.Set(toolOutput, nil)
					})
				}

				toolResults = make([]agentToolResult, len(futures))
				for i, fut := range futures {
					tc := llmResult.ToolCalls[i]
					var v *agentToolCallOutput
					err := fut.Get(ctx, &v)

					if err != nil {
						logger.Debug("workflow: parallel tool future collected (error → synthetic tool message)",
							"scope", "workflow",
							"toolIndex", i,
							"toolName", tc.ToolName,
							"toolCallID", tc.ToolCallID,
							"error", err)
						toolResults[i] = agentToolResult{
							message: interfaces.Message{
								Role:       interfaces.MessageRoleTool,
								Content:    "Tool execution failed: " + err.Error(),
								ToolName:   tc.ToolName,
								ToolCallID: tc.ToolCallID,
							},
							failed: true,
						}
					} else {
						logger.Debug("workflow: parallel tool future collected (ok)",
							"scope", "workflow",
							"toolIndex", i,
							"toolName", tc.ToolName,
							"toolCallID", tc.ToolCallID,
							"streamingUnavailable", v.streamingUnavailable)
						toolResults[i] = agentToolResult{
							message: v.msg,
							failed:  v.failed,
						}
						if v.streamingUnavailable {
							streamingUnavailable = true
						}
					}
				}
			}
		case types.AgentToolExecutionModeSequential:
			{
				logger.Info("workflow: tool execution (sequential)",
					"scope", "workflow",
					"executionMode", string(types.AgentToolExecutionModeSequential),
					"toolCount", len(llmResult.ToolCalls))
				toolInput := rt.newAgentToolCallInput(ctx, input, activityIDSuffix, messageID, iter, emitAgentEvent, "")
				toolResults = make([]agentToolResult, len(llmResult.ToolCalls))
				for i, tc := range llmResult.ToolCalls {
					logger.Debug("workflow: sequential tool executing",
						"scope", "workflow",
						"toolIndex", i,
						"toolName", tc.ToolName,
						"toolCallID", tc.ToolCallID,
						"toolCount", len(llmResult.ToolCalls))
					toolOutput, runErr := rt.executeAgentToolCall(toolInput, tc, streamingUnavailable)
					if runErr != nil {
						logger.Info("workflow: sequential tool failed",
							"scope", "workflow",
							"toolIndex", i,
							"toolName", tc.ToolName,
							"toolCallID", tc.ToolCallID,
							"error", runErr)
						toolResults[i] = agentToolResult{
							message: interfaces.Message{
								Role:       interfaces.MessageRoleTool,
								Content:    "Tool execution failed: " + runErr.Error(),
								ToolName:   tc.ToolName,
								ToolCallID: tc.ToolCallID,
							},
							failed: true,
						}
						continue
					}
					if toolOutput.streamingUnavailable {
						streamingUnavailable = true
					}
					logger.Debug("workflow: sequential tool completed",
						"scope", "workflow",
						"toolIndex", i,
						"toolName", tc.ToolName,
						"toolCallID", tc.ToolCallID,
						"streamingUnavailable", toolOutput.streamingUnavailable)
					toolResults[i] = agentToolResult{
						message: toolOutput.msg,
						failed:  toolOutput.failed,
					}
				}
			}
		default:
			return nil, fmt.Errorf("invalid tool execution mode %q: use %q or %q", toolExecMode, types.AgentToolExecutionModeParallel, types.AgentToolExecutionModeSequential)
		}

		for i, result := range toolResults {
			tc := llmResult.ToolCalls[i]
			if tc.ToolKind.CountsTowardToolTelemetry() {
				telemetry.Tools.Record(tc.ToolName, result.failed)
			}
			if tc.ToolKind == types.ToolKindRetriever {
				telemetry.Storage.TotalRetrieverSearches++
				telemetry.Storage.AgenticSearches++
				if result.failed {
					telemetry.Storage.FailedRetrieverSearches++
				}
			}
			if tc.ToolName == types.SaveMemoryToolName {
				if result.failed {
					telemetry.Storage.FailedMemoryStores++
				} else {
					telemetry.Storage.TotalMemoryStores++
				}
			}
			messages = append(messages, result.message)
		}

		if rt.conversationMemoryEnabled(input.ConversationID) && rt.AgentConfig.Session.ConversationSaveOnIteration && len(messages) > 0 {
			if err := workflow.ExecuteActivity(convCtx, rt.AddConversationMessagesActivity, AddConversationMessagesInput{
				ConversationID:   input.ConversationID,
				Messages:         messages,
				AgentFingerprint: input.AgentFingerprint,
			}).Get(convCtx, nil); err != nil {
				logger.Warn("workflow: persist conversation failed", "scope", "workflow", "conversationID", input.ConversationID, "messagesCount", len(messages), "error", err)
			} else {
				messages = []interfaces.Message{}
			}
		}

		// History-driven ContinueAsNew (same iteration boundary as tool results). Skipped when the LLM
		// returns no tools (final answer path breaks earlier in the loop).
		info := workflow.GetInfo(ctx)
		if info.GetCurrentHistoryLength() >= agentWorkflowHistoryLength || info.GetCurrentHistorySize() >= agentWorkflowHistorySizeBytes {
			logger.Info("workflow: history budget exceeded, continuing as new", "scope", "workflow",
				"iteration", iter+1,
				"messagesCount", len(messages),
				"historyLength", info.GetCurrentHistoryLength(),
				"historyLengthLimit", agentWorkflowHistoryLength,
				"historySizeBytes", info.GetCurrentHistorySize(),
				"historySizeBytesLimit", agentWorkflowHistorySizeBytes,
			)

			input.State = &AgentWorkflowState{
				Iteration: iter + 1,
				Messages:  messages,
				LLMUsage:  llmUsage,
				Telemetry: telemetry,
			}
			return nil, workflow.NewContinueAsNewError(ctx, rt.AgentWorkflow, input)
		}
	}

	// Persist unsaved workflow messages. Flag off: full batch. Flag on: per-iteration saves cleared state; only the final assistant may remain.
	if rt.conversationMemoryEnabled(input.ConversationID) && len(messages) > 0 {
		if err := workflow.ExecuteActivity(convCtx, rt.AddConversationMessagesActivity, AddConversationMessagesInput{
			ConversationID:   input.ConversationID,
			Messages:         messages,
			AgentFingerprint: input.AgentFingerprint,
		}).Get(convCtx, nil); err != nil {
			logger.Warn("workflow: persist conversation failed", "scope", "workflow", "conversationID", input.ConversationID, "messagesCount", len(messages), "error", err)
			if !rt.AgentConfig.Session.ConversationSaveOnIteration {
				return nil, err
			}
		}
	}

	if rt.RunEndMemoryStoreEnabled() {
		if err := workflow.ExecuteActivity(memoryActCtx, rt.AgentMemoryStoreActivity, AgentMemoryStoreInput{
			AgentFingerprint: input.AgentFingerprint,
			RunID:            input.RunID,
			MemoryScope:      input.MemoryScope,
			Messages:         messages,
		}).Get(memoryActCtx, nil); err != nil {
			if temporal.IsCanceledError(err) {
				return nil, err
			}
			logger.Warn("workflow: memory store failed", "scope", "workflow", "error", err)
			telemetry.Storage.FailedMemoryStores++
		} else {
			telemetry.Storage.TotalMemoryStores++
		}
	}

	// Log summary only; avoid full content to prevent leaking sensitive data
	logger.Info("workflow: agent run completed", "scope", "workflow", "contentLen", len(lastContent))

	telemetry.Run.CompletedAt = workflow.Now(ctx)

	runResult := &types.AgentRunResult{
		Content:   lastContent,
		AgentName: agentName,
		Model:     model,
		Metadata:  map[string]any{},
		LLMUsage:  llmUsage,
		Telemetry: telemetry,
	}

	return runResult, nil
}

func (rt *TemporalRuntime) conversationMemoryEnabled(conversationID string) bool {
	return conversationID != "" && rt.AgentConfig.Session.Conversation != nil
}

// newAgentToolCallInput builds activity contexts for one tool-call branch.
// parallelSlot must be unique across concurrent tools (e.g. index string); use empty when calls run sequentially.
func (rt *TemporalRuntime) newAgentToolCallInput(
	wfCtx workflow.Context,
	input AgentWorkflowInput,
	activityIDSuffix, messageID string,
	iteration int,
	emitAgentEvent func(workflow.Context, events.AgentEvent) error,
	parallelSlot string,
) agentToolCallInput {
	approvalTaskTimeout := rt.AgentConfig.Limits.ApprovalTimeout
	if approvalTaskTimeout == 0 {
		approvalTaskTimeout = types.MaxApprovalTimeout
	}
	if approvalTaskTimeout > types.MaxApprovalTimeout {
		approvalTaskTimeout = types.MaxApprovalTimeout
	}
	activityScope := activityIDSuffix
	if parallelSlot != "" {
		activityScope = activityIDSuffix + "_" + parallelSlot
	}
	policies := rt.executionPolicies()
	return agentToolCallInput{
		wfCtx:     wfCtx,
		input:     input,
		messageID: messageID,
		iteration: iteration,
		emitEvent: func(ev events.AgentEvent) error {
			return emitAgentEvent(wfCtx, ev)
		},
		authorizeCtx: workflow.WithActivityOptions(wfCtx, execActivityOptions(
			policies.ToolAuth, "AgentToolAuthorizeActivity_"+activityScope, false)),
		approvalCtx: workflow.WithActivityOptions(wfCtx, workflow.ActivityOptions{
			ActivityID:          "AgentToolApprovalActivity_" + activityScope,
			StartToCloseTimeout: approvalTaskTimeout,
			RetryPolicy:         retryPolicy(agentToolApprovalActivityMaxAttempts),
		}),
		activityScope: activityScope,
		policies:      policies,
	}
}

// executeAgentToolCall runs authorize → approval → execute or sub-agent delegation for one tool call,
// emits tool stream events, and returns the tool role message for the conversation.
// The second return is true when approval returned ApprovalStatusUnavailable (caller should set streamingUnavailable).
func (rt *TemporalRuntime) executeAgentToolCall(input agentToolCallInput, tc ToolCallRequest, streamingUnavailable bool) (*agentToolCallOutput, error) {
	logger := workflow.GetLogger(input.wfCtx)
	agentName := rt.AgentSpec.Name
	eventWorkflowID := input.input.EventWorkflowID
	eventTaskQueue := input.input.EventTaskQueue

	emitToolEndThenResult := func(toolCallID, content string) error {
		if emitErr := input.emitEvent(events.NewAgentToolCallEndEvent(toolCallID)); emitErr != nil {
			return emitErr
		}
		return input.emitEvent(events.NewAgentToolCallResultEvent(input.messageID, toolCallID, content, string(interfaces.MessageRoleTool)))
	}

	if emitErr := input.emitEvent(events.NewAgentToolCallStartEvent(tc.ToolCallID, tc.ToolName, input.messageID)); emitErr != nil {
		return nil, emitErr
	}
	if argsJSON, err := json.Marshal(tc.Args); err == nil {
		s := strings.TrimSpace(string(argsJSON))
		if s != "" && s != "null" && s != "{}" {
			if emitErr := input.emitEvent(events.NewAgentToolCallArgsEvent(tc.ToolCallID, s)); emitErr != nil {
				return nil, emitErr
			}
		}
	}

	var authResult AgentToolAuthorizeResult
	authInput := AgentToolAuthorizeInput{
		ToolCallID:       tc.ToolCallID,
		ToolName:         tc.ToolName,
		Args:             tc.Args,
		AgentFingerprint: input.input.AgentFingerprint,
	}
	if err := workflow.ExecuteActivity(input.authorizeCtx, rt.AgentToolAuthorizeActivity, authInput).Get(input.authorizeCtx, &authResult); err != nil {
		return nil, err
	}
	if !authResult.Allowed {
		logger.Info("workflow: tool authorization denied", "scope", "workflow", "toolName", tc.ToolName, "toolCallID", tc.ToolCallID)
		content := msgToolUnauthorized
		if strings.TrimSpace(authResult.Reason) != "" {
			content = fmt.Sprintf("%s Reason: %s", content, authResult.Reason)
		}
		if emitErr := emitToolEndThenResult(tc.ToolCallID, content); emitErr != nil {
			return nil, emitErr
		}
		return &agentToolCallOutput{
			msg: interfaces.Message{
				Role:       interfaces.MessageRoleTool,
				Content:    content,
				ToolName:   tc.ToolName,
				ToolCallID: tc.ToolCallID,
			},
			failed:               false,
			streamingUnavailable: false,
		}, nil
	}

	approvalStatus := types.ApprovalStatusApproved
	markStreamingUnavailable := false
	if tc.NeedsApproval {
		logger.Info("workflow: tool requires approval", "scope", "workflow", "toolName", tc.ToolName, "argCount", len(tc.Args))
		if streamingUnavailable {
			approvalStatus = types.ApprovalStatusUnavailable
		} else {
			var status types.ApprovalStatus
			approvalInput := AgentToolApprovalInput{
				AgentName:        agentName,
				ToolCallID:       tc.ToolCallID,
				ToolName:         tc.ToolName,
				ToolDisplayName:  tc.ToolDisplayName,
				Args:             tc.Args,
				EventWorkflowID:  eventWorkflowID,
				EventTaskQueue:   eventTaskQueue,
				LocalChannelName: input.input.LocalChannelName,
				AgentFingerprint: input.input.AgentFingerprint,
			}
			if route, ok := input.input.SubAgentRoutes[tc.ToolName]; ok {
				approvalInput.SubAgentName = route.Name
			}
			if err := workflow.ExecuteActivity(input.approvalCtx, rt.AgentToolApprovalActivity, approvalInput).Get(input.approvalCtx, &status); err != nil {
				return nil, err
			}
			approvalStatus = status
			if approvalStatus == types.ApprovalStatusUnavailable {
				markStreamingUnavailable = true
			}
		}
	}

	var content string
	failed := false
	switch approvalStatus {
	case types.ApprovalStatusApproved:
		if route, ok := input.input.SubAgentRoutes[tc.ToolName]; ok {
			logger.Info("workflow: executing sub-agent delegation",
				"scope", "workflow",
				"tool", tc.ToolName,
				"toolCallID", tc.ToolCallID,
				"childTaskQueue", strings.TrimSpace(route.TaskQueue),
				"subAgentDepth", input.input.SubAgentDepth)
			var subErr error
			content, subErr = rt.delegateToSubAgent(input.wfCtx, input.input, tc, route, input.emitEvent)
			if subErr != nil {
				return nil, subErr
			}
		} else {
			logger.Info("workflow: executing tool",
				"scope", "workflow",
				"tool", tc.ToolName,
				"toolCallID", tc.ToolCallID)
			var result string
			execInput := AgentToolExecuteInput{
				ToolName:         tc.ToolName,
				Args:             tc.Args,
				ConversationID:   input.input.ConversationID,
				ToolCallID:       tc.ToolCallID,
				RunID:            input.input.RunID,
				Iteration:        input.iteration,
				AgentFingerprint: input.input.AgentFingerprint,
				MemoryScope:      input.input.MemoryScope,
			}
			toolPolicy := rt.toolExecutionPolicy(tc.ToolKind, input.policies)
			execCtx := workflow.WithActivityOptions(input.wfCtx, execActivityOptions(
				toolPolicy, "AgentToolExecuteActivity_"+input.activityScope, true))
			errExec := workflow.ExecuteActivity(execCtx, rt.AgentToolExecuteActivity, execInput).Get(execCtx, &result)
			if errExec != nil {
				content = "Tool execution failed: " + errExec.Error()
				failed = true
			} else {
				content = result
			}
		}
	case types.ApprovalStatusRejected:
		content = msgToolRejected
	case types.ApprovalStatusUnavailable:
		content = msgToolApprovalUnavailable
	default:
		return nil, fmt.Errorf("workflow: unexpected approval status %q for tool %q", approvalStatus, tc.ToolName)
	}
	if emitErr := emitToolEndThenResult(tc.ToolCallID, content); emitErr != nil {
		return nil, emitErr
	}
	return &agentToolCallOutput{
		msg: interfaces.Message{
			Role:       interfaces.MessageRoleTool,
			Content:    content,
			ToolName:   tc.ToolName,
			ToolCallID: tc.ToolCallID,
		},
		failed:               failed,
		streamingUnavailable: markStreamingUnavailable,
	}, nil
}

// startLongActivityHeartbeats records activity heartbeats until stop is called. Used for long-running
// activities so Temporal can fail the attempt soon after a worker process stops (heartbeat gap > HeartbeatTimeout).
func startLongActivityHeartbeats(ctx context.Context) (stop func()) {
	stopCh := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(agentLongActivityHeartbeatInterval)
		defer ticker.Stop()
		activity.RecordHeartbeat(ctx, nil)
		for {
			select {
			case <-ctx.Done():
				return
			case <-stopCh:
				return
			case <-ticker.C:
				activity.RecordHeartbeat(ctx, nil)
			}
		}
	}()
	return func() {
		once.Do(func() { close(stopCh) })
	}
}

// publishAgentEventToStream delivers one event to the run’s local stream (event workflow update or in-memory bus).
func (rt *TemporalRuntime) publishAgentEventToStream(ctx context.Context, agentName, localChannelName, eventWorkflowID, eventTaskQueue string, ev events.AgentEvent) {
	if ev == nil || strings.TrimSpace(localChannelName) == "" {
		return
	}
	eventBytes, _ := ev.ToJSON()
	upd := &AgentEventUpdate{
		AgentName:        strings.TrimSpace(agentName),
		LocalChannelName: localChannelName,
		EventJSON:        json.RawMessage(eventBytes),
	}
	if eventWorkflowID != "" {
		_ = rt.sendAgentEventWorkflowUpdate(ctx, eventWorkflowID, eventTaskQueue, upd, client.WorkflowUpdateStageAccepted, updateWorkflowEventRPCTimeout)
	} else if rt.eventbus != nil {
		data, _ := json.Marshal(ev)
		_ = rt.eventbus.Publish(ctx, localChannelName, data)
	}
}

// AgentLLMStreamActivity streams LLM response tokens. Event order: optional reasoning block
// (REASONING_*), then TEXT_MESSAGE_START → TEXT_MESSAGE_CONTENT* → TEXT_MESSAGE_END.
// When input.ConversationID is set, fetches messages from conversation and prepends to workflow messages.
func (rt *TemporalRuntime) AgentLLMStreamActivity(ctx context.Context, input AgentLLMInput) (*AgentLLMResult, error) {
	tools, err := rt.fetchTools(ctx)
	if err != nil {
		return nil, err
	}
	if err := rt.verifyAgentFingerprint(ctx, input.AgentFingerprint, tools); err != nil {
		return nil, err
	}
	stopHB := startLongActivityHeartbeats(ctx)
	defer stopHB()

	actLog := newActivityLogger(activity.GetLogger(ctx))
	agentName := strings.TrimSpace(input.AgentName)

	messages := input.Messages
	if rt.conversationMemoryEnabled(input.ConversationID) {
		convMessages, err := rt.FetchConversationMessages(ctx, actLog, input.ConversationID)
		if err != nil {
			return nil, err
		}
		messages = append(convMessages, messages...)
	}

	emit := func(ev events.AgentEvent) {
		rt.publishAgentEventToStream(ctx, agentName, input.LocalChannelName, input.EventWorkflowID, input.EventTaskQueue, ev)
	}

	executeLLMInput := base.ExecuteLLMInput{
		Logger:           actLog,
		AgentName:        agentName,
		MessageID:        input.MessageID,
		RunID:            input.RunID,
		Iteration:        input.Iteration,
		Messages:         messages,
		SkipTools:        input.SkipTools,
		RetrieverContext: input.RetrieverContext,
		MemoryContext:    input.MemoryContext,
		Tools:            tools,
		Emit:             emit,
	}

	result, err := rt.ExecuteLLMStream(ctx, executeLLMInput)
	if err != nil {
		return nil, err
	}
	return baseLLMResultToActivity(result), nil
}

// AgentRetrieverActivity runs all configured retrievers in parallel using input.UserPrompt as the query,
// then returns a combined document context string for injection into the LLM system prompt.
// Called only for [types.RetrieverModePrefetch] and [types.RetrieverModeHybrid].
// Partial failures (some retrievers fail) are logged and skipped; if all retrievers fail, the activity
// returns an error so Temporal can retry per the retry policy.
func (rt *TemporalRuntime) AgentRetrieverActivity(ctx context.Context, input AgentRetrieverInput) (*AgentRetrieverResult, error) {
	if err := rt.verifyAgentFingerprint(ctx, input.AgentFingerprint, nil); err != nil {
		return nil, err
	}
	actLog := newActivityLogger(activity.GetLogger(ctx))
	res, err := rt.ExecuteRetrievers(ctx, base.ExecuteRetrieversInput{
		Logger:    actLog,
		RunID:     input.RunID,
		Iteration: 0,
		Query:     input.UserPrompt,
	})
	if err != nil {
		return nil, err
	}
	return &AgentRetrieverResult{
		RetrieverContext: res.Context,
		TotalSearches:    res.TotalSearches,
		FailedSearches:   res.FailedSearches,
	}, nil
}

// AgentMemoryRecallActivity loads scoped long-term memories and returns formatted prompt context.
func (rt *TemporalRuntime) AgentMemoryRecallActivity(ctx context.Context, input AgentMemoryRecallInput) (*AgentMemoryRecallResult, error) {
	if err := rt.verifyAgentFingerprint(ctx, input.AgentFingerprint, nil); err != nil {
		return nil, err
	}
	actLog := newActivityLogger(activity.GetLogger(ctx))
	res, err := rt.ExecuteMemoryRecall(ctx, base.ExecuteMemoryRecallInput{
		Logger:    actLog,
		RunID:     input.RunID,
		Iteration: 0,
		Scope:     input.MemoryScope,
		Query:     input.UserPrompt,
	})
	if err != nil {
		return nil, err
	}
	return &AgentMemoryRecallResult{
		MemoryContext: res.Context,
		TotalRecalls:  res.TotalRecalls,
		FailedRecalls: res.FailedRecalls,
	}, nil
}

// AgentMemoryStoreActivity extracts and persists long-term memories from the run.
func (rt *TemporalRuntime) AgentMemoryStoreActivity(ctx context.Context, input AgentMemoryStoreInput) error {
	if err := rt.verifyAgentFingerprint(ctx, input.AgentFingerprint, nil); err != nil {
		return err
	}
	actLog := newActivityLogger(activity.GetLogger(ctx))
	return rt.ExecuteMemoryStore(ctx, base.ExecuteMemoryStoreInput{
		Logger:    actLog,
		RunID:     input.RunID,
		Iteration: 0,
		Scope:     input.MemoryScope,
		Messages:  input.Messages,
	})
}

// AgentLLMActivity calls the LLM and returns content plus any tool calls.
// When input.ConversationID is set, fetches from store and adds assistant message on completion.
func (rt *TemporalRuntime) AgentLLMActivity(ctx context.Context, input AgentLLMInput) (*AgentLLMResult, error) {
	tools, err := rt.fetchTools(ctx)
	if err != nil {
		return nil, err
	}
	if err := rt.verifyAgentFingerprint(ctx, input.AgentFingerprint, tools); err != nil {
		return nil, err
	}
	actLog := newActivityLogger(activity.GetLogger(ctx))
	agentName := strings.TrimSpace(input.AgentName)

	messages := input.Messages
	if rt.conversationMemoryEnabled(input.ConversationID) {
		convMessages, err := rt.FetchConversationMessages(ctx, actLog, input.ConversationID)
		if err != nil {
			return nil, err
		}
		messages = append(convMessages, messages...)
	}

	emit := func(ev events.AgentEvent) {
		rt.publishAgentEventToStream(ctx, agentName, input.LocalChannelName, input.EventWorkflowID, input.EventTaskQueue, ev)
	}

	executeLLMInput := base.ExecuteLLMInput{
		Logger:           actLog,
		AgentName:        agentName,
		MessageID:        input.MessageID,
		RunID:            input.RunID,
		Iteration:        input.Iteration,
		Messages:         messages,
		SkipTools:        input.SkipTools,
		RetrieverContext: input.RetrieverContext,
		MemoryContext:    input.MemoryContext,
		Tools:            tools,
		Emit:             emit,
	}

	result, err := rt.ExecuteLLM(ctx, executeLLMInput)
	if err != nil {
		return nil, err
	}
	return baseLLMResultToActivity(result), nil
}

// AgentToolApprovalActivity blocks until the driver completes it via CompleteActivity.
// Publishes a CUSTOM (tool_approval / delegation) event to the local agent_event channel (Run and Stream).
// When EventWorkflowID is set, UpdateWorkflow uses WorkflowUpdateStageCompleted and updateWorkflowApprovalRPCTimeout
// so the event handler has returned before ErrResultPending; RPC timeout maps to ApprovalStatusUnavailable.
func (rt *TemporalRuntime) AgentToolApprovalActivity(ctx context.Context, input AgentToolApprovalInput) (types.ApprovalStatus, error) {
	if err := rt.verifyAgentFingerprint(ctx, input.AgentFingerprint, nil); err != nil {
		return types.ApprovalStatusNone, err
	}
	logger := activity.GetLogger(ctx)
	logger.Debug("activity: tool approval started", "scope", "activity", "tool", input.ToolName, "remoteEventPipeline", input.EventWorkflowID != "")

	info := activity.GetInfo(ctx)
	taskTokenB64 := base64.StdEncoding.EncodeToString(info.TaskToken)

	agentEventName := events.AgentCustomEventNameToolApproval
	subAgentName := input.SubAgentName
	if subAgentName != "" {
		agentEventName = events.AgentCustomEventNameSubAgentDelegation
	}

	var ev *events.AgentCustomEvent
	if agentEventName == events.AgentCustomEventNameToolApproval {
		logger.Debug("activity: approval is tool approval",
			"scope", "activity",
			"tool", input.ToolName,
			"mainAgent", rt.AgentSpec.Name)
		ev = events.NewAgentCustomEvent(string(agentEventName), events.AgentCustomEventApprovalValue{
			AgentName:       input.AgentName,
			ToolCallID:      input.ToolCallID,
			ToolName:        input.ToolName,
			ToolDisplayName: input.ToolDisplayName,
			Args:            input.Args,
			ApprovalToken:   taskTokenB64,
		})
	} else {
		logger.Debug("activity: approval is sub-agent delegation",
			"scope", "activity",
			"tool", input.ToolName,
			"subAgent", subAgentName,
			"mainAgent", rt.AgentSpec.Name)
		ev = events.NewAgentCustomEvent(string(agentEventName), events.AgentCustomEventDelegationValue{
			AgentName:     input.AgentName,
			SubAgentName:  subAgentName,
			Args:          input.Args,
			ApprovalToken: taskTokenB64,
		})
	}

	// Route via event workflow when eventWorkflowID is set (TemporalRuntime.enableRemoteWorkers)
	if input.EventWorkflowID != "" {
		eventBytes, _ := ev.ToJSON()
		upd := &AgentEventUpdate{
			AgentName:        rt.AgentSpec.Name,
			LocalChannelName: input.LocalChannelName,
			EventJSON:        json.RawMessage(eventBytes),
		}
		if err := rt.sendAgentEventWorkflowUpdate(ctx, input.EventWorkflowID, input.EventTaskQueue, upd, client.WorkflowUpdateStageCompleted, updateWorkflowApprovalRPCTimeout); err != nil {
			return types.ApprovalStatusUnavailable, nil
		}
		logger.Debug("activity: approval sent to event pipeline", "scope", "activity", "eventPipelineID", input.EventWorkflowID, "tool", input.ToolName)
	} else {
		if rt.eventbus == nil {
			return types.ApprovalStatusNone, fmt.Errorf("agentChannel required when eventWorkflowID is empty")
		}
		data, err := json.Marshal(ev)
		if err != nil {
			return types.ApprovalStatusNone, err
		}
		if err := rt.eventbus.Publish(ctx, input.LocalChannelName, data); err != nil {
			return types.ApprovalStatusUnavailable, nil
		}
		logger.Debug("activity: approval published to local channel", "scope", "activity", "channel", input.LocalChannelName, "tool", input.ToolName)
	}
	logger.Debug("activity: approval pending driver completion", "scope", "activity", "tool", input.ToolName)
	return types.ApprovalStatusPending, activity.ErrResultPending
}

// SendAgentEventUpdateActivity sends event: via UpdateWithStartWorkflow when eventWorkflowID is set; else in-memory agentChannel.
// Returns StreamUnavailable without error when delivery fails but the workflow should continue (dead stream / pipeline).
// Returns a non-nil error for configuration or internal failures (fatal to the workflow).
func (rt *TemporalRuntime) SendAgentEventUpdateActivity(ctx context.Context, in SendAgentEventActivityInput) (SendAgentEventResult, error) {
	logger := activity.GetLogger(ctx)
	upd := in.Update
	logger.Debug("activity: send event update started", "scope", "activity", "eventPipelineID", in.EventWorkflowID)

	if upd == nil || upd.EventJSON == nil {
		return SendAgentEventResult{}, nil
	}

	if upd.EventJSON != nil {
		logger.Debug("activity: send event update", "scope", "activity", "eventType", string(in.EventType), "agent", upd.AgentName)
	}

	// Route via event workflow when eventWorkflowID is set (TemporalRuntime.enableRemoteWorkers)
	if in.EventWorkflowID != "" {
		if err := rt.sendAgentEventWorkflowUpdate(ctx, in.EventWorkflowID, in.EventTaskQueue, upd, client.WorkflowUpdateStageAccepted, updateWorkflowEventRPCTimeout); err != nil {
			return SendAgentEventResult{StreamUnavailable: true}, nil
		}
		logger.Debug("activity: event sent to pipeline", "scope", "activity", "eventPipelineID", in.EventWorkflowID, "agent", upd.AgentName)
	} else {
		if rt.eventbus == nil {
			return SendAgentEventResult{}, fmt.Errorf("agentChannel required when eventWorkflowID is empty")
		}
		if err := rt.eventbus.Publish(ctx, upd.LocalChannelName, []byte(upd.EventJSON)); err != nil {
			return SendAgentEventResult{StreamUnavailable: true}, nil
		}
		logger.Debug("activity: event sent to local channel", "scope", "activity", "channel", upd.LocalChannelName, "agent", upd.AgentName)
	}
	logger.Debug("activity: send event update completed", "scope", "activity", "agent", upd.AgentName)
	return SendAgentEventResult{}, nil
}

// AddConversationMessagesActivity adds messages to the conversation memory.
func (rt *TemporalRuntime) AddConversationMessagesActivity(ctx context.Context, input AddConversationMessagesInput) error {
	if err := rt.verifyAgentFingerprint(ctx, input.AgentFingerprint, nil); err != nil {
		return err
	}
	conversationID := input.ConversationID
	messages := input.Messages
	logger := activity.GetLogger(ctx)

	msgCount := len(messages)

	logger.Debug("activity: add conversation messages started", "scope", "activity", "conversationID", conversationID, "messagesCount", msgCount)

	if rt.AgentConfig.Session.Conversation == nil {
		return fmt.Errorf("conversation is not configured")
	}

	ctx, sp := rt.Tracer.StartSpan(ctx, "conversation.add_messages",
		interfaces.Attribute{Key: "conversation.id", Value: conversationID},
		interfaces.Attribute{Key: "message.count", Value: msgCount},
	)
	defer sp.End()

	failCount := 0
	for _, msg := range messages {
		if err := rt.AgentConfig.Session.Conversation.AddMessage(ctx, conversationID, msg); err != nil {
			failCount++
			msgCount--
			logger.Warn("activity: add conversation message failed", "scope", "activity", "conversationID", conversationID, "error", err)
		}
	}
	if failCount > 0 {
		sp.SetAttribute("failed.count", failCount)
	}

	logger.Debug("activity: add conversation messages completed", "scope", "activity", "conversationID", conversationID, "messagesCount", msgCount)
	return nil
}

// AgentToolExecuteActivity executes a tool by name and adds tool message to conversation when ConversationID is set.
func (rt *TemporalRuntime) AgentToolExecuteActivity(ctx context.Context, input AgentToolExecuteInput) (string, error) {
	tools, err := rt.fetchTools(ctx)
	if err != nil {
		return "", err
	}
	if err := rt.verifyAgentFingerprint(ctx, input.AgentFingerprint, tools); err != nil {
		return "", err
	}
	stopHB := startLongActivityHeartbeats(ctx)
	defer stopHB()
	actLog := newActivityLogger(activity.GetLogger(ctx))
	return rt.ExecuteTool(ctx, base.ExecuteToolInput{
		Logger:     actLog,
		Tools:      tools,
		ToolName:   input.ToolName,
		Args:       input.Args,
		ToolCallID: input.ToolCallID,
		RunID:      input.RunID,
		Iteration:  input.Iteration,
	}, input.MemoryScope)
}

// AgentToolAuthorizeActivity checks optional programmatic authorization before approval/execute.
func (rt *TemporalRuntime) AgentToolAuthorizeActivity(ctx context.Context, input AgentToolAuthorizeInput) (AgentToolAuthorizeResult, error) {
	tools, err := rt.fetchTools(ctx)
	if err != nil {
		return AgentToolAuthorizeResult{}, err
	}
	if err := rt.verifyAgentFingerprint(ctx, input.AgentFingerprint, tools); err != nil {
		return AgentToolAuthorizeResult{}, err
	}
	actLog := newActivityLogger(activity.GetLogger(ctx))
	authResult, err := rt.AuthorizeTool(ctx, actLog, tools, input.ToolName, input.Args)
	if err != nil {
		return AgentToolAuthorizeResult{}, err
	}
	return AgentToolAuthorizeResult{Allowed: authResult.Allowed, Reason: authResult.Reason}, nil
}

func (rt *TemporalRuntime) delegateToSubAgent(ctx workflow.Context, input AgentWorkflowInput, tc ToolCallRequest, route SubAgentRoute, emitEvent func(events.AgentEvent) error) (string, error) {
	logger := workflow.GetLogger(ctx)
	if strings.TrimSpace(route.TaskQueue) == "" {
		logger.Warn("workflow: sub-agent delegation skipped (empty task queue)",
			"scope", "workflow",
			"tool", tc.ToolName,
			"toolCallID", tc.ToolCallID)
		return "Sub-agent delegation failed: sub-agent task queue is not configured.", nil
	}
	maxDepth := input.MaxSubAgentDepth
	if input.SubAgentDepth >= maxDepth {
		logger.Warn("workflow: sub-agent delegation refused (max depth)",
			"scope", "workflow",
			"subAgentDepth", input.SubAgentDepth,
			"maxDepth", maxDepth,
			"tool", tc.ToolName,
			"toolCallID", tc.ToolCallID)
		return fmt.Sprintf("Sub-agent delegation refused: maximum nesting depth (%d) reached for this agent.", maxDepth), nil
	}

	query := base.SubAgentQuery(tc.Args)
	subAgentID := strings.TrimSpace(route.Name)
	if subAgentID == "" {
		subAgentID = tc.ToolName
	}

	var childSuffix string
	if err := workflow.SideEffect(ctx, func(workflow.Context) interface{} {
		return uuid.New().String()
	}).Get(&childSuffix); err != nil {
		logger.Warn("workflow: sub-agent child run id failed", "scope", "workflow", "error", err)
		return "", err
	}

	childInput := AgentWorkflowInput{
		UserPrompt:       query,
		RunID:            childSuffix,
		EventWorkflowID:  input.EventWorkflowID,
		EventTaskQueue:   input.EventTaskQueue,
		LocalChannelName: input.LocalChannelName,
		StreamingEnabled: input.StreamingEnabled,
		ConversationID:   "",
		AgentFingerprint: route.AgentFingerprint,
		EventTypes:       input.EventTypes,
		MemoryScope:      base.SubAgentScope(input.MemoryScope, subAgentID),
		SubAgentDepth:    input.SubAgentDepth + 1,
		SubAgentRoutes:   route.ChildRoutes,
	}

	parentID := workflow.GetInfo(ctx).WorkflowExecution.ID
	childWfID := fmt.Sprintf("%s-sub-%s-%s", parentID, tc.ToolCallID, childSuffix)
	childTO := rt.subAgentChildWorkflowTimeout()

	logger.Debug("workflow: sub-agent child run starting",
		"scope", "workflow",
		"childWorkflowID", childWfID,
		"childTaskQueue", strings.TrimSpace(route.TaskQueue),
		"tool", tc.ToolName,
		"toolCallID", tc.ToolCallID,
		"parentSubAgentDepth", input.SubAgentDepth,
		"childSubAgentDepth", childInput.SubAgentDepth,
		"nestedChildRouteCount", len(route.ChildRoutes),
		"workflowExecutionTimeout", childTO,
		"delegatedQueryLen", len(query))

	childCtx := workflow.WithChildOptions(ctx, workflow.ChildWorkflowOptions{
		WorkflowID:               childWfID,
		TaskQueue:                route.TaskQueue,
		WorkflowExecutionTimeout: childTO,
		ParentClosePolicy:        enumspb.PARENT_CLOSE_POLICY_REQUEST_CANCEL,
		WaitForCancellation:      true,
	})

	delegationName := strings.TrimSpace(route.Name)
	if delegationName == "" {
		delegationName = tc.ToolName
	}

	if emitErr := emitEvent(events.NewAgentStepStartedEvent(delegationName)); emitErr != nil {
		return "", emitErr
	}

	var childResult *types.AgentRunResult
	if err := workflow.ExecuteChildWorkflow(childCtx, rt.AgentWorkflow, childInput).Get(childCtx, &childResult); err != nil {
		logger.Warn("workflow: sub-agent child run failed",
			"scope", "workflow",
			"childWorkflowID", childWfID,
			"tool", tc.ToolName,
			"error", err)
		return "Sub-agent workflow failed: " + err.Error(), nil
	}

	if emitErr := emitEvent(events.NewAgentStepFinishedEvent(delegationName)); emitErr != nil {
		return "", emitErr
	}

	logger.Debug("workflow: sub-agent child run completed",
		"scope", "workflow",
		"childWorkflowID", childWfID,
		"tool", tc.ToolName,
		"resultContentLen", len(childResult.Content))

	return childResult.Content, nil
}

// subAgentChildWorkflowTimeout caps how long the main agent waits on a delegated sub-agent run.
// Derived from the resolved sub-agent execution policy; falls back to the agent run timeout when
// no explicit timeout override was configured.
func (rt *TemporalRuntime) subAgentChildWorkflowTimeout() time.Duration {
	timeout := rt.executionPolicies().SubAgent.Timeout
	if timeout == 0 && rt.AgentConfig.Limits.Timeout > 0 {
		return rt.AgentConfig.Limits.Timeout
	}
	return timeout
}

// executionPolicies merges the agent's ExecutionConfig overrides onto SDK defaults and converts them to
// fully populated ExecutionPolicy values for every agent loop operation.
func (rt *TemporalRuntime) executionPolicies() agentrt.ExecutionPolicies {
	return agentrt.ResolveExecutionPolicies(rt.AgentConfig.ExecutionConfigs)
}

// toolExecutionPolicy returns the execution policy for a tool execution operation based on tool kind.
// MCP and A2A tools use their dedicated policies; all other tools use the generic ToolExecute policy.
func (rt *TemporalRuntime) toolExecutionPolicy(kind types.ToolKind, policies agentrt.ExecutionPolicies) agentrt.ExecutionPolicy {
	switch kind {
	case types.ToolKindMCP:
		return policies.MCP
	case types.ToolKindA2A:
		return policies.A2A
	default:
		return policies.ToolExecute
	}
}

// execActivityOptions builds Temporal ActivityOptions from a resolved ExecutionPolicy.
// The StartToCloseTimeout is set to the policy Timeout; retries follow the policy's own backoff.
// When withHeartbeat is true, a HeartbeatTimeout is added so long-running activities are detected as lost.
func execActivityOptions(policy agentrt.ExecutionPolicy, activityID string, withHeartbeat bool) workflow.ActivityOptions {
	attempts := int32(policy.MaxAttempts)
	if attempts < 1 {
		attempts = 1
	}
	opts := workflow.ActivityOptions{
		ActivityID:          activityID,
		StartToCloseTimeout: policy.Timeout,
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    policy.Retry.InitialInterval,
			BackoffCoefficient: policy.Retry.BackoffCoefficient,
			MaximumInterval:    policy.Retry.MaximumInterval,
			MaximumAttempts:    attempts,
		},
	}
	if withHeartbeat {
		opts.HeartbeatTimeout = agentLongActivityHeartbeatTimeout
	}
	return opts
}

// retryPolicy builds a Temporal *RetryPolicy with SDK default backoff.
// maxAttempts is clamped to a minimum of 1 so a zero value does not disable retries.
func retryPolicy(maxAttempts int32) *temporal.RetryPolicy {
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	def := agentrt.DefaultRetryPolicy()
	return &temporal.RetryPolicy{
		InitialInterval:    def.InitialInterval,
		BackoffCoefficient: def.BackoffCoefficient,
		MaximumInterval:    def.MaximumInterval,
		MaximumAttempts:    maxAttempts,
	}
}
