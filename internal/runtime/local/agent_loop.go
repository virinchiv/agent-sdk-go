package local

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/agenticenv/agent-sdk-go/internal/events"
	"github.com/agenticenv/agent-sdk-go/internal/runtime/base"
	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/google/uuid"
)

const (
	msgToolRejected            = "Tool execution was rejected by the user."
	msgToolApprovalUnavailable = "Tool approval could not be completed because no approval handler is configured; continuing without running the tool."
	msgToolUnauthorized        = "Tool execution was denied by authorization policy."
)

// AgentLoopInput holds per-run execution inputs for one local agent run.
// Mirrors AgentWorkflowInput (Temporal) for in-process execution — same fields, same semantics.
// Static agent wiring lives on the runtime [base.Runtime.AgentConfig]; resolved tools are per-run on Tools.
type AgentLoopInput struct {
	UserPrompt       string
	ConversationID   string
	StreamingEnabled bool
	// Tools is the resolved tool list for this run.
	Tools []interfaces.Tool
	// ChannelName is the eventbus channel events are published to during this run.
	// Sub-agents receive the parent's ChannelName so their events go directly to the parent stream.
	// Empty = no event fanout.
	ChannelName string
	// ApprovalHandler is called when a tool requires human approval. May be nil (approval → unavailable).
	ApprovalHandler types.ApprovalHandler
	// SubAgentRoutes maps sub-agent tool name → local route. Built by the local runtime from
	// ExecuteRequest.SubAgents before RunAgentLoop is called. Mirrors AgentWorkflowInput.SubAgentRoutes.
	SubAgentRoutes map[string]subAgentRoute
	// SubAgentDepth is the current nesting depth (0 = top-level, 1 = direct sub-agent, etc.).
	SubAgentDepth int
	// MaxSubAgentDepth caps recursive delegation. Mirrors AgentWorkflowInput.MaxSubAgentDepth.
	MaxSubAgentDepth int
	// MemoryScope is resolved before the run and used for recall/store.
	MemoryScope interfaces.MemoryScope
}

// AgentLoopResult is the outcome of a completed local agent run.
type AgentLoopResult struct {
	Content   string
	LLMUsage  *interfaces.LLMUsage
	Telemetry *types.AgentTelemetry
}

type toolResult struct {
	message interfaces.Message
	failed  bool // true: hard err, ExecuteTool err, or ctx cancel
}

// publishEventToChannel marshals ev and publishes it on channelName via the runtime eventbus.
// A nil eventbus, empty channel, or nil event is a no-op.
func (rt *LocalRuntime) publishEventToChannel(ctx context.Context, channelName string, ev events.AgentEvent) {
	if ev == nil || strings.TrimSpace(channelName) == "" || rt.eventbus == nil {
		return
	}
	data, err := json.Marshal(ev)
	if err != nil {
		rt.logger.Warn(ctx, "local: failed to marshal agent event",
			slog.String("scope", "loop"),
			slog.Any("error", err))
		return
	}
	if err := rt.eventbus.Publish(ctx, channelName, data); err != nil {
		rt.logger.Warn(ctx, "local: failed to publish agent event",
			slog.String("scope", "loop"),
			slog.String("channel", channelName),
			slog.Any("error", err))
	}
}

// RunAgentLoop executes the full agent loop in-process using base.Runtime core methods.
// It mirrors the orchestration logic of AgentWorkflow but calls base methods directly
// instead of dispatching to Temporal activities.
// Events are published to rt.eventbus on input.ChannelName; callers subscribe to that channel.
func (rt *LocalRuntime) RunAgentLoop(ctx context.Context, input AgentLoopInput) (*AgentLoopResult, error) {
	log := rt.logger
	telemetry := base.NewAgentTelemetry(time.Now())
	agentName := rt.AgentSpec.Name
	model := rt.AgentConfig.LLM.Client.GetModel()

	ctx, sp := rt.Tracer.StartSpan(ctx, "agent.loop",
		interfaces.Attribute{Key: "agent.name", Value: agentName},
		interfaces.Attribute{Key: "model", Value: model},
	)
	defer sp.End()

	tools := input.Tools

	maxIter := rt.AgentConfig.Limits.MaxIterations
	if maxIter <= 0 {
		maxIter = 10
	}

	toolExecMode := rt.ToolExecutionMode
	if toolExecMode == "" {
		toolExecMode = types.AgentToolExecutionModeParallel
	}

	// Internal emit: publish events to the eventbus channel for this run.
	emit := func(ev events.AgentEvent) {
		rt.publishEventToChannel(ctx, input.ChannelName, ev)
	}

	// Build initial message list from user prompt.
	messages := []interfaces.Message{
		{Role: interfaces.MessageRoleUser, Content: input.UserPrompt},
	}

	// Prepend conversation history when conversation memory is configured for this run.
	persistedMessageCount := 0
	if rt.conversationMemoryEnabled(input) {
		convMsgs, err := rt.FetchConversationMessages(ctx, log, input.ConversationID)
		if err != nil {
			log.Warn(ctx, "local: failed to load conversation history, continuing without it",
				slog.String("scope", "loop"),
				slog.String("conversationID", input.ConversationID),
				slog.Any("error", err))
		} else {
			messages = append(convMsgs, messages...)
			persistedMessageCount = len(convMsgs)
		}
	}

	// Pre-fetch long-term memory context when recall is enabled.
	memoryContext := ""
	if rt.MemoryConfigured() && rt.RecallEnabled() {
		log.Debug(ctx, "local: memory recall started", slog.String("scope", "loop"))
		res, err := rt.ExecuteMemoryRecall(ctx, log, input.MemoryScope, input.UserPrompt)
		if err != nil {
			return nil, fmt.Errorf("memory recall: %w", err)
		}
		memoryContext = res.Context
		telemetry.Storage.TotalMemoryRecalls += res.TotalRecalls
		telemetry.Storage.FailedMemoryRecalls += res.FailedRecalls
		log.Debug(ctx, "local: memory recall done",
			slog.String("scope", "loop"),
			slog.Bool("hasContext", memoryContext != ""))
	}

	// Pre-fetch retriever context for prefetch/hybrid modes.
	retrieverContext := ""
	retrieverMode := rt.AgentConfig.Retrievers.Mode
	if (retrieverMode == types.RetrieverModePrefetch || retrieverMode == types.RetrieverModeHybrid) &&
		len(rt.AgentConfig.Retrievers.Retrievers) > 0 {
		log.Debug(ctx, "local: retriever prefetch started",
			slog.String("scope", "loop"),
			slog.String("mode", string(retrieverMode)),
			slog.Int("retrieverCount", len(rt.AgentConfig.Retrievers.Retrievers)))
		res, err := rt.ExecuteRetrievers(ctx, log, input.UserPrompt)
		if err != nil {
			return nil, fmt.Errorf("retriever prefetch: %w", err)
		}
		retrieverContext = res.Context
		telemetry.Storage.TotalRetrieverSearches += res.TotalSearches
		telemetry.Storage.FailedRetrieverSearches += res.FailedSearches
		telemetry.Storage.PrefetchSearches += res.TotalSearches
		log.Debug(ctx, "local: retriever prefetch done",
			slog.String("scope", "loop"),
			slog.Bool("hasContext", retrieverContext != ""))
	}

	var lastContent string
	var llmUsage *interfaces.LLMUsage

	for iter := 0; iter < maxIter; iter++ {
		messageID := uuid.New().String()
		log.Debug(ctx, "local: LLM call started",
			slog.String("scope", "loop"),
			slog.Int("iteration", iter),
			slog.Int("messageCount", len(messages)))

		var llmResult *base.LLMResult
		var err error
		executeLLMInput := base.ExecuteLLMInput{
			Logger:           log,
			AgentName:        agentName,
			MessageID:        messageID,
			Messages:         messages,
			SkipTools:        false,
			MemoryContext:    memoryContext,
			RetrieverContext: retrieverContext,
			Tools:            tools,
			Emit:             emit,
		}
		if input.StreamingEnabled {
			llmResult, err = rt.ExecuteLLMStream(ctx, executeLLMInput)
		} else {
			llmResult, err = rt.ExecuteLLM(ctx, executeLLMInput)
		}
		if err != nil {
			return nil, fmt.Errorf("llm call (iter %d): %w", iter, err)
		}

		telemetry.Run.TotalLLMCalls++
		llmUsage = base.MergeLLMUsage(llmUsage, llmResult.Usage)

		// Final response: no tool calls → done.
		if len(llmResult.ToolCalls) == 0 {
			messages = append(messages, interfaces.Message{
				Role:    interfaces.MessageRoleAssistant,
				Content: llmResult.Content,
			})
			lastContent = llmResult.Content
			break
		}

		// Max iterations: re-run without tools for a final answer.
		if iter == maxIter-1 {
			log.Info(ctx, "local: max iterations reached, forcing final LLM call without tools",
				slog.String("scope", "loop"),
				slog.Int("iteration", iter))
			finalMessageID := uuid.New().String()
			executeLLMInput := base.ExecuteLLMInput{
				Logger:           log,
				AgentName:        agentName,
				MessageID:        finalMessageID,
				Messages:         messages,
				SkipTools:        true,
				MemoryContext:    memoryContext,
				RetrieverContext: retrieverContext,
				Tools:            tools,
				Emit:             emit,
			}
			if input.StreamingEnabled {
				llmResult, err = rt.ExecuteLLMStream(ctx, executeLLMInput)
			} else {
				llmResult, err = rt.ExecuteLLM(ctx, executeLLMInput)
			}
			if err != nil {
				return nil, fmt.Errorf("llm final call (iter %d): %w", iter, err)
			}
			llmUsage = base.MergeLLMUsage(llmUsage, llmResult.Usage)
			messages = append(messages, interfaces.Message{
				Role:    interfaces.MessageRoleAssistant,
				Content: llmResult.Content,
			})
			lastContent = llmResult.Content
			telemetry.Run.TotalLLMCalls++
			telemetry.Run.FinishReason = types.FinishReasonMaxIterations
			break
		}

		// Append assistant message with tool call metadata for next iteration.
		assistantMsg := interfaces.Message{
			Role:      interfaces.MessageRoleAssistant,
			Content:   llmResult.Content,
			ToolCalls: make([]*interfaces.ToolCall, len(llmResult.ToolCalls)),
		}
		for i, tc := range llmResult.ToolCalls {
			assistantMsg.ToolCalls[i] = &interfaces.ToolCall{
				ToolCallID: tc.ToolCallID,
				ToolName:   tc.ToolName,
				Args:       tc.Args,
			}
		}
		messages = append(messages, assistantMsg)

		// Execute tools according to the requested execution mode.
		var toolResults []toolResult
		switch toolExecMode {
		case types.AgentToolExecutionModeParallel:
			toolResults, err = rt.executeToolsParallel(ctx, input, messageID, llmResult.ToolCalls, emit)
		case types.AgentToolExecutionModeSequential:
			toolResults, err = rt.executeToolsSequential(ctx, input, messageID, llmResult.ToolCalls, emit)
		default:
			return nil, fmt.Errorf("invalid tool execution mode %q: use %q or %q",
				toolExecMode,
				types.AgentToolExecutionModeParallel,
				types.AgentToolExecutionModeSequential)
		}
		if err != nil {
			return nil, err
		}

		for idx, result := range toolResults {
			messages = append(messages, result.message)
			tc := llmResult.ToolCalls[idx]
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
		}

		if rt.conversationMemoryEnabled(input) && rt.AgentConfig.Session.ConversationSaveOnIteration && len(messages) > persistedMessageCount {
			rt.persistConversationMessages(ctx, input.ConversationID, messages[persistedMessageCount:])
			persistedMessageCount = len(messages)
		}
	}

	// Persist unsaved messages: full run when ConversationSaveOnIteration is false; final assistant only when true.
	if rt.conversationMemoryEnabled(input) && len(messages) > persistedMessageCount {
		rt.persistConversationMessages(ctx, input.ConversationID, messages[persistedMessageCount:])
	}

	if rt.RunEndMemoryStoreEnabled() {
		if err := rt.ExecuteMemoryStore(ctx, log, input.MemoryScope, messages); err != nil {
			log.Warn(ctx, "local: memory store failed", slog.String("scope", "loop"), slog.Any("error", err))
			telemetry.Storage.FailedMemoryStores++
		} else {
			telemetry.Storage.TotalMemoryStores++
		}
	}

	log.Info(ctx, "local: agent run completed",
		slog.String("scope", "loop"),
		slog.String("agentName", agentName),
		slog.String("model", model),
		slog.Int("contentLen", len(lastContent)))

	telemetry.Run.CompletedAt = time.Now()

	return &AgentLoopResult{
		Content:   lastContent,
		LLMUsage:  llmUsage,
		Telemetry: telemetry,
	}, nil
}

func (rt *LocalRuntime) conversationMemoryEnabled(input AgentLoopInput) bool {
	return input.ConversationID != "" && rt.AgentConfig.Session.Conversation != nil
}

// executeToolsParallel runs all tool calls concurrently and collects results in submission order.
// Errors from individual tools are returned as synthetic tool messages so the LLM can handle
// partial failures gracefully (same behaviour as the Temporal parallel branch).
func (rt *LocalRuntime) executeToolsParallel(
	ctx context.Context,
	input AgentLoopInput,
	messageID string,
	toolCalls []base.ToolCallRequest,
	emit func(events.AgentEvent),
) ([]toolResult, error) {
	rt.logger.Info(ctx, "local: tool execution (parallel)",
		slog.String("scope", "loop"),
		slog.Int("toolCount", len(toolCalls)))

	results := make([]toolResult, len(toolCalls))
	var wg sync.WaitGroup

	for i := range toolCalls {
		wg.Add(1)
		go func(idx int, tc base.ToolCallRequest) {
			defer wg.Done()
			result, err := rt.executeSingleTool(ctx, input, messageID, tc, emit)
			if err != nil {
				rt.logger.Info(ctx, "local: parallel tool failed",
					slog.String("scope", "loop"),
					slog.Int("toolIndex", idx),
					slog.String("toolName", tc.ToolName),
					slog.Any("error", err))
				result.message = interfaces.Message{
					Role:       interfaces.MessageRoleTool,
					Content:    "Tool execution failed: " + err.Error(),
					ToolName:   tc.ToolName,
					ToolCallID: tc.ToolCallID,
				}
				result.failed = true
			}
			results[idx] = result
		}(i, toolCalls[i])
	}
	wg.Wait()

	return results, nil
}

// executeToolsSequential runs tool calls one at a time.
// Hard errors from individual tools become synthetic tool messages; the batch always continues.
func (rt *LocalRuntime) executeToolsSequential(
	ctx context.Context,
	input AgentLoopInput,
	messageID string,
	toolCalls []base.ToolCallRequest,
	emit func(events.AgentEvent),
) ([]toolResult, error) {
	rt.logger.Info(ctx, "local: tool execution (sequential)",
		slog.String("scope", "loop"),
		slog.Int("toolCount", len(toolCalls)))

	results := make([]toolResult, len(toolCalls))
	for idx, tc := range toolCalls {
		result, err := rt.executeSingleTool(ctx, input, messageID, tc, emit)
		if err != nil {
			rt.logger.Info(ctx, "local: sequential tool failed",
				slog.String("scope", "loop"),
				slog.Int("toolIndex", idx),
				slog.String("toolName", tc.ToolName),
				slog.Any("error", err))

			result.message = interfaces.Message{
				Role:       interfaces.MessageRoleTool,
				Content:    "Tool execution failed: " + err.Error(),
				ToolName:   tc.ToolName,
				ToolCallID: tc.ToolCallID,
			}
			result.failed = true
		}
		results[idx] = result
	}
	return results, nil
}

// executeSingleTool runs the full lifecycle for one tool call:
// authorize → approval (if needed) → execute, emitting TOOL_CALL_* events throughout.
func (rt *LocalRuntime) executeSingleTool(
	ctx context.Context,
	input AgentLoopInput,
	messageID string,
	tc base.ToolCallRequest,
	emit func(events.AgentEvent),
) (toolResult, error) {
	log := rt.logger
	tools := input.Tools

	emitToolEndThenResult := func(toolCallID, content string) {
		emit(events.NewAgentToolCallEndEvent(toolCallID))
		emit(events.NewAgentToolCallResultEvent(messageID, toolCallID, content, string(interfaces.MessageRoleTool)))
	}

	// TOOL_CALL_START
	emit(events.NewAgentToolCallStartEvent(tc.ToolCallID, tc.ToolName, messageID))

	// TOOL_CALL_ARGS (only when non-trivial args)
	if argsJSON, err := json.Marshal(tc.Args); err == nil {
		s := string(argsJSON)
		if s != "" && s != "null" && s != "{}" {
			emit(events.NewAgentToolCallArgsEvent(tc.ToolCallID, s))
		}
	}

	// Authorization check.
	authResult, err := rt.AuthorizeTool(ctx, log, tools, tc.ToolName, tc.Args)
	if err != nil {
		return toolResult{
			message: interfaces.Message{},
			failed:  true,
		}, fmt.Errorf("tool authorization error for %q: %w", tc.ToolName, err)
	}
	if !authResult.Allowed {
		content := msgToolUnauthorized
		if authResult.Reason != "" {
			content = fmt.Sprintf("%s Reason: %s", content, authResult.Reason)
		}
		log.Info(ctx, "local: tool authorization denied",
			slog.String("scope", "loop"),
			slog.String("toolName", tc.ToolName),
			slog.String("reason", authResult.Reason))
		emitToolEndThenResult(tc.ToolCallID, content)
		return toolResult{
			message: interfaces.Message{
				Role:       interfaces.MessageRoleTool,
				Content:    content,
				ToolName:   tc.ToolName,
				ToolCallID: tc.ToolCallID,
			},
			failed: false,
		}, nil
	}

	// Determine whether this tool call is a sub-agent delegation.
	subAgentRoute, isSubAgent := input.SubAgentRoutes[tc.ToolName]

	// Approval gate when required.
	approvalStatus := types.ApprovalStatusApproved
	if tc.NeedsApproval {
		// No channel (non-streaming Execute) and no handler: skip approval.
		if input.ChannelName == "" && input.ApprovalHandler == nil {
			approvalStatus = types.ApprovalStatusUnavailable
		} else {
			// Generate a token and register a resolve channel so either path can unblock:
			//   - Streaming: caller receives CUSTOM event with token, calls rt.Approve(token, status)
			//   - Non-streaming with handler: handler calls approvalReq.Respond(status) directly
			token := uuid.New().String()
			resultCh := make(chan types.ApprovalStatus, 1)
			rt.pendingApprovals.Store(token, resultCh)
			defer rt.pendingApprovals.Delete(token)

			if isSubAgent {
				approvalReq := &types.ApprovalRequest{
					Name: types.ApprovalRequestNameSubAgent,
					Value: types.SubAgentDelegationApprovalRequestValue{
						AgentName:     rt.AgentSpec.Name,
						SubAgentName:  tc.ToolDisplayName,
						Args:          tc.Args,
						ApprovalToken: token,
					},
					Respond: func(status types.ApprovalStatus) error { resultCh <- status; return nil },
				}
				emit(events.NewAgentCustomEvent(string(events.AgentCustomEventNameSubAgentDelegation),
					events.AgentCustomEventDelegationValue{
						AgentName:     rt.AgentSpec.Name,
						SubAgentName:  tc.ToolDisplayName,
						Args:          tc.Args,
						ApprovalToken: token,
					}))
				if input.ApprovalHandler != nil {
					input.ApprovalHandler(ctx, approvalReq)
				}
			} else {
				approvalReq := &types.ApprovalRequest{
					Name: types.ApprovalRequestNameTool,
					Value: types.ToolApprovalRequestValue{
						AgentName:       rt.AgentSpec.Name,
						ToolCallID:      tc.ToolCallID,
						ToolName:        tc.ToolName,
						ToolDisplayName: tc.ToolDisplayName,
						Args:            tc.Args,
						ApprovalToken:   token,
					},
					Respond: func(status types.ApprovalStatus) error { resultCh <- status; return nil },
				}
				emit(events.NewAgentCustomEvent(string(events.AgentCustomEventNameToolApproval),
					events.AgentCustomEventApprovalValue{
						AgentName:       rt.AgentSpec.Name,
						ToolCallID:      tc.ToolCallID,
						ToolName:        tc.ToolName,
						ToolDisplayName: tc.ToolDisplayName,
						Args:            tc.Args,
						ApprovalToken:   token,
					}))
				if input.ApprovalHandler != nil {
					input.ApprovalHandler(ctx, approvalReq)
				}
			}
			// Streaming path: handler is nil; caller calls rt.Approve(token, status) → resultCh.

			select {
			case status := <-resultCh:
				approvalStatus = status
			case <-ctx.Done():
				return toolResult{
					message: interfaces.Message{},
					failed:  true,
				}, ctx.Err()
			}
		}
	}

	var content string
	failed := false
	switch approvalStatus {
	case types.ApprovalStatusApproved:
		if isSubAgent {
			stepName := strings.TrimSpace(subAgentRoute.name)
			if stepName == "" {
				stepName = tc.ToolName
			}
			if input.SubAgentDepth >= input.MaxSubAgentDepth {
				log.Warn(ctx, "local: sub-agent delegation refused (max depth)",
					slog.String("scope", "loop"),
					slog.Int("depth", input.SubAgentDepth),
					slog.Int("maxDepth", input.MaxSubAgentDepth),
					slog.String("toolName", tc.ToolName))
				content = fmt.Sprintf("Sub-agent delegation refused: maximum nesting depth (%d) reached.", input.MaxSubAgentDepth)
			} else if subAgentRoute.runtime != nil {
				query := base.SubAgentQuery(tc.Args)
				log.Info(ctx, "local: delegating to sub-agent",
					slog.String("scope", "loop"),
					slog.String("toolName", tc.ToolName),
					slog.String("stepName", stepName),
					slog.Int("depth", input.SubAgentDepth+1))
				emit(events.NewAgentStepStartedEvent(stepName))
				subResult, execErr := subAgentRoute.runtime.RunAgentLoop(ctx, AgentLoopInput{
					UserPrompt:       query,
					StreamingEnabled: input.StreamingEnabled,
					ChannelName:      input.ChannelName,
					ApprovalHandler:  input.ApprovalHandler,
					MemoryScope:      base.SubAgentScope(input.MemoryScope, stepName),
					SubAgentRoutes:   subAgentRoute.children,
					SubAgentDepth:    input.SubAgentDepth + 1,
					MaxSubAgentDepth: input.MaxSubAgentDepth,
					Tools:            subAgentRoute.tools,
				})
				emit(events.NewAgentStepFinishedEvent(stepName))
				if execErr != nil {
					content = "Sub-agent execution failed: " + execErr.Error()
				} else {
					content = subResult.Content
				}
			} else {
				content = "Sub-agent delegation not available for this runtime."
			}
		} else {
			log.Info(ctx, "local: executing tool",
				slog.String("scope", "loop"),
				slog.String("tool", tc.ToolName),
				slog.String("toolCallID", tc.ToolCallID))
			result, execErr := rt.ExecuteToolWithMemoryScope(ctx, log, tools, tc.ToolName, tc.Args, input.MemoryScope)
			if execErr != nil {
				content = "Tool execution failed: " + execErr.Error()
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
		return toolResult{
			message: interfaces.Message{},
			failed:  true,
		}, fmt.Errorf("unexpected approval status %q for tool %q", approvalStatus, tc.ToolName)
	}

	emitToolEndThenResult(tc.ToolCallID, content)
	return toolResult{
		message: interfaces.Message{
			Role:       interfaces.MessageRoleTool,
			Content:    content,
			ToolName:   tc.ToolName,
			ToolCallID: tc.ToolCallID,
		},
		failed: failed,
	}, nil
}

// persistConversationMessages stores messages in the conversation store.
// Logs per-message failures and continues; does not fail the run.
func (rt *LocalRuntime) persistConversationMessages(ctx context.Context, conversationID string, messages []interfaces.Message) {
	conv := rt.AgentConfig.Session.Conversation
	if conv == nil || len(messages) == 0 {
		return
	}

	ctx, sp := rt.Tracer.StartSpan(ctx, "conversation.add_messages",
		interfaces.Attribute{Key: "conversation.id", Value: conversationID},
		interfaces.Attribute{Key: "message.count", Value: len(messages)},
	)
	defer sp.End()

	failCount := 0
	for _, msg := range messages {
		if err := conv.AddMessage(ctx, conversationID, msg); err != nil {
			failCount++
			rt.logger.Warn(ctx, "local: add conversation message failed",
				slog.String("scope", "loop"),
				slog.String("conversationID", conversationID),
				slog.Any("error", err))
		}
	}
	if failCount > 0 {
		sp.SetAttribute("failed.count", failCount)
	}
}
