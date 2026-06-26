package local

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/agenticenv/agent-sdk-go/internal/eventbus"
	"github.com/agenticenv/agent-sdk-go/internal/events"
	sdkruntime "github.com/agenticenv/agent-sdk-go/internal/runtime"
	"github.com/agenticenv/agent-sdk-go/internal/runtime/base"
	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
	"github.com/google/uuid"
)

var _ sdkruntime.Runtime = (*LocalRuntime)(nil)
var _ sdkruntime.EventBusRuntime = (*LocalRuntime)(nil)

// LocalRuntime executes the agent loop in-process, embedding base.Runtime for shared
// core methods and holding local-specific fields (logger, eventbus).
type LocalRuntime struct {
	base.Runtime

	logger   logger.Logger
	eventbus eventbus.EventBus

	// pendingApprovals holds token → resolve channel for tools awaiting human approval.
	// Used by Approve() to unblock executeSingleTool when the caller responds via OnApproval
	// (streaming path). Thread-safe: parallel tool calls each register their own token.
	pendingApprovals sync.Map // key: string token, value: chan types.ApprovalStatus
}

// NewLocalRuntime constructs a LocalRuntime from functional options.
func NewLocalRuntime(opts ...Option) (*LocalRuntime, error) {
	r, err := buildLocalRuntime(opts...)
	if err != nil {
		return nil, err
	}
	r.logger.Info(context.Background(), "runtime created",
		slog.String("scope", "runtime"),
		slog.String("name", r.AgentSpec.Name))
	r.eventbus = eventbus.NewInmem(r.logger)
	return r, nil
}

// localChannelName returns the eventbus channel name for one run.
func localChannelName(runID string) string {
	return "agent-event-" + runID
}

// subscribeToAgentEvents subscribes to the run channel and returns a typed event channel
// plus a close function. Events are decoded from the raw JSON published by publishEventToChannel.
func (rt *LocalRuntime) subscribeToAgentEvents(ctx context.Context, channel string) (<-chan events.AgentEvent, func() error, error) {
	rawCh, closeFn, err := rt.eventbus.Subscribe(ctx, channel)
	if err != nil {
		return nil, nil, fmt.Errorf("local: subscribe to channel %q: %w", channel, err)
	}
	outCh := make(chan events.AgentEvent, 64)
	go func() {
		defer close(outCh)
		for data := range rawCh {
			ev, err := events.EventFromJSON(data)
			if err != nil {
				rt.logger.Warn(ctx, "local: failed to decode agent event",
					slog.String("scope", "runtime"),
					slog.Any("error", err))
				continue
			}
			if ev != nil {
				outCh <- ev
			}
		}
	}()
	return outCh, closeFn, nil
}

// publishLifecycleEvent publishes a lifecycle event (RUN_STARTED, RUN_FINISHED, RUN_ERROR) to the
// run channel. Uses context.Background so a cancelled runCtx never drops the terminal event.
func (rt *LocalRuntime) publishLifecycleEvent(channel string, ev events.AgentEvent) {
	if rt.eventbus == nil || channel == "" || ev == nil {
		return
	}
	data, err := json.Marshal(ev)
	if err != nil {
		return
	}
	if err := rt.eventbus.Publish(context.Background(), channel, data); err != nil {
		rt.logger.Warn(context.Background(), "local: lifecycle event publish failed",
			slog.String("scope", "runtime"),
			slog.String("channel", channel),
			slog.String("type", string(ev.Type())),
			slog.Any("error", err))
	}
}

// Execute runs the agent loop synchronously and returns the final result.
// Approval is handled inline via req.ApprovalHandler (no out-of-band tokens).
func (rt *LocalRuntime) Execute(ctx context.Context, req *sdkruntime.ExecuteRequest) (*types.AgentRunResult, error) {
	agentName := agentNameFromRuntime(rt)
	rt.logger.Debug(ctx, "runtime execute",
		slog.String("scope", "runtime"),
		slog.String("agent", agentName),
		slog.Int("inputLen", len(req.UserPrompt)))

	// Apply agent timeout when the caller has not set a deadline.
	runCtx := ctx
	if d := rt.AgentConfig.Limits.Timeout; d > 0 {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			var cancel context.CancelFunc
			runCtx, cancel = context.WithTimeout(ctx, d)
			defer cancel()
		}
	}

	conversationID := base.GetConversationID(req)
	memoryScope, memErr := rt.ResolveMemoryScope(runCtx)
	if memErr != nil {
		rt.logger.Warn(runCtx, "runtime memory scope resolve failed, continuing with empty scope",
			slog.String("scope", "runtime"),
			slog.Any("error", memErr))
		memoryScope = interfaces.MemoryScope{}
	}
	runID := uuid.New().String()

	tools := req.Tools

	loopResult, err := rt.RunAgentLoop(runCtx, AgentLoopInput{
		UserPrompt:       req.UserPrompt,
		RunID:            runID,
		ConversationID:   conversationID,
		MemoryScope:      memoryScope,
		StreamingEnabled: false,
		ChannelName:      "",
		ApprovalHandler:  req.ApprovalHandler,
		SubAgentRoutes:   buildSubAgentRoutes(req.SubAgents),
		SubAgentDepth:    0,
		MaxSubAgentDepth: req.MaxSubAgentDepth,
		Tools:            tools,
	})
	if err != nil {
		return nil, err
	}

	return &types.AgentRunResult{
		Content:   loopResult.Content,
		AgentName: strings.TrimSpace(agentName),
		Model:     rt.AgentConfig.LLM.Client.GetModel(),
		Metadata:  map[string]any{},
		LLMUsage:  loopResult.LLMUsage,
		Telemetry: loopResult.Telemetry,
	}, nil
}

// ExecuteStream starts the agent loop in a goroutine and returns a channel of AgentEvent.
// RUN_STARTED is emitted before the loop begins; RUN_FINISHED or RUN_ERROR closes the channel.
func (rt *LocalRuntime) ExecuteStream(ctx context.Context, req *sdkruntime.ExecuteRequest) (<-chan events.AgentEvent, error) {
	agentName := agentNameFromRuntime(rt)
	rt.logger.Debug(ctx, "runtime execute stream",
		slog.String("scope", "runtime"),
		slog.String("agent", agentName),
		slog.Int("inputLen", len(req.UserPrompt)))

	conversationID := base.GetConversationID(req)
	memoryScope, memErr := rt.ResolveMemoryScope(ctx)
	if memErr != nil {
		rt.logger.Warn(ctx, "runtime memory scope resolve failed, continuing with empty scope",
			slog.String("scope", "runtime"),
			slog.Any("error", memErr))
		memoryScope = interfaces.MemoryScope{}
	}
	runID := uuid.New().String()

	threadID := conversationID
	if threadID == "" {
		threadID = runID
	}
	channel := localChannelName(runID)

	// Apply agent timeout.
	runCtx := ctx
	var runCancel context.CancelFunc
	if d := rt.AgentConfig.Limits.Timeout; d > 0 {
		if _, hasDeadline := ctx.Deadline(); !hasDeadline {
			runCtx, runCancel = context.WithTimeout(ctx, d)
		}
	}

	// Subscribe before launching the loop so no events are lost.
	eventCh, closeSub, err := rt.subscribeToAgentEvents(runCtx, channel)
	if err != nil {
		if runCancel != nil {
			runCancel()
		}
		return nil, err
	}

	outCh := make(chan events.AgentEvent, 64)

	// Forward subscription events to the caller's channel.
	go func() {
		defer close(outCh)
		for ev := range eventCh {
			if ev != nil {
				outCh <- ev
			}
		}
	}()

	// Emit RUN_STARTED before the loop so callers always see the lifecycle preamble.
	rt.publishLifecycleEvent(channel, events.NewAgentRunStartedEvent(threadID, runID))

	// Run the agent loop in a goroutine; emit lifecycle terminal event on completion.
	go func() {
		var tools []interfaces.Tool
		if req != nil {
			tools = req.Tools
		}
		defer func() {
			if runCancel != nil {
				runCancel()
			}
			_ = closeSub()
		}()
		result, loopErr := rt.RunAgentLoop(runCtx, AgentLoopInput{
			UserPrompt:       req.UserPrompt,
			RunID:            runID,
			ConversationID:   conversationID,
			MemoryScope:      memoryScope,
			StreamingEnabled: req.StreamingEnabled,
			ChannelName:      channel,
			ApprovalHandler:  req.ApprovalHandler,
			SubAgentRoutes:   buildSubAgentRoutes(req.SubAgents),
			SubAgentDepth:    0,
			MaxSubAgentDepth: req.MaxSubAgentDepth,
			Tools:            tools,
		})

		if loopErr != nil {
			rt.logger.Error(runCtx, "runtime stream run failed",
				slog.String("scope", "runtime"),
				slog.String("runID", runID),
				slog.Any("error", loopErr))
			rt.publishLifecycleEvent(channel, events.NewAgentRunErrorEvent(loopErr.Error()))
			return
		}

		agentRunResult := &types.AgentRunResult{
			Content:   result.Content,
			AgentName: strings.TrimSpace(agentName),
			Model:     rt.AgentConfig.LLM.Client.GetModel(),
			Metadata:  map[string]any{},
			LLMUsage:  result.LLMUsage,
			Telemetry: result.Telemetry,
		}
		rt.publishLifecycleEvent(channel, events.NewAgentRunFinishedEvent(threadID, runID, agentRunResult))
	}()

	return outCh, nil
}

// Approve resolves a pending tool approval registered during a streaming run.
// When a tool requires approval, executeSingleTool registers a token and blocks; the
// caller receives a CUSTOM event on the stream with that token and calls Approve to unblock.
func (rt *LocalRuntime) Approve(_ context.Context, approvalToken string, status types.ApprovalStatus) error {
	val, ok := rt.pendingApprovals.LoadAndDelete(approvalToken)
	if !ok {
		return fmt.Errorf("local: no pending approval for token %q", approvalToken)
	}
	ch := val.(chan types.ApprovalStatus)
	ch <- status
	return nil
}

// Close releases runtime resources.
func (rt *LocalRuntime) Close() {
	rt.logger.Info(context.Background(), "runtime closed",
		slog.String("scope", "runtime"),
		slog.String("name", rt.AgentSpec.Name))
}

// GetEventBus returns the runtime's in-process event bus so pkg/agent can wire sub-agents
// to the same bus for streaming fan-in and delegation events.
func (rt *LocalRuntime) GetEventBus() eventbus.EventBus {
	return rt.eventbus
}

// SetEventBus replaces the runtime's event bus. Called by pkg/agent when wiring a sub-agent
// tree so all agents in the tree share the parent's bus.
func (rt *LocalRuntime) SetEventBus(bus eventbus.EventBus) {
	rt.eventbus = bus
}

func agentNameFromRuntime(rt *LocalRuntime) string {
	if rt == nil {
		return ""
	}
	return rt.AgentSpec.Name
}
