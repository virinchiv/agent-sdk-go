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

// AgentRunResult is the structured result of [Agent.Run] and [Agent.RunAsync] ([RunAsyncResult.Result]).
type AgentRunResult = types.AgentRunResult

// AgentRunAsyncResult is the single outcome from [Agent.RunAsync]. After the channel closes, Err is non-nil
// on failure; otherwise Result is non-nil.
type AgentRunAsyncResult = types.AgentRunAsyncResult

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

	// Sub-agents share the parent's in-memory pub/sub when the runtime implements [runtime.EventBusRuntime]
	// (e.g. Temporal). A custom [runtime.Runtime] from a future WithRuntime need only implement [Runtime];
	// wiring is skipped when the assert to EventBusRuntime fails.
	if ir, ok := a.runtime.(runtime.EventBusRuntime); ok {
		bus := ir.GetEventBus()
		for _, sub := range a.subAgents {
			if sub != nil {
				wireInMemoryEventChannelToSubAgents(bus, sub)
			}
		}
	}

	return a, nil
}

func wireInMemoryEventChannelToSubAgents(bus eventbus.EventBus, agent *Agent) {
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
	for _, child := range agent.subAgents {
		wireInMemoryEventChannelToSubAgents(bus, child)
	}
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
func (a *Agent) Run(ctx context.Context, input string, conversationID string) (*AgentRunResult, error) {
	a.logger.Debug(ctx, "agent run started", slog.String("scope", "agent"), slog.String("name", a.Name), slog.Int("inputLen", len(input)))

	start := time.Now()
	ctx, sp := a.tracer.StartSpan(ctx, "agent.run",
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

	if a.hasApprovalTools() && a.approvalHandler == nil {
		err := fmt.Errorf("tools require approval but WithApprovalHandler was not set (required for Run)")
		sp.RecordError(err)
		a.metrics.IncrementCounter(ctx, types.MetricRunFailed, interfaces.Attribute{Key: "error", Value: "missing_approval_handler"})
		a.metrics.RecordHistogram(ctx, types.MetricRunDurationMs, float64(time.Since(start).Milliseconds()))
		return nil, err
	}

	req := a.executeRequest(input, conversationID, false)

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

// RunAsync starts the run in a goroutine and returns two channels:
//   - resultCh: receives exactly one RunAsyncResult, then closes.
//   - approvalCh: receives each pending tool approval; call req.Respond. Channel closes when the run ends.
//
// For each approval, call req.Respond(Approved|Rejected) exactly once.
//
// WithApprovalHandler is temporarily replaced for the duration of the run; restore happens when the run finishes.
// If tools do not require approval, approvalCh is still closed immediately with no values.
func (a *Agent) RunAsync(ctx context.Context, input string, conversationID string) (resultCh <-chan AgentRunAsyncResult, approvalCh <-chan *ApprovalRequest, err error) {
	a.logger.Debug(ctx, "agent run async started", slog.String("scope", "agent"), slog.String("name", a.Name), slog.Int("inputLen", len(input)))

	if err := a.validateConversationID(conversationID); err != nil {
		return nil, nil, err
	}

	resCh := make(chan AgentRunAsyncResult, 1)
	apprCh := make(chan *ApprovalRequest, 16)

	go func() {
		defer close(apprCh)
		defer close(resCh)

		var saved ApprovalHandler
		if a.hasApprovalTools() {
			saved = a.approvalHandler
			a.approvalHandler = func(handlerCtx context.Context, req *ApprovalRequest) {
				out := &ApprovalRequest{
					Name:    req.Name,
					Value:   req.Value,
					Respond: req.Respond,
				}
				select {
				case apprCh <- out:
				default:
					// Avoid blocking Run's event loop if consumer is slow.
					go func(p *ApprovalRequest) { apprCh <- p }(out)
				}
			}
			defer func() { a.approvalHandler = saved }()
		}

		resp, runErr := a.Run(ctx, input, conversationID)
		if runErr != nil {
			resCh <- AgentRunAsyncResult{Error: runErr}
			return
		}
		resCh <- AgentRunAsyncResult{Result: resp}
	}()

	return resCh, apprCh, nil
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
func (a *Agent) Stream(ctx context.Context, input string, conversationID string) (<-chan events.AgentEvent, error) {
	a.logger.Debug(ctx, "agent run stream started", slog.String("scope", "agent"), slog.String("name", a.Name), slog.Int("inputLen", len(input)))

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

	req := a.executeRequest(input, conversationID, true)

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

func (a *Agent) validateConversationID(conversationID string) error {
	if conversationID != "" && a.conversation == nil {
		return fmt.Errorf("conversationID %s requires conversation configuration", conversationID)
	}
	if conversationID == "" && a.conversation != nil {
		return fmt.Errorf("conversationID is required when using conversation")
	}
	return nil
}

// executeRequest builds [runtime.ExecuteRequest] with per-run fields plus AgentSpec and AgentExecution for custom Runtime implementations.
func (a *Agent) executeRequest(userPrompt, conversationID string, streaming bool) *runtime.ExecuteRequest {
	return &runtime.ExecuteRequest{
		UserPrompt:       userPrompt,
		ConversationID:   conversationID,
		StreamingEnabled: streaming,
		SubAgents:        a.buildSubAgentSpecs(),
		MaxSubAgentDepth: a.maxSubAgentDepth,
		ApprovalHandler:  a.approvalHandler,
		AgentSpec:        a.agentSpec(),
		AgentExecution:   a.agentExecution(),
	}
}

func (a *Agent) agentSpec() *runtime.AgentSpec {
	s := a.runtimeAgentSpec()
	return &s
}

func (a *Agent) agentExecution() *runtime.AgentExecution {
	e := a.runtimeAgentExecution()
	return &e
}

// buildSubAgentSpecs builds the runtime-agnostic sub-agent spec tree for this agent.
// Each runtime receives this tree via ExecuteRequest.SubAgents and constructs its own
// internal routing structures (local: *LocalRuntime refs; temporal: task queue + fingerprint).
func (a *Agent) buildSubAgentSpecs() []*runtime.SubAgentSpec {
	if a == nil || len(a.subAgents) == 0 {
		return nil
	}
	out := make([]*runtime.SubAgentSpec, 0, len(a.subAgents))
	for _, sub := range a.subAgents {
		if sub == nil {
			continue
		}
		toolName, err := subAgentToolName(sub.Name)
		if err != nil || toolName == "" {
			continue
		}
		out = append(out, &runtime.SubAgentSpec{
			Name:     sub.Name,
			ToolName: toolName,
			Runtime:  sub.runtime,
			Children: sub.buildSubAgentSpecs(),
		})
	}
	if len(out) == 0 {
		return nil
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
	return out
}
