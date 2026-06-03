package base

import (
	"context"
	"errors"
	"testing"

	"github.com/agenticenv/agent-sdk-go/internal/events"
	sdkruntime "github.com/agenticenv/agent-sdk-go/internal/runtime"
	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	ifmocks "github.com/agenticenv/agent-sdk-go/pkg/interfaces/mocks"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
	"github.com/agenticenv/agent-sdk-go/pkg/observability"
	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/require"
)

// newTestRuntime returns a Runtime wired with noop tracer/metrics and the provided execution.
func newTestRuntime(exec sdkruntime.AgentExecution) *Runtime {
	return &Runtime{
		AgentSpec: sdkruntime.AgentSpec{
			Name:         "test-agent",
			SystemPrompt: "you are helpful",
		},
		AgentExecution: exec,
		Tracer:         observability.DefaultNoopTracer,
		Metrics:        observability.DefaultNoopMetrics,
	}
}

func noopLog() logger.Logger { return logger.NoopLogger() }

// stubLLMClient is a minimal LLMClient that returns a fixed response.
type stubLLMClient struct {
	resp *interfaces.LLMResponse
	err  error
}

func (s stubLLMClient) Generate(_ context.Context, _ *interfaces.LLMRequest) (*interfaces.LLMResponse, error) {
	return s.resp, s.err
}
func (stubLLMClient) GenerateStream(_ context.Context, _ *interfaces.LLMRequest) (interfaces.LLMStream, error) {
	return nil, errors.New("stream not implemented in stub")
}
func (stubLLMClient) GetModel() string                    { return "stub" }
func (stubLLMClient) GetProvider() interfaces.LLMProvider { return interfaces.LLMProviderOpenAI }
func (stubLLMClient) IsStreamSupported() bool             { return false }

// --- BuildLLMRequest ---

func TestBuildLLMRequest_Basic(t *testing.T) {
	rt := newTestRuntime(sdkruntime.AgentExecution{
		LLM: sdkruntime.AgentLLM{Client: stubLLMClient{}},
	})
	msgs := []interfaces.Message{{Role: interfaces.MessageRoleUser, Content: "hello"}}
	req, tools := rt.BuildLLMRequest(msgs, false, "")

	require.Equal(t, "you are helpful", req.SystemMessage)
	require.Equal(t, msgs, req.Messages)
	require.Empty(t, tools)
}

func TestBuildLLMRequest_WithRetrieverContext(t *testing.T) {
	rt := newTestRuntime(sdkruntime.AgentExecution{
		LLM: sdkruntime.AgentLLM{Client: stubLLMClient{}},
	})
	req, _ := rt.BuildLLMRequest(nil, false, "extra context")
	require.Contains(t, req.SystemMessage, "you are helpful")
	require.Contains(t, req.SystemMessage, "extra context")
}

func TestBuildLLMRequest_SkipTools(t *testing.T) {
	ctrl := gomock.NewController(t)
	tool := ifmocks.NewMockTool(ctrl)
	tool.EXPECT().Name().Return("t").AnyTimes()
	tool.EXPECT().Description().Return("").AnyTimes()
	tool.EXPECT().Parameters().Return(nil).AnyTimes()

	rt := newTestRuntime(sdkruntime.AgentExecution{
		LLM:   sdkruntime.AgentLLM{Client: stubLLMClient{}},
		Tools: sdkruntime.AgentTools{Tools: []interfaces.Tool{tool}},
	})
	req, _ := rt.BuildLLMRequest(nil, true, "")
	require.Empty(t, req.Tools)
}

// approvalToolStub is a Tool that also implements ToolApproval.
type approvalToolStub struct {
	name             string
	approvalRequired bool
}

func (a approvalToolStub) Name() string                                             { return a.name }
func (a approvalToolStub) DisplayName() string                                      { return a.name }
func (a approvalToolStub) Description() string                                      { return "" }
func (a approvalToolStub) Parameters() interfaces.JSONSchema                        { return nil }
func (a approvalToolStub) Execute(_ context.Context, _ map[string]any) (any, error) { return nil, nil }
func (a approvalToolStub) ApprovalRequired() bool                                   { return a.approvalRequired }

// authorizerToolStub is a Tool that also implements ToolAuthorizer.
type authorizerToolStub struct {
	name   string
	allow  bool
	reason string
	err    error
}

func (a authorizerToolStub) Name() string                      { return a.name }
func (a authorizerToolStub) DisplayName() string               { return a.name }
func (a authorizerToolStub) Description() string               { return "" }
func (a authorizerToolStub) Parameters() interfaces.JSONSchema { return nil }
func (a authorizerToolStub) Execute(_ context.Context, _ map[string]any) (any, error) {
	return nil, nil
}
func (a authorizerToolStub) Authorize(_ context.Context, _ map[string]any) (interfaces.ToolAuthorizationDecision, error) {
	return interfaces.ToolAuthorizationDecision{Allow: a.allow, Reason: a.reason}, a.err
}

// --- RequiresApproval ---

func TestRequiresApproval_NoPolicyToolHasApproval(t *testing.T) {
	rt := newTestRuntime(sdkruntime.AgentExecution{})
	tool := approvalToolStub{name: "t", approvalRequired: true}
	require.True(t, rt.RequiresApproval(tool))
}

func TestRequiresApproval_NoPolicyToolNoApproval(t *testing.T) {
	ctrl := gomock.NewController(t)
	tool := ifmocks.NewMockTool(ctrl)
	rt := newTestRuntime(sdkruntime.AgentExecution{})
	require.False(t, rt.RequiresApproval(tool))
}

// --- FetchConversationMessages ---

func TestFetchConversationMessages_NoConversation(t *testing.T) {
	rt := newTestRuntime(sdkruntime.AgentExecution{
		Session: sdkruntime.AgentSession{Conversation: nil},
	})
	_, err := rt.FetchConversationMessages(context.Background(), noopLog(), "conv-1")
	require.Error(t, err)
	require.Contains(t, err.Error(), "conversation is not configured")
}

func TestFetchConversationMessages_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	conv := ifmocks.NewMockConversation(ctrl)
	msgs := []interfaces.Message{{Role: interfaces.MessageRoleUser, Content: "hi"}}
	conv.EXPECT().ListMessages(gomock.Any(), "conv-1", gomock.Any()).Return(msgs, nil)

	rt := newTestRuntime(sdkruntime.AgentExecution{
		Session: sdkruntime.AgentSession{Conversation: conv, ConversationSize: 10},
	})
	got, err := rt.FetchConversationMessages(context.Background(), noopLog(), "conv-1")
	require.NoError(t, err)
	require.Equal(t, msgs, got)
}

func TestFetchConversationMessages_Error(t *testing.T) {
	ctrl := gomock.NewController(t)
	conv := ifmocks.NewMockConversation(ctrl)
	conv.EXPECT().ListMessages(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil, errors.New("store down"))

	rt := newTestRuntime(sdkruntime.AgentExecution{
		Session: sdkruntime.AgentSession{Conversation: conv},
	})
	_, err := rt.FetchConversationMessages(context.Background(), noopLog(), "c")
	require.Error(t, err)
	require.Contains(t, err.Error(), "store down")
}

// --- ExecuteTool ---

func TestExecuteTool_UnknownTool(t *testing.T) {
	rt := newTestRuntime(sdkruntime.AgentExecution{
		Tools: sdkruntime.AgentTools{Tools: nil},
	})
	_, err := rt.ExecuteTool(context.Background(), noopLog(), "missing", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown tool")
}

func TestExecuteTool_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	tool := ifmocks.NewMockTool(ctrl)
	tool.EXPECT().Name().Return("calc").AnyTimes()
	tool.EXPECT().Execute(gomock.Any(), gomock.Any()).Return("42", nil)

	rt := newTestRuntime(sdkruntime.AgentExecution{
		Tools: sdkruntime.AgentTools{Tools: []interfaces.Tool{tool}},
	})
	result, err := rt.ExecuteTool(context.Background(), noopLog(), "calc", map[string]any{"x": 1})
	require.NoError(t, err)
	require.Equal(t, "42", result)
}

func TestExecuteTool_ToolError(t *testing.T) {
	ctrl := gomock.NewController(t)
	tool := ifmocks.NewMockTool(ctrl)
	tool.EXPECT().Name().Return("fail-tool").AnyTimes()
	tool.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(nil, errors.New("tool failed"))

	rt := newTestRuntime(sdkruntime.AgentExecution{
		Tools: sdkruntime.AgentTools{Tools: []interfaces.Tool{tool}},
	})
	_, err := rt.ExecuteTool(context.Background(), noopLog(), "fail-tool", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "tool failed")
}

// --- AuthorizeTool ---

func TestAuthorizeTool_UnknownTool(t *testing.T) {
	rt := newTestRuntime(sdkruntime.AgentExecution{})
	_, err := rt.AuthorizeTool(context.Background(), noopLog(), "ghost", nil)
	require.Error(t, err)
}

func TestAuthorizeTool_NoAuthorizer_AllowedByDefault(t *testing.T) {
	ctrl := gomock.NewController(t)
	tool := ifmocks.NewMockTool(ctrl)
	tool.EXPECT().Name().Return("plain").AnyTimes()

	rt := newTestRuntime(sdkruntime.AgentExecution{
		Tools: sdkruntime.AgentTools{Tools: []interfaces.Tool{tool}},
	})
	result, err := rt.AuthorizeTool(context.Background(), noopLog(), "plain", nil)
	require.NoError(t, err)
	require.True(t, result.Allowed)
}

func TestAuthorizeTool_Allowed(t *testing.T) {
	tool := authorizerToolStub{name: "secure", allow: true}
	rt := newTestRuntime(sdkruntime.AgentExecution{
		Tools: sdkruntime.AgentTools{Tools: []interfaces.Tool{tool}},
	})
	result, err := rt.AuthorizeTool(context.Background(), noopLog(), "secure", nil)
	require.NoError(t, err)
	require.True(t, result.Allowed)
}

func TestAuthorizeTool_Denied(t *testing.T) {
	tool := authorizerToolStub{name: "gated", allow: false, reason: "not allowed"}
	rt := newTestRuntime(sdkruntime.AgentExecution{
		Tools: sdkruntime.AgentTools{Tools: []interfaces.Tool{tool}},
	})
	result, err := rt.AuthorizeTool(context.Background(), noopLog(), "gated", nil)
	require.NoError(t, err)
	require.False(t, result.Allowed)
	require.Equal(t, "not allowed", result.Reason)
}

// --- ExecuteRetrievers ---

func TestExecuteRetrievers_NoRetrievers(t *testing.T) {
	rt := newTestRuntime(sdkruntime.AgentExecution{})
	got, err := rt.ExecuteRetrievers(context.Background(), noopLog(), "query")
	require.NoError(t, err)
	require.Equal(t, "", got)
}

func TestExecuteRetrievers_AllFail(t *testing.T) {
	ctrl := gomock.NewController(t)
	r := ifmocks.NewMockRetriever(ctrl)
	r.EXPECT().Name().Return("r1").AnyTimes()
	r.EXPECT().Search(gomock.Any(), gomock.Any()).Return(nil, errors.New("down"))

	rt := newTestRuntime(sdkruntime.AgentExecution{
		Retrievers: sdkruntime.AgentRetrievers{Retrievers: []interfaces.Retriever{r}},
	})
	_, err := rt.ExecuteRetrievers(context.Background(), noopLog(), "q")
	require.Error(t, err)
	require.Contains(t, err.Error(), "all")
}

func TestExecuteRetrievers_Success(t *testing.T) {
	ctrl := gomock.NewController(t)
	r := ifmocks.NewMockRetriever(ctrl)
	r.EXPECT().Name().Return("kb").AnyTimes()
	r.EXPECT().Search(gomock.Any(), "my query").Return([]interfaces.Document{
		{Content: "doc content", Source: "src", Score: 0.95},
	}, nil)

	rt := newTestRuntime(sdkruntime.AgentExecution{
		Retrievers: sdkruntime.AgentRetrievers{Retrievers: []interfaces.Retriever{r}},
	})
	got, err := rt.ExecuteRetrievers(context.Background(), noopLog(), "my query")
	require.NoError(t, err)
	require.Contains(t, got, "doc content")
}

// --- ExecuteLLM ---

func TestExecuteLLM_LLMError(t *testing.T) {
	rt := newTestRuntime(sdkruntime.AgentExecution{
		LLM: sdkruntime.AgentLLM{Client: stubLLMClient{err: errors.New("llm unavailable")}},
	})
	_, err := rt.ExecuteLLM(context.Background(), noopLog(), "agent", "msg-1", nil, false, "", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "llm unavailable")
}

func TestExecuteLLM_Success_NoTools(t *testing.T) {
	rt := newTestRuntime(sdkruntime.AgentExecution{
		LLM: sdkruntime.AgentLLM{Client: stubLLMClient{
			resp: &interfaces.LLMResponse{Content: "hello world"},
		}},
	})
	result, err := rt.ExecuteLLM(context.Background(), noopLog(), "agent", "msg-1", nil, false, "", nil)
	require.NoError(t, err)
	require.Equal(t, "hello world", result.Content)
	require.Empty(t, result.ToolCalls)
}

func TestExecuteLLM_EmitsTextMessageEvents(t *testing.T) {
	rt := newTestRuntime(sdkruntime.AgentExecution{
		LLM: sdkruntime.AgentLLM{Client: stubLLMClient{
			resp: &interfaces.LLMResponse{Content: "response text"},
		}},
	})

	var emitted []events.AgentEventType
	emit := func(ev events.AgentEvent) {
		emitted = append(emitted, ev.Type())
	}

	_, err := rt.ExecuteLLM(context.Background(), noopLog(), "agent", "msg-1", nil, false, "", emit)
	require.NoError(t, err)
	require.Equal(t, []events.AgentEventType{
		events.AgentEventTypeTextMessageStart,
		events.AgentEventTypeTextMessageContent,
		events.AgentEventTypeTextMessageEnd,
	}, emitted)
}

func TestExecuteLLM_NilEmitDoesNotPanic(t *testing.T) {
	rt := newTestRuntime(sdkruntime.AgentExecution{
		LLM: sdkruntime.AgentLLM{Client: stubLLMClient{
			resp: &interfaces.LLMResponse{Content: "ok"},
		}},
	})
	require.NotPanics(t, func() {
		_, _ = rt.ExecuteLLM(context.Background(), noopLog(), "a", "m", nil, false, "", nil)
	})
}

func TestExecuteLLM_UnknownToolCallReturnsError(t *testing.T) {
	rt := newTestRuntime(sdkruntime.AgentExecution{
		LLM: sdkruntime.AgentLLM{Client: stubLLMClient{
			resp: &interfaces.LLMResponse{
				Content: "",
				ToolCalls: []*interfaces.ToolCall{
					{ToolCallID: "1", ToolName: "nonexistent", Args: nil},
				},
			},
		}},
	})
	_, err := rt.ExecuteLLM(context.Background(), noopLog(), "a", "m", nil, false, "", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown tool")
}

func TestExecuteLLM_WithUsageMetrics(t *testing.T) {
	rt := newTestRuntime(sdkruntime.AgentExecution{
		LLM: sdkruntime.AgentLLM{Client: stubLLMClient{
			resp: &interfaces.LLMResponse{
				Content: "ok",
				Usage:   &interfaces.LLMUsage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			},
		}},
	})
	result, err := rt.ExecuteLLM(context.Background(), noopLog(), "a", "m", nil, false, "", nil)
	require.NoError(t, err)
	require.NotNil(t, result.Usage)
	require.EqualValues(t, 10, result.Usage.PromptTokens)
}

func TestExecuteLLM_ToolCallWithEmptyDisplayName(t *testing.T) {
	ctrl := gomock.NewController(t)
	tool := ifmocks.NewMockTool(ctrl)
	tool.EXPECT().Name().Return("my-tool").AnyTimes()
	tool.EXPECT().Description().Return("").AnyTimes()
	tool.EXPECT().Parameters().Return(nil).AnyTimes()
	tool.EXPECT().DisplayName().Return("").AnyTimes() // empty → falls back to tool name

	rt := newTestRuntime(sdkruntime.AgentExecution{
		LLM: sdkruntime.AgentLLM{Client: stubLLMClient{
			resp: &interfaces.LLMResponse{
				ToolCalls: []*interfaces.ToolCall{
					{ToolCallID: "tc1", ToolName: "my-tool"},
				},
			},
		}},
		Tools: sdkruntime.AgentTools{Tools: []interfaces.Tool{tool}},
	})
	result, err := rt.ExecuteLLM(context.Background(), noopLog(), "a", "m", nil, false, "", nil)
	require.NoError(t, err)
	require.Len(t, result.ToolCalls, 1)
	require.Equal(t, "my-tool", result.ToolCalls[0].ToolDisplayName)
}

func TestExecuteLLM_NilToolCallInResponse(t *testing.T) {
	rt := newTestRuntime(sdkruntime.AgentExecution{
		LLM: sdkruntime.AgentLLM{Client: stubLLMClient{
			resp: &interfaces.LLMResponse{
				Content:   "answer",
				ToolCalls: []*interfaces.ToolCall{nil}, // nil entry must be skipped
			},
		}},
	})
	result, err := rt.ExecuteLLM(context.Background(), noopLog(), "a", "m", nil, false, "", nil)
	require.NoError(t, err)
	require.Empty(t, result.ToolCalls)
}

// --- RequiresApproval with policy ---

func TestRequiresApproval_PolicyOverrides(t *testing.T) {
	ctrl := gomock.NewController(t)
	tool := ifmocks.NewMockTool(ctrl)
	policy := ifmocks.NewMockAgentToolApprovalPolicy(ctrl)
	policy.EXPECT().RequiresApproval(tool).Return(true)

	rt := newTestRuntime(sdkruntime.AgentExecution{
		Tools: sdkruntime.AgentTools{ApprovalPolicy: policy},
	})
	require.True(t, rt.RequiresApproval(tool))
}

// --- AuthorizeTool error path ---

func TestAuthorizeTool_AuthorizerError(t *testing.T) {
	tool := authorizerToolStub{name: "err-tool", err: errors.New("auth backend down")}
	rt := newTestRuntime(sdkruntime.AgentExecution{
		Tools: sdkruntime.AgentTools{Tools: []interfaces.Tool{tool}},
	})
	_, err := rt.AuthorizeTool(context.Background(), noopLog(), "err-tool", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "auth backend down")
}

// --- ExecuteRetrievers partial failure ---

func TestExecuteRetrievers_PartialFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	good := ifmocks.NewMockRetriever(ctrl)
	good.EXPECT().Name().Return("good").AnyTimes()
	good.EXPECT().Search(gomock.Any(), gomock.Any()).Return([]interfaces.Document{
		{Content: "useful", Source: "s", Score: 0.8},
	}, nil)

	bad := ifmocks.NewMockRetriever(ctrl)
	bad.EXPECT().Name().Return("bad").AnyTimes()
	bad.EXPECT().Search(gomock.Any(), gomock.Any()).Return(nil, errors.New("timeout"))

	rt := newTestRuntime(sdkruntime.AgentExecution{
		Retrievers: sdkruntime.AgentRetrievers{Retrievers: []interfaces.Retriever{good, bad}},
	})
	got, err := rt.ExecuteRetrievers(context.Background(), noopLog(), "q")
	require.NoError(t, err) // partial is ok
	require.Contains(t, got, "useful")
}

// --- ExecuteLLMStream ---

// streamCapableLLMClient wraps a stubLLMClient and sets IsStreamSupported=true.
type streamCapableLLMClient struct {
	stubLLMClient
	stream    interfaces.LLMStream
	streamErr error
}

func (s streamCapableLLMClient) IsStreamSupported() bool { return true }
func (s streamCapableLLMClient) GenerateStream(_ context.Context, _ *interfaces.LLMRequest) (interfaces.LLMStream, error) {
	return s.stream, s.streamErr
}

// fixedStream is a simple in-memory LLMStream backed by a slice of chunks.
type fixedStream struct {
	chunks []*interfaces.LLMStreamChunk
	pos    int
	result *interfaces.LLMResponse
	err    error
}

func newFixedStream(chunks []*interfaces.LLMStreamChunk, result *interfaces.LLMResponse) *fixedStream {
	return &fixedStream{chunks: chunks, result: result}
}

func (s *fixedStream) Next() bool {
	s.pos++
	return s.pos <= len(s.chunks)
}
func (s *fixedStream) Current() *interfaces.LLMStreamChunk {
	if s.pos < 1 || s.pos > len(s.chunks) {
		return nil
	}
	return s.chunks[s.pos-1]
}
func (s *fixedStream) Err() error                         { return s.err }
func (s *fixedStream) GetResult() *interfaces.LLMResponse { return s.result }

func TestExecuteLLMStream_FallbackGenerate_Success(t *testing.T) {
	rt := newTestRuntime(sdkruntime.AgentExecution{
		LLM: sdkruntime.AgentLLM{Client: stubLLMClient{
			resp: &interfaces.LLMResponse{Content: "fallback answer"},
		}},
	})
	result, err := rt.ExecuteLLMStream(context.Background(), noopLog(), "agent", "msg", nil, false, "", nil)
	require.NoError(t, err)
	require.Equal(t, "fallback answer", result.Content)
}

func TestExecuteLLMStream_FallbackGenerate_LLMError(t *testing.T) {
	rt := newTestRuntime(sdkruntime.AgentExecution{
		LLM: sdkruntime.AgentLLM{Client: stubLLMClient{err: errors.New("llm down")}},
	})
	_, err := rt.ExecuteLLMStream(context.Background(), noopLog(), "agent", "msg", nil, false, "", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "llm down")
}

func TestExecuteLLMStream_FallbackGenerate_EmitsEvents(t *testing.T) {
	rt := newTestRuntime(sdkruntime.AgentExecution{
		LLM: sdkruntime.AgentLLM{Client: stubLLMClient{
			resp: &interfaces.LLMResponse{Content: "hi"},
		}},
	})
	var emitted []events.AgentEventType
	emit := func(ev events.AgentEvent) { emitted = append(emitted, ev.Type()) }

	_, err := rt.ExecuteLLMStream(context.Background(), noopLog(), "agent", "msg", nil, false, "", emit)
	require.NoError(t, err)
	require.Equal(t, []events.AgentEventType{
		events.AgentEventTypeTextMessageStart,
		events.AgentEventTypeTextMessageContent,
		events.AgentEventTypeTextMessageEnd,
	}, emitted)
}

func TestExecuteLLMStream_GenerateStreamError(t *testing.T) {
	rt := newTestRuntime(sdkruntime.AgentExecution{
		LLM: sdkruntime.AgentLLM{Client: streamCapableLLMClient{
			streamErr: errors.New("stream init failed"),
		}},
	})
	_, err := rt.ExecuteLLMStream(context.Background(), noopLog(), "agent", "msg", nil, false, "", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "stream init failed")
}

func TestExecuteLLMStream_StreamError_AfterChunks(t *testing.T) {
	s := newFixedStream([]*interfaces.LLMStreamChunk{
		{ContentDelta: "partial"},
	}, nil)
	s.err = errors.New("connection reset")

	rt := newTestRuntime(sdkruntime.AgentExecution{
		LLM: sdkruntime.AgentLLM{Client: streamCapableLLMClient{stream: s}},
	})
	_, err := rt.ExecuteLLMStream(context.Background(), noopLog(), "agent", "msg", nil, false, "", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "connection reset")
}

func TestExecuteLLMStream_StreamNilResult(t *testing.T) {
	s := newFixedStream(nil, nil) // no chunks, GetResult() returns nil

	rt := newTestRuntime(sdkruntime.AgentExecution{
		LLM: sdkruntime.AgentLLM{Client: streamCapableLLMClient{stream: s}},
	})
	_, err := rt.ExecuteLLMStream(context.Background(), noopLog(), "agent", "msg", nil, false, "", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "stream completed without result")
}

func TestExecuteLLMStream_TextChunks_EmitsCorrectEvents(t *testing.T) {
	s := newFixedStream([]*interfaces.LLMStreamChunk{
		{ContentDelta: "hello"},
		{ContentDelta: " world"},
	}, &interfaces.LLMResponse{Content: "hello world"})

	rt := newTestRuntime(sdkruntime.AgentExecution{
		LLM: sdkruntime.AgentLLM{Client: streamCapableLLMClient{stream: s}},
	})

	var emitted []events.AgentEventType
	emit := func(ev events.AgentEvent) { emitted = append(emitted, ev.Type()) }

	result, err := rt.ExecuteLLMStream(context.Background(), noopLog(), "agent", "msg", nil, false, "", emit)
	require.NoError(t, err)
	require.Equal(t, "hello world", result.Content)
	require.Equal(t, events.AgentEventTypeTextMessageStart, emitted[0])
	require.Equal(t, events.AgentEventTypeTextMessageContent, emitted[1])
	require.Equal(t, events.AgentEventTypeTextMessageContent, emitted[2])
	require.Equal(t, events.AgentEventTypeTextMessageEnd, emitted[3])
}

func TestExecuteLLMStream_ReasoningChunks_EmitsReasoningEvents(t *testing.T) {
	s := newFixedStream([]*interfaces.LLMStreamChunk{
		{ThinkingDelta: "let me think"},
		{ContentDelta: "answer"},
	}, &interfaces.LLMResponse{Content: "answer"})

	rt := newTestRuntime(sdkruntime.AgentExecution{
		LLM: sdkruntime.AgentLLM{Client: streamCapableLLMClient{stream: s}},
	})

	var emitted []events.AgentEventType
	emit := func(ev events.AgentEvent) { emitted = append(emitted, ev.Type()) }

	_, err := rt.ExecuteLLMStream(context.Background(), noopLog(), "agent", "msg", nil, false, "", emit)
	require.NoError(t, err)

	// Reasoning events must appear before text events
	require.Equal(t, events.AgentEventTypeReasoningStart, emitted[0])
	require.Equal(t, events.AgentEventTypeReasoningMessageStart, emitted[1])
	require.Equal(t, events.AgentEventTypeReasoningMessageContent, emitted[2])
	// flush reasoning before text
	require.Equal(t, events.AgentEventTypeReasoningMessageEnd, emitted[3])
	require.Equal(t, events.AgentEventTypeReasoningEnd, emitted[4])
	require.Equal(t, events.AgentEventTypeTextMessageStart, emitted[5])
}

func TestExecuteLLMStream_ToolOnlyResponse_EmitsEmptyAssistantTurn(t *testing.T) {
	ctrl := gomock.NewController(t)
	tool := ifmocks.NewMockTool(ctrl)
	tool.EXPECT().Name().Return("search").AnyTimes()
	tool.EXPECT().Description().Return("").AnyTimes()
	tool.EXPECT().Parameters().Return(nil).AnyTimes()
	tool.EXPECT().DisplayName().Return("Search").AnyTimes()

	s := newFixedStream(nil, &interfaces.LLMResponse{
		ToolCalls: []*interfaces.ToolCall{{ToolCallID: "1", ToolName: "search"}},
	})

	rt := newTestRuntime(sdkruntime.AgentExecution{
		LLM:   sdkruntime.AgentLLM{Client: streamCapableLLMClient{stream: s}},
		Tools: sdkruntime.AgentTools{Tools: []interfaces.Tool{tool}},
	})

	var emitted []events.AgentEventType
	emit := func(ev events.AgentEvent) { emitted = append(emitted, ev.Type()) }

	result, err := rt.ExecuteLLMStream(context.Background(), noopLog(), "agent", "msg", nil, false, "", emit)
	require.NoError(t, err)
	require.Len(t, result.ToolCalls, 1)
	// finalizeAssistantText emits a start/content/end even when no text chunks arrived
	require.Contains(t, emitted, events.AgentEventTypeTextMessageStart)
	require.Contains(t, emitted, events.AgentEventTypeTextMessageEnd)
}

func TestExecuteLLMStream_WithUsageMetrics(t *testing.T) {
	s := newFixedStream(nil, &interfaces.LLMResponse{
		Content: "done",
		Usage:   &interfaces.LLMUsage{PromptTokens: 8, CompletionTokens: 4, TotalTokens: 12},
	})
	rt := newTestRuntime(sdkruntime.AgentExecution{
		LLM: sdkruntime.AgentLLM{Client: streamCapableLLMClient{stream: s}},
	})
	result, err := rt.ExecuteLLMStream(context.Background(), noopLog(), "agent", "msg", nil, false, "", nil)
	require.NoError(t, err)
	require.NotNil(t, result.Usage)
	require.EqualValues(t, 8, result.Usage.PromptTokens)
}

func TestExecuteLLMStream_NilChunkSkipped(t *testing.T) {
	s := newFixedStream([]*interfaces.LLMStreamChunk{
		nil,
		{ContentDelta: "text"},
	}, &interfaces.LLMResponse{Content: "text"})

	rt := newTestRuntime(sdkruntime.AgentExecution{
		LLM: sdkruntime.AgentLLM{Client: streamCapableLLMClient{stream: s}},
	})
	result, err := rt.ExecuteLLMStream(context.Background(), noopLog(), "agent", "msg", nil, false, "", nil)
	require.NoError(t, err)
	require.Equal(t, "text", result.Content)
}

func TestExecuteLLMStream_FallbackGenerate_WithUsage(t *testing.T) {
	rt := newTestRuntime(sdkruntime.AgentExecution{
		LLM: sdkruntime.AgentLLM{Client: stubLLMClient{
			resp: &interfaces.LLMResponse{
				Content: "done",
				Usage:   &interfaces.LLMUsage{PromptTokens: 5, CompletionTokens: 3, TotalTokens: 8},
			},
		}},
	})
	result, err := rt.ExecuteLLMStream(context.Background(), noopLog(), "agent", "msg", nil, false, "", nil)
	require.NoError(t, err)
	require.NotNil(t, result.Usage)
	require.EqualValues(t, 5, result.Usage.PromptTokens)
}

func TestExecuteLLMStream_FallbackGenerate_UnknownToolCallError(t *testing.T) {
	rt := newTestRuntime(sdkruntime.AgentExecution{
		LLM: sdkruntime.AgentLLM{Client: stubLLMClient{
			resp: &interfaces.LLMResponse{
				ToolCalls: []*interfaces.ToolCall{{ToolCallID: "1", ToolName: "ghost"}},
			},
		}},
	})
	_, err := rt.ExecuteLLMStream(context.Background(), noopLog(), "agent", "msg", nil, false, "", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown tool")
}

func TestExecuteLLMStream_Stream_UnknownToolCallError(t *testing.T) {
	s := newFixedStream(nil, &interfaces.LLMResponse{
		ToolCalls: []*interfaces.ToolCall{{ToolCallID: "1", ToolName: "ghost"}},
	})
	rt := newTestRuntime(sdkruntime.AgentExecution{
		LLM: sdkruntime.AgentLLM{Client: streamCapableLLMClient{stream: s}},
	})
	_, err := rt.ExecuteLLMStream(context.Background(), noopLog(), "agent", "msg", nil, false, "", nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown tool")
}

func TestExecuteRetrievers_EmptyDocsSkipped(t *testing.T) {
	ctrl := gomock.NewController(t)
	r := ifmocks.NewMockRetriever(ctrl)
	r.EXPECT().Name().Return("empty-kb").AnyTimes()
	r.EXPECT().Search(gomock.Any(), gomock.Any()).Return([]interfaces.Document{}, nil) // no docs

	rt := newTestRuntime(sdkruntime.AgentExecution{
		Retrievers: sdkruntime.AgentRetrievers{Retrievers: []interfaces.Retriever{r}},
	})
	got, err := rt.ExecuteRetrievers(context.Background(), noopLog(), "q")
	require.NoError(t, err)
	require.Equal(t, "", got)
}

// --- ApplyLLMSampling Reasoning field ---

func TestApplyLLMSampling_Reasoning(t *testing.T) {
	req := &interfaces.LLMRequest{}
	ApplyLLMSampling(&types.LLMSampling{
		Reasoning: &types.LLMReasoning{Effort: "medium", Enabled: true},
	}, req)
	require.NotNil(t, req.Reasoning)
	require.Equal(t, "medium", req.Reasoning.Effort)
	require.True(t, req.Reasoning.Enabled)
}
