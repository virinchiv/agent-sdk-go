package agent

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"log/slog"

	"github.com/agenticenv/agent-sdk-go/internal/eventbus"
	"github.com/agenticenv/agent-sdk-go/internal/events"
	"github.com/agenticenv/agent-sdk-go/internal/runtime"
	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/memory"
)

// Agent runs LLM-backed agent execution through the configured execution runtime.
// It holds configuration, that runtime, and optionally an embedded [AgentWorker] for in-process polling.
// Sub-agents share the parent runtime's event bus for delegation and approvals in the same process.
type Agent struct {
	agentConfig
	runtime          runtime.Runtime
	localAgentWorker *AgentWorker // run worker; set when workers are embedded
}

// ErrAgentAlreadyRunning is returned when Run, RunAsync, or Stream is called while a run is already in progress.
var ErrAgentAlreadyRunning = errors.New("agent already has an active run")

// AgentRunOptions is the options to use runtime execution
type AgentRunOptions = types.AgentRunOptions

// ConversationOptions is the options to use for the conversation
type ConversationOptions = types.ConversationOptions

// AgentRunResult is the structured result of [Agent.Run] and [Agent.RunAsync] ([RunAsyncResult.Result]).
type AgentRunResult = types.AgentRunResult

// AgentRunAsyncResult is the single outcome from [Agent.RunAsync]. After the channel closes, Err is non-nil
// on failure; otherwise Result is non-nil.
type AgentRunAsyncResult = types.AgentRunAsyncResult

// AgentTelemetry is the unified container for operational insights across
// a single agent run, covering run lifecycle, tool calls, and storage operations.
// Token usage is reported separately on AgentRunResult.LLMUsage.
type AgentTelemetry = types.AgentTelemetry

// LLMUsage is the token usage for a single LLM call.
type LLMUsage = types.LLMUsage

// RunTelemetry captures the orchestration lifecycle metrics for a single agent run.
type RunTelemetry = types.RunTelemetry

// ToolTelemetry tracks tool invocation counts and per-tool breakdowns across a single agent run.
type ToolTelemetry = types.ToolTelemetry

// StorageTelemetry tracks RAG retrieval operations (prefetch, agentic, and hybrid searches).
type StorageTelemetry = types.StorageTelemetry

// buildAgent builds an Agent from options. Validates approval handler when tools require approval.
func buildAgent(opts []Option) (*Agent, error) {
	cfg, err := buildAgentConfig(opts)
	if err != nil {
		return nil, err
	}
	a := &Agent{
		agentConfig: *cfg,
	}

	// This guard is Temporal-specific: streaming on Temporal requires a local worker unless
	// remote workers are enabled. LocalRuntime streams in-process via ExecuteStream and needs
	// no background worker poll loop, so we skip the guard for the local backend.
	if cfg.hasTemporalRuntime() && a.disableLocalWorker && a.streamEnabled && !a.enableRemoteWorkers {
		return nil, fmt.Errorf("DisableLocalWorker with streaming requires EnableRemoteWorkers()")
	}

	rt, err := cfg.buildAgentRuntime(false)
	if err != nil {
		return nil, err
	}
	a.runtime = rt

	// Worker poll loop is only needed for backends that implement WorkerRuntime (e.g. Temporal).
	// LocalRuntime executes in-process via Execute/ExecuteStream; creating a worker for it would
	// log a spurious error because LocalRuntime does not implement WorkerRuntime.
	if !a.disableLocalWorker && cfg.hasTemporalRuntime() {
		a.localAgentWorker = &AgentWorker{agentConfig: *cfg, runtime: rt}
	}

	return a, nil
}

// NewAgent creates an Agent with the given options.
// Background runtime workers (when used) start lazily when [Agent.Stream] runs or when approvals need them.
func NewAgent(opts ...Option) (*Agent, error) {
	a, err := buildAgent(opts)
	if err != nil {
		return nil, err
	}
	a.logger.Info(context.Background(), "agent created", slog.String("scope", "agent"), slog.String("name", a.Name), slog.String("taskQueue", a.taskQueue), slog.Bool("embedWorker", a.localAgentWorker != nil))
	if a.localAgentWorker != nil {
		go func() {
			if err := a.localAgentWorker.Start(context.Background()); err != nil {
				a.logger.Error(context.Background(), "embedded agent worker failed to start", slog.String("scope", "agent"), slog.Any("error", err))
			}
		}()
	}
	return a, nil
}

// Close stops an embedded local worker if present, then closes the runtime (which may terminate runs,
// release remote resources, and close backend connections owned by the runtime, depending on the implementation).
// Only one run can be active per agent.
func (a *Agent) Close() {
	a.logger.Info(context.Background(), "closing agent", slog.String("scope", "agent"), slog.String("name", a.Name))

	ctx := context.Background()
	if a.localAgentWorker != nil {
		a.logger.Debug(ctx, "stopping local agent worker", slog.String("scope", "agent"))
		a.localAgentWorker.Stop()
	}

	a.runtime.Close()

	// Flush OTLP when built via [WithObservabilityConfig] (batched exporters need Shutdown). No-ops for noop.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	_ = a.tracer.Shutdown(shutdownCtx)
	_ = a.metrics.Shutdown(shutdownCtx)
	_ = a.logs.Shutdown(shutdownCtx)
	cancel()

	a.logger.Info(ctx, "agent closed", slog.String("scope", "agent"), slog.String("name", a.Name))
}

// Run starts one execution and returns the result. Use [WithApprovalHandler] when tools require approval for Run (handler uses req.Respond); [Stream] uses approval events and [Agent.OnApproval].
// Use [WithTimeout] or a context with deadline to avoid blocking.
// When using [WithConversation], pass the conversation ID; agent and worker must use the same ID.
func (a *Agent) Run(ctx context.Context, input string, opts *AgentRunOptions) (*AgentRunResult, error) {
	a.logger.Debug(ctx, "agent run started", slog.String("scope", "agent"), slog.String("name", a.Name), slog.Int("inputLen", len(input)))
	return a.runInternal(ctx, input, opts, false)
}

func (a *Agent) runInternal(ctx context.Context, input string, opts *AgentRunOptions, runAsync bool) (*AgentRunResult, error) {
	ctx = a.attachMemoryScopeContext(ctx)
	conversationID := conversationIDFromOpts(opts)

	spanName := "agent.run"
	if runAsync {
		spanName = "agent.run.async"
	}

	start := time.Now()
	ctx, sp := a.tracer.StartSpan(ctx, spanName,
		interfaces.Attribute{Key: "agent.name", Value: a.Name},
		interfaces.Attribute{Key: "conversation.id", Value: conversationID},
		interfaces.Attribute{Key: "input.length", Value: len(input)},
	)
	defer sp.End()
	a.metrics.IncrementCounter(ctx, types.MetricRunStarted)

	if err := a.validateConversationID(conversationID); err != nil {
		sp.RecordError(err)
		a.metrics.IncrementCounter(ctx, types.MetricRunFailed, interfaces.Attribute{Key: "error", Value: "conversation_id_invalid"})
		a.metrics.RecordHistogram(ctx, types.MetricRunDurationMs, float64(time.Since(start).Milliseconds()))
		return nil, err
	}

	tools, err := a.resolveTools(ctx)
	if err != nil {
		sp.RecordError(err)
		a.metrics.IncrementCounter(ctx, types.MetricRunFailed, interfaces.Attribute{Key: "error", Value: "tools_list_failed"})
		a.metrics.RecordHistogram(ctx, types.MetricRunDurationMs, float64(time.Since(start).Milliseconds()))
		return nil, err
	}

	if a.hasApprovalTools(tools) && a.approvalHandler == nil {
		err := fmt.Errorf("tools require approval but WithApprovalHandler was not set (required for Run)")
		sp.RecordError(err)
		a.metrics.IncrementCounter(ctx, types.MetricRunFailed, interfaces.Attribute{Key: "error", Value: "missing_approval_handler"})
		a.metrics.RecordHistogram(ctx, types.MetricRunDurationMs, float64(time.Since(start).Milliseconds()))
		return nil, err
	}

	subAgents, err := a.resolveSubAgentSpecs(ctx)
	if err != nil {
		sp.RecordError(err)
		a.metrics.IncrementCounter(ctx, types.MetricRunFailed, interfaces.Attribute{Key: "error", Value: "build_sub_agent_specs_failed"})
		a.metrics.RecordHistogram(ctx, types.MetricRunDurationMs, float64(time.Since(start).Milliseconds()))
		return nil, err
	}
	a.shareEventBusWithSubAgents()

	req := a.executeRequest(input, opts, false, tools, subAgents)

	result, err := a.runtime.Execute(ctx, req)
	if err != nil {
		sp.RecordError(err)
		a.metrics.IncrementCounter(ctx, types.MetricRunFailed, interfaces.Attribute{Key: "error", Value: "runtime_execute_failed"})
		a.metrics.RecordHistogram(ctx, types.MetricRunDurationMs, float64(time.Since(start).Milliseconds()))
		return nil, err
	}
	a.metrics.RecordHistogram(ctx, types.MetricRunDurationMs, float64(time.Since(start).Milliseconds()))
	a.metrics.IncrementCounter(ctx, types.MetricRunCompleted)
	return result, nil
}

// RunAsync starts the run in a goroutine and returns a channel that receives exactly one
// [AgentRunAsyncResult], then closes. Use [WithApprovalHandler] when tools require approval
// (same as [Agent.Run]).
func (a *Agent) RunAsync(ctx context.Context, input string, opts *AgentRunOptions) (<-chan AgentRunAsyncResult, error) {
	a.logger.Debug(ctx, "agent run async started", slog.String("scope", "agent"), slog.String("name", a.Name), slog.Int("inputLen", len(input)))

	resCh := make(chan AgentRunAsyncResult, 1)
	go func() {
		defer close(resCh)
		resp, err := a.runInternal(ctx, input, opts, true)
		if err != nil {
			resCh <- AgentRunAsyncResult{Error: err}
			return
		}
		resCh <- AgentRunAsyncResult{Result: resp}
	}()
	return resCh, nil
}

func copyApprovalArgs(src map[string]any) map[string]any {
	if src == nil {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

// Stream starts the run and returns a channel of [AgentEvent]. Streaming continues until the root run’s
// terminal lifecycle event ([AgentEventTypeRunFinished] / [*AgentRunFinishedEvent]); sub-agent runs may emit
// additional [AgentEventTypeRunFinished] events that are delivered but do not close the root stream (see doc on [BaseEvent]).
// After the root completes, the channel may stay open briefly while the backend finishes cleanup, then closes.
// For approvals (tool or delegation), receive [AgentEventTypeCustom] ([AgentCustomEvent]), parse with
// [ParseCustomEventApproval] / [ParseCustomEventDelegation], then call [Agent.OnApproval] with the token from Value.
// When using [WithConversation], pass the conversation ID.
func (a *Agent) Stream(ctx context.Context, input string, opts *AgentRunOptions) (<-chan events.AgentEvent, error) {
	a.logger.Debug(ctx, "agent run stream started", slog.String("scope", "agent"), slog.String("name", a.Name), slog.Int("inputLen", len(input)))

	ctx = a.attachMemoryScopeContext(ctx)
	conversationID := conversationIDFromOpts(opts)

	start := time.Now()
	ctx, sp := a.tracer.StartSpan(ctx, "agent.stream",
		interfaces.Attribute{Key: "agent.name", Value: a.Name},
		interfaces.Attribute{Key: "conversation.id", Value: conversationID},
		interfaces.Attribute{Key: "input.length", Value: len(input)},
	)
	defer sp.End()
	a.metrics.IncrementCounter(ctx, types.MetricStreamStarted)

	if err := a.validateConversationID(conversationID); err != nil {
		sp.RecordError(err)
		a.metrics.IncrementCounter(ctx, types.MetricStreamFailed, interfaces.Attribute{Key: "error", Value: "conversation_id_invalid"})
		a.metrics.RecordHistogram(ctx, types.MetricStreamDurationMs, float64(time.Since(start).Milliseconds()))
		return nil, err
	}

	tools, err := a.resolveTools(ctx)
	if err != nil {
		sp.RecordError(err)
		a.metrics.IncrementCounter(ctx, types.MetricStreamFailed, interfaces.Attribute{Key: "error", Value: "tools_list_failed"})
		a.metrics.RecordHistogram(ctx, types.MetricStreamDurationMs, float64(time.Since(start).Milliseconds()))
		return nil, err
	}
	subAgents, err := a.resolveSubAgentSpecs(ctx)
	if err != nil {
		sp.RecordError(err)
		a.metrics.IncrementCounter(ctx, types.MetricStreamFailed, interfaces.Attribute{Key: "error", Value: "build_sub_agent_specs_failed"})
		a.metrics.RecordHistogram(ctx, types.MetricStreamDurationMs, float64(time.Since(start).Milliseconds()))
		return nil, err
	}
	a.shareEventBusWithSubAgents()

	req := a.executeRequest(input, opts, true, tools, subAgents)

	streamCh, err := a.runtime.ExecuteStream(ctx, req)
	if err != nil {
		sp.RecordError(err)
		a.metrics.IncrementCounter(ctx, types.MetricStreamFailed, interfaces.Attribute{Key: "error", Value: "runtime_execute_stream_failed"})
		a.metrics.RecordHistogram(ctx, types.MetricStreamDurationMs, float64(time.Since(start).Milliseconds()))
		return nil, err
	}
	a.metrics.RecordHistogram(ctx, types.MetricStreamDurationMs, float64(time.Since(start).Milliseconds()))
	a.metrics.IncrementCounter(ctx, types.MetricStreamDispatched)
	return streamCh, nil
}

func (a *Agent) attachMemoryScopeContext(ctx context.Context) context.Context {
	if a.Name != "" {
		ctx = memory.WithContextAgentID(ctx, a.Name)
	}
	return ctx
}

func conversationIDFromOpts(opts *AgentRunOptions) string {
	if opts != nil && opts.ConversationOptions != nil {
		return opts.ConversationOptions.ID
	}
	return ""
}

func (a *Agent) validateConversationID(conversationID string) error {
	if conversationID != "" && a.conversationConfig == nil {
		return fmt.Errorf("conversationID %s requires conversation configuration", conversationID)
	}
	if conversationID == "" && a.conversationConfig != nil {
		return fmt.Errorf("conversationID is required when using conversation")
	}
	return nil
}

// executeRequest builds [runtime.ExecuteRequest] with per-run fields for Run, Stream, and RunAsync.
func (a *Agent) executeRequest(userPrompt string, opts *AgentRunOptions, streaming bool, tools []interfaces.Tool, subAgents []*runtime.SubAgentSpec) *runtime.ExecuteRequest {
	return &runtime.ExecuteRequest{
		UserPrompt:       userPrompt,
		RunOptions:       opts,
		StreamingEnabled: streaming,
		SubAgents:        subAgents,
		MaxSubAgentDepth: a.maxSubAgentDepth,
		ApprovalHandler:  a.approvalHandler,
		Tools:            tools,
	}
}

// Sub-agents share the parent's in-memory pub/sub when the runtime implements [runtime.EventBusRuntime]
// (e.g. Temporal). A custom [runtime.Runtime] from a future WithRuntime need only implement [Runtime];
// wiring is skipped when the assert to EventBusRuntime fails.
func (a *Agent) shareEventBusWithSubAgents() {
	if a == nil {
		return
	}
	ir, ok := a.runtime.(runtime.EventBusRuntime)
	if !ok || a.subAgentRegistry == nil {
		return
	}
	bus := ir.GetEventBus()
	for _, sub := range a.subAgentRegistry.List() {
		if sub != nil {
			shareEventBusWithSubAgent(bus, sub)
		}
	}
}

func shareEventBusWithSubAgent(bus eventbus.EventBus, agent *Agent) {
	if agent == nil || bus == nil {
		return
	}
	if ir, ok := agent.runtime.(runtime.EventBusRuntime); ok {
		ir.SetEventBus(bus)
	}
	if agent.localAgentWorker != nil {
		if ir, ok := agent.localAgentWorker.runtime.(runtime.EventBusRuntime); ok {
			ir.SetEventBus(bus)
		}
	}
	if agent.subAgentRegistry == nil {
		return
	}
	for _, child := range agent.subAgentRegistry.List() {
		shareEventBusWithSubAgent(bus, child)
	}
}

// resolveSubAgentSpecs builds the runtime-agnostic sub-agent spec tree for this agent.
// Each runtime receives this tree via ExecuteRequest.SubAgents and constructs its own
// internal routing structures (local: *LocalRuntime refs; temporal: task queue + fingerprint).
func (a *Agent) resolveSubAgentSpecs(ctx context.Context) ([]*runtime.SubAgentSpec, error) {
	if a == nil || a.subAgentRegistry == nil {
		return nil, nil
	}
	subs := a.subAgentRegistry.List()
	if len(subs) == 0 {
		return nil, nil
	}
	out := make([]*runtime.SubAgentSpec, 0, len(subs))
	for _, sub := range subs {
		if sub == nil {
			continue
		}
		toolName, err := subAgentToolName(sub.Name)
		if err != nil || toolName == "" {
			continue
		}
		tools, err := sub.resolveTools(ctx)
		if err != nil {
			return nil, err
		}
		children, err := sub.resolveSubAgentSpecs(ctx)
		if err != nil {
			return nil, err
		}
		out = append(out, &runtime.SubAgentSpec{
			Name:     sub.Name,
			ToolName: toolName,
			Runtime:  sub.runtime,
			Children: children,
			Tools:    tools,
		})
	}
	if len(out) == 0 {
		return nil, nil
	}
	if a.logger != nil {
		names := make([]string, 0, len(out))
		for _, s := range out {
			names = append(names, s.ToolName)
		}
		sort.Strings(names)
		a.logger.Debug(context.Background(), "built sub-agent specs for runtime delegation",
			slog.String("scope", "agent"),
			slog.Any("subAgentToolNames", names),
			slog.Int("specCount", len(out)))
	}
	return out, nil
}

// ToolRegistry returns the agent's tool registry.
func (a *Agent) ToolRegistry() ToolRegistry {
	if a == nil {
		return nil
	}
	return a.toolRegistry
}

// MCPRegistry returns the agent's MCP client registry.
func (a *Agent) MCPRegistry() MCPRegistry {
	if a == nil {
		return nil
	}
	return a.mcpRegistry
}

// A2ARegistry returns the agent's A2A client registry.
func (a *Agent) A2ARegistry() A2ARegistry {
	if a == nil {
		return nil
	}
	return a.a2aRegistry
}

// SubAgentRegistry returns the agent's sub-agent registry.
func (a *Agent) SubAgentRegistry() SubAgentRegistry {
	if a == nil {
		return nil
	}
	return a.subAgentRegistry
}
