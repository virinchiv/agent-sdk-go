package local

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/agenticenv/agent-sdk-go/internal/events"
	sdkruntime "github.com/agenticenv/agent-sdk-go/internal/runtime"
	"github.com/agenticenv/agent-sdk-go/internal/runtime/base"
	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	ifmocks "github.com/agenticenv/agent-sdk-go/pkg/interfaces/mocks"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
	"github.com/agenticenv/agent-sdk-go/pkg/observability"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Shared test stubs
// ---------------------------------------------------------------------------

// seqLLMClient returns LLM responses from a pre-loaded sequence.
// Once the sequence is exhausted it returns a plain "done" response.
type seqLLMClient struct {
	mu        sync.Mutex
	responses []*interfaces.LLMResponse
	errs      []error
	call      int
}

func (s *seqLLMClient) Generate(_ context.Context, _ *interfaces.LLMRequest) (*interfaces.LLMResponse, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	i := s.call
	s.call++
	if i < len(s.errs) && s.errs[i] != nil {
		return nil, s.errs[i]
	}
	if i < len(s.responses) {
		return s.responses[i], nil
	}
	return &interfaces.LLMResponse{Content: "done"}, nil
}
func (s *seqLLMClient) GenerateStream(_ context.Context, _ *interfaces.LLMRequest) (interfaces.LLMStream, error) {
	return nil, errors.New("stream not implemented in seqLLMClient")
}
func (s *seqLLMClient) GetModel() string                    { return "test-model" }
func (s *seqLLMClient) GetProvider() interfaces.LLMProvider { return interfaces.LLMProviderOpenAI }
func (s *seqLLMClient) IsStreamSupported() bool             { return false }

// stubTool is a minimal Tool with configurable execute result and optional approval.
type stubTool struct {
	name          string
	result        string
	execErr       error
	needsApproval bool
}

func (t stubTool) Name() string                      { return t.name }
func (t stubTool) DisplayName() string               { return t.name }
func (t stubTool) Description() string               { return "" }
func (t stubTool) Parameters() interfaces.JSONSchema { return nil }
func (t stubTool) Execute(_ context.Context, _ map[string]any) (any, error) {
	return t.result, t.execErr
}
func (t stubTool) ApprovalRequired() bool { return t.needsApproval }

// newLocalRT constructs a LocalRuntime suitable for tests.
func newLocalRT(t *testing.T, client interfaces.LLMClient, tools ...interfaces.Tool) *LocalRuntime {
	t.Helper()
	rt, err := NewLocalRuntime(
		WithLogger(logger.NoopLogger()),
		WithAgentSpec(sdkruntime.AgentSpec{Name: "test-agent", SystemPrompt: "you are helpful"}),
		WithAgentConfig(sdkruntime.AgentConfig{
			LLM: sdkruntime.AgentLLM{Client: client},
			Limits: sdkruntime.AgentLimits{
				MaxIterations: 5,
				Timeout:       30 * time.Second,
			},
		}),
	)
	require.NoError(t, err)
	_ = tools // callers pass resolved tools on ExecuteRequest.Tools
	return rt
}

func execReq(prompt string, tools ...interfaces.Tool) *sdkruntime.ExecuteRequest {
	return &sdkruntime.ExecuteRequest{UserPrompt: prompt, Tools: tools}
}

// collectEvents drains an event channel until it is closed or timeout elapses,
// returning all events received.
func collectEvents(t *testing.T, ch <-chan events.AgentEvent, timeout time.Duration) []events.AgentEvent {
	t.Helper()
	var collected []events.AgentEvent
	deadline := time.After(timeout)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return collected
			}
			if ev != nil {
				collected = append(collected, ev)
			}
		case <-deadline:
			t.Fatalf("collectEvents: timed out after %s waiting for channel to close", timeout)
			return collected
		}
	}
}

// eventTypes extracts the AgentEventType from each collected event.
func eventTypes(evs []events.AgentEvent) []events.AgentEventType {
	out := make([]events.AgentEventType, len(evs))
	for i, ev := range evs {
		out[i] = ev.Type()
	}
	return out
}

// ---------------------------------------------------------------------------
// NewLocalRuntime
// ---------------------------------------------------------------------------

func TestNewLocalRuntime_MissingLLMClient(t *testing.T) {
	_, err := NewLocalRuntime(
		WithAgentSpec(sdkruntime.AgentSpec{Name: "agent"}),
		WithAgentConfig(sdkruntime.AgentConfig{}),
	)
	require.Error(t, err)
	require.Contains(t, err.Error(), "llm client is required")
}

func TestNewLocalRuntime_DefaultNoopObservability(t *testing.T) {
	rt, err := NewLocalRuntime(
		WithAgentConfig(sdkruntime.AgentConfig{
			LLM: sdkruntime.AgentLLM{Client: &seqLLMClient{}},
		}),
	)
	require.NoError(t, err)
	require.Equal(t, observability.DefaultNoopTracer, rt.Tracer)
	require.Equal(t, observability.DefaultNoopMetrics, rt.Metrics)
}

func TestNewLocalRuntime_WithAllOptions(t *testing.T) {
	ctrl := gomock.NewController(t)
	tracer := ifmocks.NewMockTracer(ctrl)
	metrics := ifmocks.NewMockMetrics(ctrl)

	rt, err := NewLocalRuntime(
		WithLogger(logger.NoopLogger()),
		WithAgentSpec(sdkruntime.AgentSpec{Name: "my-agent"}),
		WithAgentConfig(sdkruntime.AgentConfig{
			LLM: sdkruntime.AgentLLM{Client: &seqLLMClient{}},
		}),
		WithTracer(tracer),
		WithMetrics(metrics),
		WithToolExecutionMode(types.AgentToolExecutionModeSequential),
	)
	require.NoError(t, err)
	require.Equal(t, "my-agent", rt.AgentSpec.Name)
	require.Equal(t, tracer, rt.Tracer)
	require.Equal(t, metrics, rt.Metrics)
	require.Equal(t, types.AgentToolExecutionModeSequential, rt.ToolExecutionMode)
}

func TestNewLocalRuntime_EventBusInitialised(t *testing.T) {
	rt := newLocalRT(t, &seqLLMClient{})
	require.NotNil(t, rt.eventbus, "eventbus should be initialised by NewLocalRuntime")
}

// ---------------------------------------------------------------------------
// agentNameFromRuntime
// ---------------------------------------------------------------------------

func TestAgentNameFromRuntime_NilRuntime(t *testing.T) {
	require.Equal(t, "", agentNameFromRuntime(nil))
}

func TestAgentNameFromRuntime_WithName(t *testing.T) {
	rt := newLocalRT(t, &seqLLMClient{})
	require.Equal(t, "test-agent", agentNameFromRuntime(rt))
}

// ---------------------------------------------------------------------------
// Execute
// ---------------------------------------------------------------------------

func TestExecute_SimpleTextResponse(t *testing.T) {
	client := &seqLLMClient{
		responses: []*interfaces.LLMResponse{
			{Content: "Hello from the agent"},
		},
	}
	rt := newLocalRT(t, client)

	result, err := rt.Execute(context.Background(), &sdkruntime.ExecuteRequest{
		UserPrompt: "hi",
	})

	require.NoError(t, err)
	require.Equal(t, "Hello from the agent", result.Content)
	require.Equal(t, "test-agent", result.AgentName)
	require.Equal(t, "test-model", result.Model)
}

func TestExecute_PropagatesLLMError(t *testing.T) {
	client := &seqLLMClient{
		errs: []error{errors.New("llm unavailable")},
	}
	rt := newLocalRT(t, client)

	_, err := rt.Execute(context.Background(), &sdkruntime.ExecuteRequest{UserPrompt: "hi"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "llm unavailable")
}

func TestExecute_AppliesTimeoutWhenNoDeadline(t *testing.T) {
	// Build a runtime with a very short timeout.
	blocking := &blockingLLMClient{block: make(chan struct{})}
	rt, err := NewLocalRuntime(
		WithLogger(logger.NoopLogger()),
		WithAgentConfig(sdkruntime.AgentConfig{
			LLM: sdkruntime.AgentLLM{Client: blocking},
			Limits: sdkruntime.AgentLimits{
				MaxIterations: 1,
				Timeout:       50 * time.Millisecond,
			},
		}),
	)
	require.NoError(t, err)

	// Pass context.Background() — no deadline — so Execute applies the runtime timeout.
	start := time.Now()
	_, err = rt.Execute(context.Background(), &sdkruntime.ExecuteRequest{UserPrompt: "hi"})
	elapsed := time.Since(start)

	// Should have been cancelled by the 50ms runtime timeout.
	require.Error(t, err)
	assert.Less(t, elapsed, 2*time.Second, "runtime timeout should fire well before 2s")
}

// blockingLLMClient blocks until its context is cancelled.
type blockingLLMClient struct {
	block chan struct{}
}

func (b *blockingLLMClient) Generate(ctx context.Context, _ *interfaces.LLMRequest) (*interfaces.LLMResponse, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}
func (b *blockingLLMClient) GenerateStream(_ context.Context, _ *interfaces.LLMRequest) (interfaces.LLMStream, error) {
	return nil, errors.New("not supported")
}
func (b *blockingLLMClient) GetModel() string                    { return "blocking" }
func (b *blockingLLMClient) GetProvider() interfaces.LLMProvider { return interfaces.LLMProviderOpenAI }
func (b *blockingLLMClient) IsStreamSupported() bool             { return false }

func TestExecute_WithApprovalHandler(t *testing.T) {
	client := &seqLLMClient{
		responses: []*interfaces.LLMResponse{
			{
				ToolCalls: []*interfaces.ToolCall{
					{ToolCallID: "c1", ToolName: "approve-tool"},
				},
			},
			{Content: "tool done"},
		},
	}
	tool := stubTool{name: "approve-tool", result: "executed", needsApproval: true}
	rt := newLocalRT(t, client, tool)

	handlerCalled := false
	handler := func(_ context.Context, req *types.ApprovalRequest) {
		handlerCalled = true
		_ = req.Respond(types.ApprovalStatusApproved)
	}

	result, err := rt.Execute(context.Background(), &sdkruntime.ExecuteRequest{
		UserPrompt:      "run tool",
		Tools:           []interfaces.Tool{tool},
		ApprovalHandler: handler,
	})

	require.NoError(t, err)
	require.True(t, handlerCalled, "approval handler must be called")
	require.Equal(t, "tool done", result.Content)
}

// ---------------------------------------------------------------------------
// ExecuteStream
// ---------------------------------------------------------------------------

func TestExecuteStream_EmitsRunStartedAndFinished(t *testing.T) {
	client := &seqLLMClient{
		responses: []*interfaces.LLMResponse{{Content: "stream answer"}},
	}
	rt := newLocalRT(t, client)

	ch, err := rt.ExecuteStream(context.Background(), &sdkruntime.ExecuteRequest{
		UserPrompt: "hello",
	})
	require.NoError(t, err)

	evs := collectEvents(t, ch, 5*time.Second)
	types := eventTypes(evs)

	require.Contains(t, types, events.AgentEventTypeRunStarted)
	require.Contains(t, types, events.AgentEventTypeRunFinished)

	// RUN_STARTED must come first, RUN_FINISHED last.
	first := types[0]
	last := types[len(types)-1]
	require.Equal(t, events.AgentEventTypeRunStarted, first)
	require.Equal(t, events.AgentEventTypeRunFinished, last)
}

func TestExecuteStream_EmitsRunError(t *testing.T) {
	client := &seqLLMClient{
		errs: []error{errors.New("llm down")},
	}
	rt := newLocalRT(t, client)

	ch, err := rt.ExecuteStream(context.Background(), &sdkruntime.ExecuteRequest{
		UserPrompt: "hi",
	})
	require.NoError(t, err) // subscribe succeeds synchronously

	evs := collectEvents(t, ch, 5*time.Second)
	types := eventTypes(evs)

	require.Contains(t, types, events.AgentEventTypeRunStarted)
	require.Contains(t, types, events.AgentEventTypeRunError)
}

func TestExecuteStream_ChannelClosedAfterTerminalEvent(t *testing.T) {
	client := &seqLLMClient{
		responses: []*interfaces.LLMResponse{{Content: "done"}},
	}
	rt := newLocalRT(t, client)

	ch, err := rt.ExecuteStream(context.Background(), &sdkruntime.ExecuteRequest{UserPrompt: "hi"})
	require.NoError(t, err)

	// Channel must close eventually.
	timeout := time.After(5 * time.Second)
	for {
		select {
		case _, ok := <-ch:
			if !ok {
				return // channel closed — success
			}
		case <-timeout:
			t.Fatal("channel never closed")
		}
	}
}

func TestExecuteStream_ContextCancelledAborts(t *testing.T) {
	blocking := &blockingLLMClient{block: make(chan struct{})}
	rt := newLocalRT(t, blocking)

	ctx, cancel := context.WithCancel(context.Background())

	ch, err := rt.ExecuteStream(ctx, &sdkruntime.ExecuteRequest{UserPrompt: "hi"})
	require.NoError(t, err)

	// Give the goroutine a moment to start, then cancel.
	time.Sleep(20 * time.Millisecond)
	cancel()

	evs := collectEvents(t, ch, 3*time.Second)
	types := eventTypes(evs)
	require.Contains(t, types, events.AgentEventTypeRunStarted)
	// Channel must close (error or finished).
	// Verifying closure is enough; collectEvents blocks until close.
	_ = types
}

// ---------------------------------------------------------------------------
// Approve
// ---------------------------------------------------------------------------

func TestApprove_UnknownToken(t *testing.T) {
	rt := newLocalRT(t, &seqLLMClient{})
	err := rt.Approve(context.Background(), "nonexistent-token", types.ApprovalStatusApproved)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no pending approval for token")
}

func TestApprove_ResolvesRegisteredChannel(t *testing.T) {
	rt := newLocalRT(t, &seqLLMClient{})

	const token = "test-token-123"
	resultCh := make(chan types.ApprovalStatus, 1)
	rt.pendingApprovals.Store(token, resultCh)

	err := rt.Approve(context.Background(), token, types.ApprovalStatusApproved)
	require.NoError(t, err)

	select {
	case status := <-resultCh:
		require.Equal(t, types.ApprovalStatusApproved, status)
	case <-time.After(time.Second):
		t.Fatal("expected status on channel, got timeout")
	}

	// Token should have been removed by LoadAndDelete.
	_, loaded := rt.pendingApprovals.Load(token)
	require.False(t, loaded, "token must be removed after Approve")
}

func TestApprove_RejectsViaSameToken(t *testing.T) {
	rt := newLocalRT(t, &seqLLMClient{})

	const token = "reject-token"
	resultCh := make(chan types.ApprovalStatus, 1)
	rt.pendingApprovals.Store(token, resultCh)

	err := rt.Approve(context.Background(), token, types.ApprovalStatusRejected)
	require.NoError(t, err)

	status := <-resultCh
	require.Equal(t, types.ApprovalStatusRejected, status)
}

func TestApprove_DoubleApproveSecondErrors(t *testing.T) {
	rt := newLocalRT(t, &seqLLMClient{})

	const token = "double-token"
	resultCh := make(chan types.ApprovalStatus, 1)
	rt.pendingApprovals.Store(token, resultCh)

	require.NoError(t, rt.Approve(context.Background(), token, types.ApprovalStatusApproved))
	// Second call: token already removed by LoadAndDelete.
	err := rt.Approve(context.Background(), token, types.ApprovalStatusApproved)
	require.Error(t, err)
}

func TestApprove_StreamingEndToEnd(t *testing.T) {
	// LLM: first call returns a tool call needing approval, second returns final text.
	client := &seqLLMClient{
		responses: []*interfaces.LLMResponse{
			{ToolCalls: []*interfaces.ToolCall{
				{ToolCallID: "c1", ToolName: "guarded-tool"},
			}},
			{Content: "approved result"},
		},
	}
	tool := stubTool{name: "guarded-tool", result: "ran!", needsApproval: true}
	rt := newLocalRT(t, client, tool)

	ch, err := rt.ExecuteStream(context.Background(), &sdkruntime.ExecuteRequest{
		UserPrompt: "run guarded tool",
		Tools:      []interfaces.Tool{tool},
	})
	require.NoError(t, err)

	// Collect events until we see a CUSTOM approval event, then approve.
	var approvalToken string
	var allEvents []events.AgentEvent

	timeout := time.After(5 * time.Second)
outer:
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				break outer
			}
			if ev == nil {
				continue
			}
			allEvents = append(allEvents, ev)
			if ev.Type() == events.AgentEventTypeCustom {
				val, parseErr := events.ParseCustomEventApproval(ev.(*events.AgentCustomEvent))
				if parseErr == nil && val.ApprovalToken != "" {
					approvalToken = val.ApprovalToken
					// Approve in a separate goroutine to unblock the loop.
					go func(tok string) {
						_ = rt.Approve(context.Background(), tok, types.ApprovalStatusApproved)
					}(approvalToken)
				}
			}
		case <-timeout:
			t.Fatal("timed out waiting for streaming events")
		}
	}

	types := eventTypes(allEvents)
	require.NotEmpty(t, approvalToken, "expected an approval token in CUSTOM event")
	require.Contains(t, types, events.AgentEventTypeRunFinished)
}

// ---------------------------------------------------------------------------
// Close
// ---------------------------------------------------------------------------

func TestClose_NoError(t *testing.T) {
	rt := newLocalRT(t, &seqLLMClient{})
	require.NotPanics(t, rt.Close)
}

// ---------------------------------------------------------------------------
// publishLifecycleEvent
// ---------------------------------------------------------------------------

func TestPublishLifecycleEvent_NilEventbus(t *testing.T) {
	rt := &LocalRuntime{
		Runtime: base.Runtime{
			AgentSpec: sdkruntime.AgentSpec{Name: "a"},
		},
		logger: logger.NoopLogger(),
	}
	// eventbus is nil — must not panic.
	require.NotPanics(t, func() {
		rt.publishLifecycleEvent("some-channel", events.NewAgentRunErrorEvent("oops"))
	})
}

func TestPublishLifecycleEvent_EmptyChannel(t *testing.T) {
	rt := newLocalRT(t, &seqLLMClient{})
	// empty channel — must not panic.
	require.NotPanics(t, func() {
		rt.publishLifecycleEvent("", events.NewAgentRunErrorEvent("oops"))
	})
}

func TestPublishLifecycleEvent_NilEvent(t *testing.T) {
	rt := newLocalRT(t, &seqLLMClient{})
	require.NotPanics(t, func() {
		rt.publishLifecycleEvent("ch", nil)
	})
}

// ---------------------------------------------------------------------------
// EventBusRuntime interface
// ---------------------------------------------------------------------------

func TestGetEventBus_ReturnsInitialisedBus(t *testing.T) {
	rt := newLocalRT(t, &seqLLMClient{})
	require.NotNil(t, rt.GetEventBus(), "GetEventBus must return the bus initialised by NewLocalRuntime")
}

func TestSetEventBus_ReplacesBus(t *testing.T) {
	rt := newLocalRT(t, &seqLLMClient{})
	original := rt.GetEventBus()

	// Build a second runtime and swap its bus into the first.
	rt2 := newLocalRT(t, &seqLLMClient{})
	newBus := rt2.GetEventBus()

	rt.SetEventBus(newBus)
	require.Same(t, newBus, rt.GetEventBus(), "GetEventBus should return the new bus")
	require.NotSame(t, original, rt.GetEventBus(), "bus should have changed after SetEventBus")
}

// ---------------------------------------------------------------------------
// localChannelName
// ---------------------------------------------------------------------------

func TestLocalChannelName(t *testing.T) {
	name := localChannelName("run-42")
	require.Equal(t, "agent-event-run-42", name)
}

// ---------------------------------------------------------------------------
// subscribeToAgentEvents
// ---------------------------------------------------------------------------

func TestSubscribeToAgentEvents_DecodesEvents(t *testing.T) {
	rt := newLocalRT(t, &seqLLMClient{})
	ctx := context.Background()
	ch, closeFn, err := rt.subscribeToAgentEvents(ctx, "test-channel")
	require.NoError(t, err)
	defer func() { _ = closeFn() }()

	// Publish a raw lifecycle event.
	ev := events.NewAgentRunStartedEvent("thread-1", "run-1")
	rt.publishLifecycleEvent("test-channel", ev)

	select {
	case received := <-ch:
		require.Equal(t, events.AgentEventTypeRunStarted, received.Type())
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for event")
	}
}

// ---------------------------------------------------------------------------
// Execute with tool call (two-turn)
// ---------------------------------------------------------------------------

func TestExecute_ToolCallThenFinalAnswer(t *testing.T) {
	client := &seqLLMClient{
		responses: []*interfaces.LLMResponse{
			{
				ToolCalls: []*interfaces.ToolCall{
					{ToolCallID: "c1", ToolName: "calc"},
				},
			},
			{Content: "the answer is 42"},
		},
	}
	tool := stubTool{name: "calc", result: "42"}
	rt := newLocalRT(t, client, tool)

	result, err := rt.Execute(context.Background(), execReq("compute", tool))
	require.NoError(t, err)
	require.Equal(t, "the answer is 42", result.Content)
}

// ---------------------------------------------------------------------------
// Execute — conversation persistence
// ---------------------------------------------------------------------------

func TestExecute_PersistsConversationMessages(t *testing.T) {
	ctrl := gomock.NewController(t)
	conv := ifmocks.NewMockConversation(ctrl)

	// ListMessages returns empty history for "conv-1".
	conv.EXPECT().ListMessages(gomock.Any(), "conv-1", gomock.Any()).Return(nil, nil)
	// AddMessage is called for each message (user + assistant = 2).
	conv.EXPECT().AddMessage(gomock.Any(), "conv-1", gomock.Any()).Return(nil).Times(2)

	client := &seqLLMClient{
		responses: []*interfaces.LLMResponse{{Content: "persisted"}},
	}
	rt, err := NewLocalRuntime(
		WithLogger(logger.NoopLogger()),
		WithAgentSpec(sdkruntime.AgentSpec{Name: "agent"}),
		WithAgentConfig(sdkruntime.AgentConfig{
			LLM:     sdkruntime.AgentLLM{Client: client},
			Session: sdkruntime.AgentSession{Conversation: conv, ConversationSize: 20},
			Limits:  sdkruntime.AgentLimits{MaxIterations: 5, Timeout: 5 * time.Second},
		}),
	)
	require.NoError(t, err)

	_, err = rt.Execute(context.Background(), &sdkruntime.ExecuteRequest{
		UserPrompt: "remember this",
		RunOptions: &types.AgentRunOptions{
			ConversationOptions: &types.ConversationOptions{
				ID: "conv-1",
			},
		},
	})
	require.NoError(t, err)
}
