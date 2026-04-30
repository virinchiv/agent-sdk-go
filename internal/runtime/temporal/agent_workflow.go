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
	agentLLMActivityTaskTimeout time.Duration = 30 * time.Minute
	agentLLMActivityMaxAttempts int32         = 3
	// Heartbeat for long LLM stream / tool execute: fail stuck attempts soon after worker loss (<< StartToClose).
	agentLongActivityHeartbeatTimeout  time.Duration = 30 * time.Second
	agentLongActivityHeartbeatInterval time.Duration = 10 * time.Second

	agentToolApprovalActivityMaxAttempts int32 = 1

	agentToolAuthorizeActivityTaskTimeout time.Duration = 30 * time.Minute
	agentToolAuthorizeActivityMaxAttempts int32         = 1

	agentToolExecuteActivityTaskTimeout time.Duration = 30 * time.Minute
	agentToolExecuteActivityMaxAttempts int32         = 3

	sendEventActivityTaskTimeout time.Duration = 15 * time.Second
	sendEventActivityMaxAttempts int32         = 1

	conversationActivityTaskTimeout time.Duration = 30 * time.Second
	conversationActivityMaxAttempts int32         = 1

	// updateWorkflowEventRPCTimeout caps UpdateWorkflow for normal events (Accepted). When the event worker
	// or process is gone, fail fast instead of blocking until sendEventActivityTaskTimeout. Must be < sendEventActivityTaskTimeout.
	updateWorkflowEventRPCTimeout = 10 * time.Second
	// updateWorkflowApprovalRPCTimeout caps UpdateWorkflow when posting approval to the event pipeline (Completed).
	// Only the "deliver to event workflow handler" phase; must be far below approval activity StartToClose.
	updateWorkflowApprovalRPCTimeout = 30 * time.Second
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
// LocalChannelName is the in-process pub/sub channel name used for in-memory event fan-in across the
// delegation tree. Set once at the top level (agent_event_<main-workflow-id>) and propagated unchanged
// to all sub-agents. Contrast with EventWorkflowID which is used for out-of-process (remote) routing.
// EventTaskQueue is the Temporal task queue for AgentEventWorkflow (e.g. main TaskQueue + "-events"); required
// for UpdateWithStartWorkflow when EventWorkflowID is set.
// EventTypes is set by the SDK; a single "*" element means emit all event kinds (used for Stream).
// AgentFingerprint is the SHA-256 hex digest of the worker-local agent config; activities reject on mismatch.
type AgentWorkflowInput struct {
	UserPrompt       string                         `json:"user_prompt,omitempty"`
	EventWorkflowID  string                         `json:"event_workflow_id,omitempty"`
	EventTaskQueue   string                         `json:"event_task_queue,omitempty"`
	LocalChannelName string                         `json:"local_channel_name,omitempty"`
	StreamingEnabled bool                           `json:"streaming_enabled,omitempty"`
	ConversationID   string                         `json:"conversation_id,omitempty"`
	AgentFingerprint string                         `json:"agent_fingerprint,omitempty"`
	EventTypes       []events.AgentEventType        `json:"event_types,omitempty"`
	SubAgentDepth    int                            `json:"sub_agent_depth,omitempty"`
	SubAgentRoutes   map[string]types.SubAgentRoute `json:"sub_agent_routes,omitempty"`
	MaxSubAgentDepth int                            `json:"max_sub_agent_depth,omitempty"`
}

// AgentLLMInput is the input to AgentLLMActivity and AgentLLMStreamActivity.
// When ConversationID is set, the activity loads history from the store. MessageID is the assistant text id
// for TEXT_MESSAGE_* (and stream ordering with REASONING_*); the workflow sets it each turn.
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
}

// AgentLLMResult is the return value of AgentLLMActivity. Workflow uses it to decide: return content or execute tools.
type AgentLLMResult struct {
	Content   string               `json:"content"`
	ToolCalls []ToolCallRequest    `json:"tool_calls"`
	Usage     *interfaces.LLMUsage `json:"usage,omitempty"`
}

// ToolCallRequest is a tool invocation with approval flag. NeedsApproval is set by AgentLLMActivity.
type ToolCallRequest struct {
	ToolCallID      string         `json:"tool_call_id"` // from LLM; used to match tool results
	ToolName        string         `json:"tool_name"`
	ToolDisplayName string         `json:"tool_display_name,omitempty"`
	Args            map[string]any `json:"args"`
	NeedsApproval   bool           `json:"needs_approval"`
}

// agentToolCallInput bundles the workflow handle, per-iteration activity contexts, and emit plumbing for tool execution.
// Built once per sequential LLM tool round, or once per parallel branch (unique parallelSlot activity IDs).
type agentToolCallInput struct {
	wfCtx        workflow.Context
	input        AgentWorkflowInput
	messageID    string
	emitEvent    func(events.AgentEvent) error
	authorizeCtx workflow.Context
	approvalCtx  workflow.Context
	execCtx      workflow.Context
}

// agentToolCallOutput is the output of executeAgentToolCall.
type agentToolCallOutput struct {
	msg                  interfaces.Message
	streamingUnavailable bool
}

// AgentToolExecuteInput is the input to AgentToolExecuteActivity.
type AgentToolExecuteInput struct {
	ToolName         string               `json:"tool_name"`
	Args             map[string]any       `json:"args"`
	ConversationID   string               `json:"conversation_id,omitempty"`
	Messages         []interfaces.Message `json:"messages,omitempty"`
	ToolCallID       string               `json:"tool_call_id,omitempty"`
	AgentFingerprint string               `json:"agent_fingerprint,omitempty"`
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
	model := rt.AgentExecution.LLM.Client.GetModel()

	maxIter := rt.AgentExecution.Limits.MaxIterations

	var activityIDSuffix string
	err := workflow.SideEffect(ctx, func(ctx workflow.Context) interface{} {
		return uuid.New().String()
	}).Get(&activityIDSuffix)
	if err != nil {
		return nil, err
	}

	llmActCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		ActivityID:          "AgentLLMActivity_" + activityIDSuffix,
		StartToCloseTimeout: agentLLMActivityTaskTimeout,
		RetryPolicy:         retryPolicy(agentLLMActivityMaxAttempts),
	})
	streamActCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		ActivityID:          "AgentLLMStreamActivity_" + activityIDSuffix,
		StartToCloseTimeout: agentLLMActivityTaskTimeout,
		HeartbeatTimeout:    agentLongActivityHeartbeatTimeout,
		RetryPolicy:         retryPolicy(agentLLMActivityMaxAttempts),
	})

	sendEventCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		ActivityID:          "SendAgentEventUpdateActivity_" + activityIDSuffix,
		StartToCloseTimeout: sendEventActivityTaskTimeout,
		RetryPolicy:         retryPolicy(sendEventActivityMaxAttempts),
	})
	convCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		ActivityID:          "ConversationActivity_" + activityIDSuffix,
		StartToCloseTimeout: conversationActivityTaskTimeout,
		RetryPolicy:         retryPolicy(conversationActivityMaxAttempts),
	})

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

	useStreaming := input.StreamingEnabled && rt.AgentExecution.LLM.Client.IsStreamSupported()

	messages := []interfaces.Message{{Role: interfaces.MessageRoleUser, Content: input.UserPrompt}}

	lastContent := ""
	var runUsage *interfaces.LLMUsage
	var llmResult AgentLLMResult
	for iter := 0; iter < maxIter; iter++ {

		messageID := uuid.New().String()

		llmInput := AgentLLMInput{
			AgentName:        agentName,
			ConversationID:   input.ConversationID,
			Messages:         messages,
			AgentFingerprint: input.AgentFingerprint,
			MessageID:        messageID,
			EventWorkflowID:  eventWorkflowID,
			EventTaskQueue:   eventTaskQueue,
			LocalChannelName: input.LocalChannelName,
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

		runUsage = mergeLLMUsage(runUsage, llmResult.Usage)

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
			runUsage = mergeLLMUsage(runUsage, llmResult.Usage)
			messages = append(messages, interfaces.Message{Role: interfaces.MessageRoleAssistant, Content: llmResult.Content})
			lastContent = llmResult.Content
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

		var toolResults []interfaces.Message

		toolExecMode := rt.AgentToolExecutionMode
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
						parallelInput := rt.newAgentToolCallInput(gCtx, input, activityIDSuffix, messageID, emitAgentEvent, slot)
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

				toolResults = make([]interfaces.Message, len(futures))
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
						// Tool failed — send error as tool result so LLM can handle it
						toolResults[i] = interfaces.Message{
							Role:       interfaces.MessageRoleTool,
							Content:    "Tool execution failed: " + err.Error(),
							ToolName:   tc.ToolName,
							ToolCallID: tc.ToolCallID,
						}
					} else {
						// Success: branch always Set(non-nil *agentToolCallOutput, nil).
						logger.Debug("workflow: parallel tool future collected (ok)",
							"scope", "workflow",
							"toolIndex", i,
							"toolName", tc.ToolName,
							"toolCallID", tc.ToolCallID,
							"streamingUnavailable", v.streamingUnavailable)
						toolResults[i] = v.msg
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
				toolInput := rt.newAgentToolCallInput(ctx, input, activityIDSuffix, messageID, emitAgentEvent, "")
				// authorize / approve / execute, then TOOL_CALL_END + TOOL_CALL_RESULT.
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
						return nil, runErr
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
					toolResults = append(toolResults, toolOutput.msg)
				}
			}
		default:
			return nil, fmt.Errorf("invalid tool execution mode %q: use %q or %q", toolExecMode, types.AgentToolExecutionModeParallel, types.AgentToolExecutionModeSequential)
		}

		messages = append(messages, toolResults...)
	}

	// Add all accumulated messages to conversation after execution completes (only when conversationID set)
	if input.ConversationID != "" {
		if len(messages) == 0 {
			logger.Debug("workflow: no conversation messages to persist", "scope", "workflow", "conversationID", input.ConversationID)
		} else {
			if err := workflow.ExecuteActivity(convCtx, rt.AddConversationMessagesActivity, AddConversationMessagesInput{
				ConversationID:   input.ConversationID,
				Messages:         messages,
				AgentFingerprint: input.AgentFingerprint,
			}).Get(convCtx, nil); err != nil {
				logger.Warn("workflow: persist conversation failed", "scope", "workflow", "conversationID", input.ConversationID, "messagesCount", len(messages), "error", err)
				return nil, err
			}
		}
	}

	// Log summary only; avoid full content to prevent leaking sensitive data
	logger.Info("workflow: agent run completed", "scope", "workflow", "contentLen", len(lastContent))
	return &types.AgentRunResult{
		Content: lastContent, AgentName: agentName, Model: model, Metadata: map[string]any{}, Usage: runUsage,
	}, nil
}

// newAgentToolCallInput builds activity contexts for one tool-call branch.
// parallelSlot must be unique across concurrent tools (e.g. index string); use empty when calls run sequentially.
func (rt *TemporalRuntime) newAgentToolCallInput(
	wfCtx workflow.Context,
	input AgentWorkflowInput,
	activityIDSuffix, messageID string,
	emitAgentEvent func(workflow.Context, events.AgentEvent) error,
	parallelSlot string,
) agentToolCallInput {
	approvalTaskTimeout := rt.AgentExecution.Limits.ApprovalTimeout
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
	return agentToolCallInput{
		wfCtx:     wfCtx,
		input:     input,
		messageID: messageID,
		emitEvent: func(ev events.AgentEvent) error {
			return emitAgentEvent(wfCtx, ev)
		},
		authorizeCtx: workflow.WithActivityOptions(wfCtx, workflow.ActivityOptions{
			ActivityID:          "AgentToolAuthorizeActivity_" + activityScope,
			StartToCloseTimeout: agentToolAuthorizeActivityTaskTimeout,
			RetryPolicy:         retryPolicy(agentToolAuthorizeActivityMaxAttempts),
		}),
		approvalCtx: workflow.WithActivityOptions(wfCtx, workflow.ActivityOptions{
			ActivityID:          "AgentToolApprovalActivity_" + activityScope,
			StartToCloseTimeout: approvalTaskTimeout,
			RetryPolicy:         retryPolicy(agentToolApprovalActivityMaxAttempts),
		}),
		execCtx: workflow.WithActivityOptions(wfCtx, workflow.ActivityOptions{
			ActivityID:          "AgentToolExecuteActivity_" + activityScope,
			StartToCloseTimeout: agentToolExecuteActivityTaskTimeout,
			HeartbeatTimeout:    agentLongActivityHeartbeatTimeout,
			RetryPolicy:         retryPolicy(agentToolExecuteActivityMaxAttempts),
		}),
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
				AgentFingerprint: input.input.AgentFingerprint,
			}
			errExec := workflow.ExecuteActivity(input.execCtx, rt.AgentToolExecuteActivity, execInput).Get(input.execCtx, &result)
			if errExec != nil {
				content = "Tool execution failed: " + errExec.Error()
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
	if err := rt.verifyAgentFingerprint(input.AgentFingerprint); err != nil {
		return nil, err
	}
	stopHB := startLongActivityHeartbeats(ctx)
	defer stopHB()
	logger := activity.GetLogger(ctx)
	info := activity.GetInfo(ctx)
	agentWorkflowID := info.WorkflowExecution.ID
	agentName := strings.TrimSpace(input.AgentName)

	messages := input.Messages
	if input.ConversationID != "" {
		convMessages, err := rt.fetchConversationMessages(ctx, input.ConversationID)
		if err != nil {
			return nil, err
		}
		messages = append(convMessages, messages...)
	}

	logger.Debug("activity: LLM stream started", "scope", "activity", "runID", agentWorkflowID, "messageCount", len(messages))

	req, tools := rt.buildLLMRequest(messages, input.SkipTools)

	emitDelta := func(ev events.AgentEvent) {
		rt.publishAgentEventToStream(ctx, agentName, input.LocalChannelName, input.EventWorkflowID, input.EventTaskQueue, ev)
	}

	textMsgOpen := false
	openTextMsg := func() {
		if textMsgOpen {
			return
		}
		emitDelta(events.NewAgentTextMessageStartEvent(input.MessageID, string(interfaces.MessageRoleAssistant)))
		textMsgOpen = true
	}
	closeTextMsg := func() {
		if !textMsgOpen {
			return
		}
		emitDelta(events.NewAgentTextMessageEndEvent(input.MessageID))
		textMsgOpen = false
	}
	// If the model never sent text chunks, still emit one text message (empty for tool-only) to match one activity = one assistant turn.
	finalizeAssistantTextMessage := func(result *AgentLLMResult) {
		if textMsgOpen {
			closeTextMsg()
			return
		}
		openTextMsg()
		emitDelta(events.NewAgentTextMessageContentEvent(input.MessageID, result.Content))
		closeTextMsg()
	}

	isLLMStreamSupported := rt.AgentExecution.LLM.Client.IsStreamSupported()
	if !isLLMStreamSupported {
		logger.Debug("activity: LLM stream unsupported, using generate", "scope", "activity")
		resp, err := rt.AgentExecution.LLM.Client.Generate(ctx, req)
		if err != nil {
			return nil, err
		}
		result, err := rt.llmResponseToResult(resp, tools)
		if err != nil {
			return nil, err
		}
		finalizeAssistantTextMessage(result)
		return result, nil
	}

	stream, err := rt.AgentExecution.LLM.Client.GenerateStream(ctx, req)
	if err != nil {
		return nil, err
	}

	// Reasoning AG-UI order: REASONING_START → REASONING_MESSAGE_START → REASONING_MESSAGE_CONTENT* →
	// REASONING_MESSAGE_END → REASONING_END (flushed before the first assistant text delta, or at stream end).
	// reasoningMID is a new UUID per reasoning phase (regenerated after a prior phase is flushed).
	var reasoningMID string
	reasoningPhaseOpen := false
	reasoningMsgOpen := false
	flushReasoning := func() {
		if reasoningMsgOpen {
			emitDelta(events.NewAgentReasoningMessageEndEvent(reasoningMID))
			reasoningMsgOpen = false
		}
		if reasoningPhaseOpen {
			emitDelta(events.NewAgentReasoningEndEvent(reasoningMID))
			reasoningPhaseOpen = false
		}
	}
	openReasoning := func() {
		if reasoningPhaseOpen {
			return
		}
		reasoningMID = uuid.New().String()
		emitDelta(events.NewAgentReasoningStartEvent(reasoningMID))
		reasoningPhaseOpen = true
		emitDelta(events.NewAgentReasoningMessageStartEvent(reasoningMID, string(interfaces.MessageRoleReasoning)))
		reasoningMsgOpen = true
	}

	for stream.Next() {
		chunk := stream.Current()
		if chunk == nil {
			continue
		}
		if chunk.ContentDelta != "" {
			flushReasoning()
			openTextMsg()
			//TEXT_MESSAGE_CONTENT
			emitDelta(events.NewAgentTextMessageContentEvent(input.MessageID, chunk.ContentDelta))
		}
		if chunk.ThinkingDelta != "" {
			openReasoning()
			//REASONING_MESSAGE_CONTENT
			emitDelta(events.NewAgentReasoningMessageContentEvent(reasoningMID, chunk.ThinkingDelta))
		}
	}
	flushReasoning()
	if err := stream.Err(); err != nil {
		return nil, err
	}

	resp := stream.GetResult()
	if resp == nil {
		return nil, fmt.Errorf("stream completed without result")
	}
	logger.Debug("activity: LLM stream completed", "scope", "activity", "runID", agentWorkflowID)
	result, err := rt.llmResponseToResult(resp, tools)
	if err != nil {
		return nil, err
	}
	finalizeAssistantTextMessage(result)
	return result, nil
}

// buildLLMRequest builds an LLMRequest from messages and skipTools. Returns the request and tools list.
func (rt *TemporalRuntime) buildLLMRequest(messages []interfaces.Message, skipTools bool) (*interfaces.LLMRequest, []interfaces.Tool) {
	tools := rt.AgentExecution.Tools.Tools
	req := &interfaces.LLMRequest{
		SystemMessage:  rt.AgentSpec.SystemPrompt,
		ResponseFormat: rt.AgentSpec.ResponseFormat,
		Messages:       messages,
	}
	applyLLMSampling(rt.AgentExecution.LLM.Sampling, req)
	if skipTools {
		req.Tools = []interfaces.ToolSpec{}
	} else {
		req.Tools = interfaces.ToolsToSpecs(tools)
	}
	return req, tools
}

// fetchConversationMessages fetches messages for the LLM: fetches from conversation when ConversationID is set,
func (rt *TemporalRuntime) fetchConversationMessages(ctx context.Context, conversationID string) ([]interfaces.Message, error) {
	logger := activity.GetLogger(ctx)
	logger.Debug("activity: loading conversation history", "scope", "activity", "conversationID", conversationID)

	if rt.AgentExecution.Session.Conversation == nil {
		return nil, fmt.Errorf("conversation is not configured")
	}

	limit := rt.AgentExecution.Session.ConversationSize
	if limit <= 0 {
		limit = 20
	}

	messages, err := rt.AgentExecution.Session.Conversation.ListMessages(ctx, conversationID, interfaces.WithLimit(limit))
	if err != nil {
		return nil, fmt.Errorf("failed to list conversation messages: %w", err)
	}

	logger.Debug("activity: conversation history loaded", "scope", "activity", "messageCount", len(messages))
	return messages, nil
}

func (rt *TemporalRuntime) llmResponseToResult(resp *interfaces.LLMResponse, tools []interfaces.Tool) (*AgentLLMResult, error) {
	result := &AgentLLMResult{Content: resp.Content, Usage: cloneLLMUsagePtr(resp.Usage)}
	for _, tc := range resp.ToolCalls {
		if tc == nil {
			continue
		}
		tool, ok := findToolByName(tools, tc.ToolName)
		if !ok {
			return nil, fmt.Errorf("unknown tool: %s", tc.ToolName)
		}
		needsApproval := rt.requiresApproval(tool)
		displayName := tool.DisplayName()
		if displayName == "" {
			displayName = tc.ToolName
		}
		result.ToolCalls = append(result.ToolCalls, ToolCallRequest{
			ToolCallID:      tc.ToolCallID,
			ToolName:        tc.ToolName,
			ToolDisplayName: displayName,
			Args:            tc.Args,
			NeedsApproval:   needsApproval,
		})
	}
	return result, nil
}

// AgentLLMActivity calls the LLM and returns content plus any tool calls.
// When input.ConversationID is set, fetches from store and adds assistant message on completion.
func (rt *TemporalRuntime) AgentLLMActivity(ctx context.Context, input AgentLLMInput) (*AgentLLMResult, error) {
	if err := rt.verifyAgentFingerprint(input.AgentFingerprint); err != nil {
		return nil, err
	}
	logger := activity.GetLogger(ctx)

	messages := input.Messages
	if input.ConversationID != "" {
		convMessages, err := rt.fetchConversationMessages(ctx, input.ConversationID)
		if err != nil {
			return nil, err
		}
		messages = append(convMessages, messages...)
	}

	logger.Debug("activity: LLM generate started", "scope", "activity", "messageCount", len(messages))
	req, tools := rt.buildLLMRequest(messages, input.SkipTools)
	resp, err := rt.AgentExecution.LLM.Client.Generate(ctx, req)
	if err != nil {
		return nil, err
	}
	logger.Debug("activity: LLM generate completed", "scope", "activity", "messageCount", len(messages))
	result, err := rt.llmResponseToResult(resp, tools)
	if err != nil {
		return nil, err
	}
	agentNameTrim := strings.TrimSpace(input.AgentName)
	publish := func(ev events.AgentEvent) {
		rt.publishAgentEventToStream(ctx, agentNameTrim, input.LocalChannelName, input.EventWorkflowID, input.EventTaskQueue, ev)
	}
	publish(events.NewAgentTextMessageStartEvent(input.MessageID, string(interfaces.MessageRoleAssistant)))
	publish(events.NewAgentTextMessageContentEvent(input.MessageID, result.Content))
	publish(events.NewAgentTextMessageEndEvent(input.MessageID))
	return result, nil
}

// AgentToolApprovalActivity blocks until the driver completes it via CompleteActivity.
// Publishes a CUSTOM (tool_approval / delegation) event to the local agent_event channel (Run and Stream).
// When EventWorkflowID is set, UpdateWorkflow uses WorkflowUpdateStageCompleted and updateWorkflowApprovalRPCTimeout
// so the event handler has returned before ErrResultPending; RPC timeout maps to ApprovalStatusUnavailable.
func (rt *TemporalRuntime) AgentToolApprovalActivity(ctx context.Context, input AgentToolApprovalInput) (types.ApprovalStatus, error) {
	if err := rt.verifyAgentFingerprint(input.AgentFingerprint); err != nil {
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
	if err := rt.verifyAgentFingerprint(input.AgentFingerprint); err != nil {
		return err
	}
	conversationID := input.ConversationID
	messages := input.Messages
	logger := activity.GetLogger(ctx)

	msgCount := len(messages)

	logger.Debug("activity: add conversation messages started", "scope", "activity", "conversationID", conversationID, "messagesCount", msgCount)

	if rt.AgentExecution.Session.Conversation == nil {
		return fmt.Errorf("conversation is not configured")
	}

	for _, msg := range messages {
		if err := rt.AgentExecution.Session.Conversation.AddMessage(ctx, conversationID, msg); err != nil {
			msgCount--
			logger.Warn("activity: add conversation message failed", "scope", "activity", "conversationID", conversationID, "error", err)
		}
	}

	logger.Debug("activity: add conversation messages completed", "scope", "activity", "conversationID", conversationID, "messagesCount", msgCount)
	return nil
}

// AgentToolExecuteActivity executes a tool by name and adds tool message to conversation when ConversationID is set.
func (rt *TemporalRuntime) AgentToolExecuteActivity(ctx context.Context, input AgentToolExecuteInput) (string, error) {
	if err := rt.verifyAgentFingerprint(input.AgentFingerprint); err != nil {
		return "", err
	}
	stopHB := startLongActivityHeartbeats(ctx)
	defer stopHB()
	toolName := input.ToolName
	args := input.Args
	logger := activity.GetLogger(ctx)
	logger.Debug("activity: tool execute started", "scope", "activity", "tool", toolName, "argCount", len(args))
	tools := rt.AgentExecution.Tools.Tools
	tool, ok := findToolByName(tools, toolName)
	if !ok {
		logger.Warn("activity: unknown tool", "scope", "activity", "tool", toolName)
		return "", fmt.Errorf("unknown tool: %s", toolName)
	}
	result, err := tool.Execute(ctx, args)
	if err != nil {
		return "", err
	}
	content := fmt.Sprintf("%v", result)
	logger.Debug("activity: tool execute completed", "scope", "activity", "tool", toolName)
	return content, nil
}

// AgentToolAuthorizeActivity checks optional programmatic authorization before approval/execute.
func (rt *TemporalRuntime) AgentToolAuthorizeActivity(ctx context.Context, input AgentToolAuthorizeInput) (AgentToolAuthorizeResult, error) {
	if err := rt.verifyAgentFingerprint(input.AgentFingerprint); err != nil {
		return AgentToolAuthorizeResult{}, err
	}
	toolName := input.ToolName
	args := input.Args
	logger := activity.GetLogger(ctx)
	logger.Debug("activity: tool authorize started", "scope", "activity", "tool", toolName, "argCount", len(args))
	tools := rt.AgentExecution.Tools.Tools
	tool, ok := findToolByName(tools, toolName)
	if !ok {
		logger.Warn("activity: unknown tool in authorization", "scope", "activity", "tool", toolName)
		return AgentToolAuthorizeResult{}, fmt.Errorf("unknown tool: %s", toolName)
	}
	authorizer, ok := tool.(interfaces.ToolAuthorizer)
	if !ok {
		logger.Debug("activity: tool has no authorizer; allow by default", "scope", "activity", "tool", toolName)
		return AgentToolAuthorizeResult{Allowed: true}, nil
	}
	decision, err := authorizer.Authorize(ctx, args)
	if err != nil {
		logger.Warn("activity: tool authorization failed", "scope", "activity", "tool", toolName, "error", err)
		return AgentToolAuthorizeResult{}, err
	}
	if decision.Allow {
		logger.Debug("activity: tool authorization allowed", "scope", "activity", "tool", toolName)
		return AgentToolAuthorizeResult{Allowed: true}, nil
	}
	reason := strings.TrimSpace(decision.Reason)
	logger.Info("activity: tool authorization denied", "scope", "activity", "tool", toolName, "reason", reason)
	return AgentToolAuthorizeResult{
		Allowed: false,
		Reason:  reason,
	}, nil
}

func findToolByName(tools []interfaces.Tool, toolName string) (interfaces.Tool, bool) {
	for _, t := range tools {
		if t.Name() == toolName {
			return t, true
		}
	}
	return nil, false
}

func (rt *TemporalRuntime) delegateToSubAgent(ctx workflow.Context, input AgentWorkflowInput, tc ToolCallRequest, route types.SubAgentRoute, emitEvent func(events.AgentEvent) error) (string, error) {
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

	query := subAgentQueryFromArgs(tc.Args)
	childInput := AgentWorkflowInput{
		UserPrompt:       query,
		EventWorkflowID:  input.EventWorkflowID,
		EventTaskQueue:   input.EventTaskQueue,
		LocalChannelName: input.LocalChannelName,
		StreamingEnabled: input.StreamingEnabled,
		ConversationID:   "",
		AgentFingerprint: route.AgentFingerprint,
		EventTypes:       input.EventTypes,
		SubAgentDepth:    input.SubAgentDepth + 1,
		SubAgentRoutes:   route.ChildRoutes,
	}

	var childSuffix string
	if err := workflow.SideEffect(ctx, func(workflow.Context) interface{} {
		return uuid.New().String()
	}).Get(&childSuffix); err != nil {
		logger.Warn("workflow: sub-agent child run id failed", "scope", "workflow", "error", err)
		return "", err
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

	stepName := strings.TrimSpace(route.Name)
	if stepName == "" {
		stepName = tc.ToolName
	}

	if emitErr := emitEvent(events.NewAgentStepStartedEvent(stepName)); emitErr != nil {
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

	if emitErr := emitEvent(events.NewAgentStepFinishedEvent(stepName)); emitErr != nil {
		return "", emitErr
	}

	logger.Debug("workflow: sub-agent child run completed",
		"scope", "workflow",
		"childWorkflowID", childWfID,
		"tool", tc.ToolName,
		"resultContentLen", len(childResult.Content))

	return childResult.Content, nil
}

func (rt *TemporalRuntime) requiresApproval(t interfaces.Tool) bool {
	if rt.AgentExecution.Tools.ApprovalPolicy == nil {
		// No policy: honor tool's ApprovalRequired
		if ar, ok := t.(interfaces.ToolApproval); ok && ar.ApprovalRequired() {
			return true
		}
		return false
	}
	// Policy set: policy decides (can override tool default)
	return rt.AgentExecution.Tools.ApprovalPolicy.RequiresApproval(t)
}

func subAgentQueryFromArgs(args map[string]any) string {
	if args == nil {
		return ""
	}
	q, _ := args[types.SubAgentToolParamQuery].(string)
	return q
}

// subAgentChildWorkflowTimeout caps how long the main agent waits on a delegated sub-agent run.
// Uses the main agent worker's agent timeout (same package as delegateToSubAgent); sub-agent workers may define
// their own limits separately, but this bounds the child execution from the main agent's perspective.
func (rt *TemporalRuntime) subAgentChildWorkflowTimeout() time.Duration {
	return rt.AgentExecution.Limits.Timeout
}

func mergeLLMUsage(acc *interfaces.LLMUsage, add *interfaces.LLMUsage) *interfaces.LLMUsage {
	if add == nil {
		return acc
	}
	if acc == nil {
		return cloneLLMUsagePtr(add)
	}
	return &interfaces.LLMUsage{
		PromptTokens:       acc.PromptTokens + add.PromptTokens,
		CompletionTokens:   acc.CompletionTokens + add.CompletionTokens,
		TotalTokens:        acc.TotalTokens + add.TotalTokens,
		CachedPromptTokens: acc.CachedPromptTokens + add.CachedPromptTokens,
		ReasoningTokens:    acc.ReasoningTokens + add.ReasoningTokens,
	}
}

func cloneLLMUsagePtr(u *interfaces.LLMUsage) *interfaces.LLMUsage {
	if u == nil {
		return nil
	}
	c := *u
	return &c
}

func retryPolicy(maxAttempts int32) *temporal.RetryPolicy {
	return &temporal.RetryPolicy{
		InitialInterval:    time.Second,
		BackoffCoefficient: 2,
		MaximumInterval:    10 * time.Minute,
		MaximumAttempts:    maxAttempts,
	}
}

func applyLLMSampling(sampling *types.LLMSampling, req *interfaces.LLMRequest) {
	if sampling == nil {
		return
	}
	s := sampling
	if s.Temperature != nil {
		req.Temperature = s.Temperature
	}
	if s.MaxTokens > 0 {
		req.MaxTokens = s.MaxTokens
	}
	if s.TopP != nil {
		req.TopP = s.TopP
	}
	if s.TopK != nil {
		req.TopK = s.TopK
	}
	if s.Reasoning != nil {
		r := *s.Reasoning
		req.Reasoning = &r
	}
}
