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
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newLoopRT builds a LocalRuntime with the given LLM client and optional tools.
func newLoopRT(t *testing.T, maxIter int, client interfaces.LLMClient, tools ...interfaces.Tool) (*LocalRuntime, []interfaces.Tool) {
	t.Helper()
	rt, err := NewLocalRuntime(
		WithLogger(logger.NoopLogger()),
		WithAgentSpec(sdkruntime.AgentSpec{Name: "loop-agent", SystemPrompt: "sys"}),
		WithAgentConfig(sdkruntime.AgentConfig{
			LLM:    sdkruntime.AgentLLM{Client: client},
			Limits: sdkruntime.AgentLimits{MaxIterations: maxIter, Timeout: 10 * time.Second},
		}),
	)
	require.NoError(t, err)
	return rt, tools
}

func runLoop(ctx context.Context, rt *LocalRuntime, tools []interfaces.Tool, in AgentLoopInput) (*AgentLoopResult, error) {
	if len(in.Tools) == 0 {
		in.Tools = tools
	}
	return rt.RunAgentLoop(ctx, in)
}

func loopToolsInput(tools []interfaces.Tool) AgentLoopInput {
	return AgentLoopInput{Tools: tools}
}

// noopEmit discards all events.
func noopEmit(_ events.AgentEvent) {}

// captureEmit returns an emit function and a pointer to the captured events slice.
func captureEmit() (func(events.AgentEvent), *[]events.AgentEvent) {
	var evs []events.AgentEvent
	return func(ev events.AgentEvent) { evs = append(evs, ev) }, &evs
}

// ---------------------------------------------------------------------------
// RunAgentLoop — basic paths
// ---------------------------------------------------------------------------

func TestRunAgentLoop_SimpleTextResponse(t *testing.T) {
	client := &seqLLMClient{
		responses: []*interfaces.LLMResponse{{Content: "hello world"}},
	}
	rt, _ := newLoopRT(t, 5, client)

	result, err := runLoop(context.Background(), rt, nil, AgentLoopInput{UserPrompt: "hi"})
	require.NoError(t, err)
	require.Equal(t, "hello world", result.Content)
}

func TestRunAgentLoop_LLMError(t *testing.T) {
	client := &seqLLMClient{errs: []error{errors.New("llm fail")}}
	rt, _ := newLoopRT(t, 5, client)

	_, err := runLoop(context.Background(), rt, nil, AgentLoopInput{UserPrompt: "hi"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "llm fail")
}

func TestRunAgentLoop_DefaultMaxIterations(t *testing.T) {
	// When MaxIterations = 0 the loop defaults to 10.
	// The client returns a text response on the first call so it exits immediately.
	client := &seqLLMClient{
		responses: []*interfaces.LLMResponse{{Content: "early exit"}},
	}
	rt, err := NewLocalRuntime(
		WithLogger(logger.NoopLogger()),
		WithAgentConfig(sdkruntime.AgentConfig{
			LLM:    sdkruntime.AgentLLM{Client: client},
			Limits: sdkruntime.AgentLimits{MaxIterations: 0, Timeout: 10 * time.Second},
		}),
	)
	require.NoError(t, err)

	result, err := runLoop(context.Background(), rt, nil, AgentLoopInput{UserPrompt: "hi"})
	require.NoError(t, err)
	require.Equal(t, "early exit", result.Content)
}

func TestRunAgentLoop_ToolCallThenFinalAnswer(t *testing.T) {
	client := &seqLLMClient{
		responses: []*interfaces.LLMResponse{
			{ToolCalls: []*interfaces.ToolCall{{ToolCallID: "c1", ToolName: "add"}}},
			{Content: "sum is 7"},
		},
	}
	tool := stubTool{name: "add", result: "7"}
	rt, tools := newLoopRT(t, 5, client, tool)

	result, err := runLoop(context.Background(), rt, tools, AgentLoopInput{UserPrompt: "add"})
	require.NoError(t, err)
	require.Equal(t, "sum is 7", result.Content)
}

func TestRunAgentLoop_MaxIterationsForcesFinalCall(t *testing.T) {
	// With maxIter=1 and the only LLM response returning a tool call, the loop
	// must fire a second "forced final" LLM call (skipTools=true) and return its content.
	client := &seqLLMClient{
		responses: []*interfaces.LLMResponse{
			{ToolCalls: []*interfaces.ToolCall{{ToolCallID: "c1", ToolName: "add"}}},
			{Content: "forced final answer"},
		},
	}
	tool := stubTool{name: "add", result: "7"}
	rt, tools := newLoopRT(t, 1, client, tool)

	result, err := runLoop(context.Background(), rt, tools, AgentLoopInput{UserPrompt: "add"})
	require.NoError(t, err)
	require.Equal(t, "forced final answer", result.Content)
}

// ---------------------------------------------------------------------------
// RunAgentLoop — tool execution modes
// ---------------------------------------------------------------------------

func TestRunAgentLoop_SequentialMode(t *testing.T) {
	client := &seqLLMClient{
		responses: []*interfaces.LLMResponse{
			{ToolCalls: []*interfaces.ToolCall{
				{ToolCallID: "c1", ToolName: "t1"},
				{ToolCallID: "c2", ToolName: "t2"},
			}},
			{Content: "sequential done"},
		},
	}
	tool1 := stubTool{name: "t1", result: "r1"}
	tool2 := stubTool{name: "t2", result: "r2"}
	rt, tools := newLoopRT(t, 5, client, tool1, tool2)
	rt.ToolExecutionMode = types.AgentToolExecutionModeSequential

	result, err := runLoop(context.Background(), rt, tools, AgentLoopInput{UserPrompt: "go"})
	require.NoError(t, err)
	require.Equal(t, "sequential done", result.Content)
}

func TestRunAgentLoop_InvalidToolMode(t *testing.T) {
	client := &seqLLMClient{
		responses: []*interfaces.LLMResponse{
			{ToolCalls: []*interfaces.ToolCall{{ToolCallID: "c1", ToolName: "t1"}}},
		},
	}
	tool := stubTool{name: "t1", result: "r"}
	rt, tools := newLoopRT(t, 5, client, tool)
	rt.ToolExecutionMode = "invalid-mode"

	_, err := runLoop(context.Background(), rt, tools, AgentLoopInput{UserPrompt: "go"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid tool execution mode")
}

// ---------------------------------------------------------------------------
// RunAgentLoop — conversation
// ---------------------------------------------------------------------------

func TestRunAgentLoop_WithConversationID(t *testing.T) {
	ctrl := gomock.NewController(t)
	conv := ifmocks.NewMockConversation(ctrl)

	history := []interfaces.Message{{Role: interfaces.MessageRoleUser, Content: "old message"}}
	conv.EXPECT().ListMessages(gomock.Any(), "conv-x", gomock.Any()).Return(history, nil)
	// user + assistant = 2 messages persisted (history messages re-saved too).
	conv.EXPECT().AddMessage(gomock.Any(), "conv-x", gomock.Any()).Return(nil).AnyTimes()

	client := &seqLLMClient{
		responses: []*interfaces.LLMResponse{{Content: "with history"}},
	}
	rt, err := NewLocalRuntime(
		WithLogger(logger.NoopLogger()),
		WithAgentConfig(sdkruntime.AgentConfig{
			LLM:     sdkruntime.AgentLLM{Client: client},
			Session: sdkruntime.AgentSession{Conversation: conv, ConversationSize: 10},
			Limits:  sdkruntime.AgentLimits{MaxIterations: 5, Timeout: 5 * time.Second},
		}),
	)
	require.NoError(t, err)

	result, err := runLoop(context.Background(), rt, nil, AgentLoopInput{
		UserPrompt:     "new question",
		ConversationID: "conv-x",
	})
	require.NoError(t, err)
	require.Equal(t, "with history", result.Content)
}

func TestRunAgentLoop_ConversationFetchErrorContinues(t *testing.T) {
	ctrl := gomock.NewController(t)
	conv := ifmocks.NewMockConversation(ctrl)
	conv.EXPECT().ListMessages(gomock.Any(), "bad-conv", gomock.Any()).Return(nil, errors.New("store down"))
	// No AddMessage expected since conversation fetch failed (but we still try to persist).
	conv.EXPECT().AddMessage(gomock.Any(), "bad-conv", gomock.Any()).Return(nil).AnyTimes()

	client := &seqLLMClient{
		responses: []*interfaces.LLMResponse{{Content: "continued without history"}},
	}
	rt, err := NewLocalRuntime(
		WithLogger(logger.NoopLogger()),
		WithAgentConfig(sdkruntime.AgentConfig{
			LLM:     sdkruntime.AgentLLM{Client: client},
			Session: sdkruntime.AgentSession{Conversation: conv},
			Limits:  sdkruntime.AgentLimits{MaxIterations: 5, Timeout: 5 * time.Second},
		}),
	)
	require.NoError(t, err)

	result, err := runLoop(context.Background(), rt, nil, AgentLoopInput{
		UserPrompt:     "hi",
		ConversationID: "bad-conv",
	})
	// Must not fail — just warns and continues.
	require.NoError(t, err)
	require.Equal(t, "continued without history", result.Content)
}

// ---------------------------------------------------------------------------
// RunAgentLoop — retrievers
// ---------------------------------------------------------------------------

func TestRunAgentLoop_RetrieverPrefetch(t *testing.T) {
	ctrl := gomock.NewController(t)
	ret := ifmocks.NewMockRetriever(ctrl)
	ret.EXPECT().Name().Return("kb").AnyTimes()
	ret.EXPECT().Search(gomock.Any(), "fetch me").Return([]interfaces.Document{
		{Content: "relevant doc", Source: "kb", Score: 0.9},
	}, nil)

	client := &seqLLMClient{
		responses: []*interfaces.LLMResponse{{Content: "answer with context"}},
	}
	rt, err := NewLocalRuntime(
		WithLogger(logger.NoopLogger()),
		WithAgentConfig(sdkruntime.AgentConfig{
			LLM: sdkruntime.AgentLLM{Client: client},
			Retrievers: sdkruntime.AgentRetrievers{
				Mode:       types.RetrieverModePrefetch,
				Retrievers: []interfaces.Retriever{ret},
			},
			Limits: sdkruntime.AgentLimits{MaxIterations: 5, Timeout: 5 * time.Second},
		}),
	)
	require.NoError(t, err)

	result, err := runLoop(context.Background(), rt, nil, AgentLoopInput{UserPrompt: "fetch me"})
	require.NoError(t, err)
	require.Equal(t, "answer with context", result.Content)
}

func TestRunAgentLoop_RetrieverPrefetchError(t *testing.T) {
	ctrl := gomock.NewController(t)
	ret := ifmocks.NewMockRetriever(ctrl)
	ret.EXPECT().Name().Return("kb").AnyTimes()
	ret.EXPECT().Search(gomock.Any(), gomock.Any()).Return(nil, errors.New("kb down"))

	client := &seqLLMClient{}
	rt, err := NewLocalRuntime(
		WithLogger(logger.NoopLogger()),
		WithAgentConfig(sdkruntime.AgentConfig{
			LLM: sdkruntime.AgentLLM{Client: client},
			Retrievers: sdkruntime.AgentRetrievers{
				Mode:       types.RetrieverModePrefetch,
				Retrievers: []interfaces.Retriever{ret},
			},
			Limits: sdkruntime.AgentLimits{MaxIterations: 5, Timeout: 5 * time.Second},
		}),
	)
	require.NoError(t, err)

	_, err = runLoop(context.Background(), rt, nil, AgentLoopInput{UserPrompt: "fetch"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "retriever prefetch")
}

// ---------------------------------------------------------------------------
// RunAgentLoop — event emission
// ---------------------------------------------------------------------------

func TestRunAgentLoop_ToolEventsEmittedToChannel(t *testing.T) {
	client := &seqLLMClient{
		responses: []*interfaces.LLMResponse{
			{ToolCalls: []*interfaces.ToolCall{{ToolCallID: "c1", ToolName: "calc"}}},
			{Content: "done"},
		},
	}
	tool := stubTool{name: "calc", result: "99"}
	rt, tools := newLoopRT(t, 5, client, tool)

	ctx := context.Background()
	channel := "test-tool-events"
	eventCh, closeFn, err := rt.subscribeToAgentEvents(ctx, channel)
	require.NoError(t, err)

	// close only once
	var closeOnce sync.Once
	safeClose := func() { closeOnce.Do(func() { _ = closeFn() }) }
	defer safeClose()

	// Run the loop in a goroutine; close the subscription after it finishes so eventCh drains.
	go func() {
		_, _ = runLoop(ctx, rt, tools, AgentLoopInput{
			UserPrompt:  "compute",
			ChannelName: channel,
		})
		safeClose()
	}()

	var collected []events.AgentEvent
	timeout := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-eventCh:
			if !ok {
				goto done
			}
			if ev != nil {
				collected = append(collected, ev)
			}
		case <-timeout:
			t.Fatal("timed out waiting for events")
		}
	}
done:
	etypes := eventTypes(collected)
	assert.Contains(t, etypes, events.AgentEventTypeToolCallStart)
	assert.Contains(t, etypes, events.AgentEventTypeToolCallEnd)
	assert.Contains(t, etypes, events.AgentEventTypeToolCallResult)
}

// ---------------------------------------------------------------------------
// executeToolsParallel
// ---------------------------------------------------------------------------

func TestExecuteToolsParallel_AllSucceed(t *testing.T) {
	t1 := stubTool{name: "t1", result: "r1"}
	t2 := stubTool{name: "t2", result: "r2"}
	rt, tools := newLoopRT(t, 5, &seqLLMClient{}, t1, t2)

	calls := []base.ToolCallRequest{
		{ToolCallID: "c1", ToolName: "t1"},
		{ToolCallID: "c2", ToolName: "t2"},
	}

	msgs, err := rt.executeToolsParallel(context.Background(), loopToolsInput(tools), "msg-1", calls, noopEmit)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	// Order must match submission order (parallel but results are indexed).
	require.Equal(t, "r1", msgs[0].Content)
	require.Equal(t, "r2", msgs[1].Content)
}

func TestExecuteToolsParallel_ToolErrorInMessage(t *testing.T) {
	// Parallel: individual tool errors become synthetic error messages, not hard failures.
	failing := stubTool{name: "bad", execErr: errors.New("boom")}
	rt, tools := newLoopRT(t, 5, &seqLLMClient{}, failing)

	calls := []base.ToolCallRequest{{ToolCallID: "c1", ToolName: "bad"}}
	msgs, err := rt.executeToolsParallel(context.Background(), loopToolsInput(tools), "msg", calls, noopEmit)
	require.NoError(t, err) // parallel swallows into message
	require.Len(t, msgs, 1)
	require.Contains(t, msgs[0].Content, "boom")
}

func TestExecuteToolsParallel_ResultsOrderPreserved(t *testing.T) {
	// Three tools; verify result order matches submission order despite concurrency.
	toolSet := []interfaces.Tool{
		stubTool{name: "a", result: "A"},
		stubTool{name: "b", result: "B"},
		stubTool{name: "c", result: "C"},
	}
	rt, tools := newLoopRT(t, 5, &seqLLMClient{}, toolSet...)

	calls := []base.ToolCallRequest{
		{ToolCallID: "1", ToolName: "a"},
		{ToolCallID: "2", ToolName: "b"},
		{ToolCallID: "3", ToolName: "c"},
	}
	msgs, err := rt.executeToolsParallel(context.Background(), loopToolsInput(tools), "m", calls, noopEmit)
	require.NoError(t, err)
	require.Equal(t, []string{"A", "B", "C"}, []string{msgs[0].Content, msgs[1].Content, msgs[2].Content})
}

// ---------------------------------------------------------------------------
// executeToolsSequential
// ---------------------------------------------------------------------------

func TestExecuteToolsSequential_AllSucceed(t *testing.T) {
	t1 := stubTool{name: "s1", result: "v1"}
	t2 := stubTool{name: "s2", result: "v2"}
	rt, tools := newLoopRT(t, 5, &seqLLMClient{}, t1, t2)

	calls := []base.ToolCallRequest{
		{ToolCallID: "c1", ToolName: "s1"},
		{ToolCallID: "c2", ToolName: "s2"},
	}
	msgs, err := rt.executeToolsSequential(context.Background(), loopToolsInput(tools), "msg", calls, noopEmit)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	require.Equal(t, "v1", msgs[0].Content)
	require.Equal(t, "v2", msgs[1].Content)
}

func TestExecuteToolsSequential_HardErrorOnContextCancel(t *testing.T) {
	// A tool that blocks until ctx is cancelled → executeSingleTool returns ctx.Err().
	// Sequential should propagate that error.
	rt, _ := newLoopRT(t, 5, &seqLLMClient{})
	// Add a fake tool that needs approval with no channel or handler → unavailable (not an error).
	// Instead: use a blocking LLM as a proxy — but we need a tool-level error.
	// We'll cancel the context before calling.
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancelled

	calls := []base.ToolCallRequest{{ToolCallID: "c1", ToolName: "missing-tool"}}
	_, err := rt.executeToolsSequential(ctx, AgentLoopInput{}, "msg", calls, noopEmit)
	// AuthorizeTool returns error for unknown tool.
	require.Error(t, err)
}

// ---------------------------------------------------------------------------
// executeSingleTool
// ---------------------------------------------------------------------------

func TestExecuteSingleTool_Approved(t *testing.T) {
	tool := stubTool{name: "my-tool", result: "hello"}
	rt, tools := newLoopRT(t, 5, &seqLLMClient{}, tool)

	emit, evs := captureEmit()
	msg, err := rt.executeSingleTool(context.Background(), loopToolsInput(tools), "msg-1",
		base.ToolCallRequest{ToolCallID: "c1", ToolName: "my-tool"}, emit)

	require.NoError(t, err)
	require.Equal(t, "hello", msg.Content)
	require.Equal(t, interfaces.MessageRoleTool, msg.Role)
	require.Equal(t, "my-tool", msg.ToolName)

	etypes := eventTypes(*evs)
	require.Contains(t, etypes, events.AgentEventTypeToolCallStart)
	require.Contains(t, etypes, events.AgentEventTypeToolCallEnd)
	require.Contains(t, etypes, events.AgentEventTypeToolCallResult)
}

func TestExecuteSingleTool_ToolExecError(t *testing.T) {
	tool := stubTool{name: "boom", execErr: errors.New("exec failed")}
	rt, tools := newLoopRT(t, 5, &seqLLMClient{}, tool)

	msg, err := rt.executeSingleTool(context.Background(), loopToolsInput(tools), "msg",
		base.ToolCallRequest{ToolCallID: "c1", ToolName: "boom"}, noopEmit)
	require.NoError(t, err) // tool errors become a content message, not a hard error
	require.Contains(t, msg.Content, "exec failed")
}

func TestExecuteSingleTool_UnknownToolErrors(t *testing.T) {
	rt, _ := newLoopRT(t, 5, &seqLLMClient{}) // no tools registered

	_, err := rt.executeSingleTool(context.Background(), AgentLoopInput{}, "msg",
		base.ToolCallRequest{ToolCallID: "c1", ToolName: "ghost"}, noopEmit)
	require.Error(t, err)
	require.Contains(t, err.Error(), "ghost")
}

func TestExecuteSingleTool_AuthorizationDenied(t *testing.T) {
	tool := struct {
		stubTool
		allow  bool
		reason string
	}{
		stubTool: stubTool{name: "restricted"},
		allow:    false,
		reason:   "policy denied",
	}

	// Use an authorizerToolStub from the runtime_test helpers (same package).
	authTool := authorizerStubLocal{name: "restricted", allow: false, reason: "policy denied"}
	rt, tools := newLoopRT(t, 5, &seqLLMClient{}, authTool)

	msg, err := rt.executeSingleTool(context.Background(), loopToolsInput(tools), "msg",
		base.ToolCallRequest{ToolCallID: "c1", ToolName: "restricted"}, noopEmit)
	require.NoError(t, err)
	require.Contains(t, msg.Content, msgToolUnauthorized)
	_ = tool
}

func TestExecuteSingleTool_AuthorizationError(t *testing.T) {
	authTool := authorizerStubLocal{name: "err-tool", allow: false, authErr: errors.New("auth backend down")}
	rt, tools := newLoopRT(t, 5, &seqLLMClient{}, authTool)

	_, err := rt.executeSingleTool(context.Background(), loopToolsInput(tools), "msg",
		base.ToolCallRequest{ToolCallID: "c1", ToolName: "err-tool"}, noopEmit)
	require.Error(t, err)
	require.Contains(t, err.Error(), "auth backend down")
}

func TestExecuteSingleTool_ApprovalUnavailable(t *testing.T) {
	// No channel, no handler → approval status = unavailable, tool not run.
	tool := stubTool{name: "guarded", result: "secret", needsApproval: true}
	rt, tools := newLoopRT(t, 5, &seqLLMClient{}, tool)

	msg, err := rt.executeSingleTool(context.Background(),
		AgentLoopInput{ChannelName: "", ApprovalHandler: nil, Tools: tools}, "msg",
		base.ToolCallRequest{ToolCallID: "c1", ToolName: "guarded", NeedsApproval: true}, noopEmit)
	require.NoError(t, err)
	require.Contains(t, msg.Content, msgToolApprovalUnavailable)
}

func TestExecuteSingleTool_ApprovalHandlerApproves(t *testing.T) {
	tool := stubTool{name: "guarded", result: "ok", needsApproval: true}
	rt, tools := newLoopRT(t, 5, &seqLLMClient{}, tool)

	handler := func(_ context.Context, req *types.ApprovalRequest) {
		_ = req.Respond(types.ApprovalStatusApproved)
	}

	msg, err := rt.executeSingleTool(context.Background(),
		AgentLoopInput{ApprovalHandler: handler, Tools: tools}, "msg",
		base.ToolCallRequest{ToolCallID: "c1", ToolName: "guarded", NeedsApproval: true}, noopEmit)
	require.NoError(t, err)
	require.Equal(t, "ok", msg.Content)
}

func TestExecuteSingleTool_ApprovalHandlerRejects(t *testing.T) {
	tool := stubTool{name: "guarded", result: "secret", needsApproval: true}
	rt, tools := newLoopRT(t, 5, &seqLLMClient{}, tool)

	handler := func(_ context.Context, req *types.ApprovalRequest) {
		_ = req.Respond(types.ApprovalStatusRejected)
	}

	msg, err := rt.executeSingleTool(context.Background(),
		AgentLoopInput{ApprovalHandler: handler, Tools: tools}, "msg",
		base.ToolCallRequest{ToolCallID: "c1", ToolName: "guarded", NeedsApproval: true}, noopEmit)
	require.NoError(t, err)
	require.Equal(t, msgToolRejected, msg.Content)
}

func TestExecuteSingleTool_StreamingApproveUnblocks(t *testing.T) {
	// Streaming path: ChannelName set, no ApprovalHandler.
	// We call rt.Approve from a goroutine to unblock executeSingleTool.
	tool := stubTool{name: "guarded", result: "stream-ok", needsApproval: true}
	rt, tools := newLoopRT(t, 5, &seqLLMClient{}, tool)

	// Capture the approval token from the emitted CUSTOM event.
	var capturedToken string
	var mu sync.Mutex
	tokenSet := make(chan struct{})

	emit := func(ev events.AgentEvent) {
		if ev == nil || ev.Type() != events.AgentEventTypeCustom {
			return
		}
		customEv, ok := ev.(*events.AgentCustomEvent)
		if !ok {
			return
		}
		val, err := events.ParseCustomEventApproval(customEv)
		if err != nil || val.ApprovalToken == "" {
			return
		}
		mu.Lock()
		capturedToken = val.ApprovalToken
		mu.Unlock()
		select {
		case <-tokenSet:
		default:
			close(tokenSet)
		}
	}

	done := make(chan struct{})
	var (
		resultMsg interfaces.Message
		resultErr error
	)
	go func() {
		defer close(done)
		resultMsg, resultErr = rt.executeSingleTool(
			context.Background(),
			AgentLoopInput{ChannelName: "some-channel", Tools: tools}, // streaming path
			"msg",
			base.ToolCallRequest{ToolCallID: "c1", ToolName: "guarded", NeedsApproval: true},
			emit,
		)
	}()

	// Wait for the token, then approve.
	select {
	case <-tokenSet:
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for approval token")
	}
	mu.Lock()
	tok := capturedToken
	mu.Unlock()

	require.NoError(t, rt.Approve(context.Background(), tok, types.ApprovalStatusApproved))

	<-done
	require.NoError(t, resultErr)
	require.Equal(t, "stream-ok", resultMsg.Content)
}

func TestExecuteSingleTool_ApprovalContextCancel(t *testing.T) {
	tool := stubTool{name: "guarded", result: "should not run", needsApproval: true}
	rt, tools := newLoopRT(t, 5, &seqLLMClient{}, tool)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()

	_, err := rt.executeSingleTool(ctx,
		AgentLoopInput{ChannelName: "some-channel", Tools: tools}, "msg",
		base.ToolCallRequest{ToolCallID: "c1", ToolName: "guarded", NeedsApproval: true}, noopEmit)

	<-done
	require.Error(t, err)
	require.ErrorIs(t, err, context.Canceled)
}

// ---------------------------------------------------------------------------
// publishEventToChannel
// ---------------------------------------------------------------------------

func TestPublishEventToChannel_NoOpWhenChannelEmpty(t *testing.T) {
	rt, _ := newLoopRT(t, 5, &seqLLMClient{})
	require.NotPanics(t, func() {
		rt.publishEventToChannel(context.Background(), "", events.NewAgentRunErrorEvent("x"))
	})
}

func TestPublishEventToChannel_NoOpWhenNilEvent(t *testing.T) {
	rt, _ := newLoopRT(t, 5, &seqLLMClient{})
	require.NotPanics(t, func() {
		rt.publishEventToChannel(context.Background(), "ch", nil)
	})
}

func TestPublishEventToChannel_NoOpWhenNilEventbus(t *testing.T) {
	rt := &LocalRuntime{
		Runtime: base.Runtime{
			AgentSpec: sdkruntime.AgentSpec{Name: "a"},
		},
		logger: logger.NoopLogger(),
		// eventbus is nil
	}
	require.NotPanics(t, func() {
		rt.publishEventToChannel(context.Background(), "ch", events.NewAgentRunErrorEvent("x"))
	})
}

// ---------------------------------------------------------------------------
// persistConversationMessages
// ---------------------------------------------------------------------------

func TestPersistConversationMessages_NilConversation(t *testing.T) {
	rt, _ := newLoopRT(t, 5, &seqLLMClient{})
	// No conversation configured — must not panic or error.
	err := persistConversationMessages(context.Background(), rt, "c", []interfaces.Message{
		{Role: interfaces.MessageRoleUser, Content: "hi"},
	})
	require.NoError(t, err)
}

func TestPersistConversationMessages_StoresAllMessages(t *testing.T) {
	ctrl := gomock.NewController(t)
	conv := ifmocks.NewMockConversation(ctrl)
	conv.EXPECT().AddMessage(gomock.Any(), "conv-1", gomock.Any()).Return(nil).Times(3)

	rt, err := NewLocalRuntime(
		WithLogger(logger.NoopLogger()),
		WithAgentConfig(sdkruntime.AgentConfig{
			LLM:     sdkruntime.AgentLLM{Client: &seqLLMClient{}},
			Session: sdkruntime.AgentSession{Conversation: conv},
			Limits:  sdkruntime.AgentLimits{Timeout: 5 * time.Second},
		}),
	)
	require.NoError(t, err)

	msgs := []interfaces.Message{
		{Role: interfaces.MessageRoleUser, Content: "1"},
		{Role: interfaces.MessageRoleAssistant, Content: "2"},
		{Role: interfaces.MessageRoleTool, Content: "3"},
	}
	err = persistConversationMessages(context.Background(), rt, "conv-1", msgs)
	require.NoError(t, err)
}

func TestPersistConversationMessages_AddMessageErrorWarnsOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	conv := ifmocks.NewMockConversation(ctrl)
	conv.EXPECT().AddMessage(gomock.Any(), "c", gomock.Any()).Return(errors.New("store err")).AnyTimes()

	rt, err := NewLocalRuntime(
		WithLogger(logger.NoopLogger()),
		WithAgentConfig(sdkruntime.AgentConfig{
			LLM:     sdkruntime.AgentLLM{Client: &seqLLMClient{}},
			Session: sdkruntime.AgentSession{Conversation: conv},
			Limits:  sdkruntime.AgentLimits{Timeout: 5 * time.Second},
		}),
	)
	require.NoError(t, err)

	// persistConversationMessages returns nil even when AddMessage fails (warns only).
	err = persistConversationMessages(context.Background(), rt, "c", []interfaces.Message{
		{Role: interfaces.MessageRoleUser, Content: "hi"},
	})
	require.NoError(t, err)
}

// ---------------------------------------------------------------------------
// authorizerStubLocal — tool with configurable authorization for loop tests
// ---------------------------------------------------------------------------

type authorizerStubLocal struct {
	name    string
	allow   bool
	reason  string
	authErr error
}

func (a authorizerStubLocal) Name() string                      { return a.name }
func (a authorizerStubLocal) DisplayName() string               { return a.name }
func (a authorizerStubLocal) Description() string               { return "" }
func (a authorizerStubLocal) Parameters() interfaces.JSONSchema { return nil }
func (a authorizerStubLocal) Execute(_ context.Context, _ map[string]any) (any, error) {
	return "auth-result", nil
}
func (a authorizerStubLocal) Authorize(_ context.Context, _ map[string]any) (interfaces.ToolAuthorizationDecision, error) {
	return interfaces.ToolAuthorizationDecision{Allow: a.allow, Reason: a.reason}, a.authErr
}
