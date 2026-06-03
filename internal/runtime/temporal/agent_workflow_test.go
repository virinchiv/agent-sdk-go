package temporal

import (
	"context"
	"fmt"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"go.temporal.io/sdk/testsuite"
	"go.temporal.io/sdk/workflow"

	sdkruntime "github.com/agenticenv/agent-sdk-go/internal/runtime"
	"github.com/agenticenv/agent-sdk-go/internal/runtime/base"
	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces/mocks"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
	"github.com/agenticenv/agent-sdk-go/pkg/observability"
)

func testRuntimeForWorkflow(t *testing.T) *TemporalRuntime {
	t.Helper()
	return &TemporalRuntime{
		Runtime: base.Runtime{
			AgentSpec: sdkruntime.AgentSpec{Name: "WorkflowTestAgent"},
			AgentExecution: sdkruntime.AgentExecution{
				LLM:     sdkruntime.AgentLLM{Client: stubLLM{}},
				Limits:  sdkruntime.AgentLimits{MaxIterations: 5},
				Tools:   sdkruntime.AgentTools{Tools: nil},
				Session: sdkruntime.AgentSession{},
			},
			Tracer:  observability.DefaultNoopTracer,
			Metrics: observability.DefaultNoopMetrics,
		},
		logger: logger.NoopLogger(),
	}
}

// newActivityTestEnv returns a [testsuite.TestActivityEnvironment] for isolated activity tests.
func newActivityTestEnv(t *testing.T) *testsuite.TestActivityEnvironment {
	t.Helper()
	var suite testsuite.WorkflowTestSuite
	return suite.NewTestActivityEnvironment()
}

// streamCapableStubLLM enables the streaming branch in [AgentWorkflow] (useStreaming == true).
type streamCapableStubLLM struct{ stubLLM }

func (streamCapableStubLLM) IsStreamSupported() bool { return true }

func TestAgentWorkflow_SingleLLMNoTools(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	rt := testRuntimeForWorkflow(t)

	env.RegisterWorkflow(rt.AgentWorkflow)
	env.OnActivity(rt.AgentLLMActivity, mock.Anything, mock.Anything).Return(func(ctx context.Context, in AgentLLMInput) (*AgentLLMResult, error) {
		if in.Messages == nil {
			t.Error("expected messages")
		}
		return &AgentLLMResult{Content: "final answer", ToolCalls: nil}, nil
	})

	env.ExecuteWorkflow(rt.AgentWorkflow, AgentWorkflowInput{
		UserPrompt:       "hello",
		StreamingEnabled: false,
	})

	require.True(t, env.IsWorkflowCompleted())
	var result types.AgentRunResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "final answer", result.Content)
	require.Equal(t, "WorkflowTestAgent", result.AgentName)
	require.Equal(t, "stub", result.Model)
}

func TestAgentWorkflow_StreamingPath_UsesStreamActivity(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	rt := testRuntimeForWorkflow(t)
	rt.AgentExecution.LLM.Client = streamCapableStubLLM{}

	env.RegisterWorkflow(rt.AgentWorkflow)
	env.OnActivity(rt.AgentLLMStreamActivity, mock.Anything, mock.Anything).Return(func(ctx context.Context, in AgentLLMInput) (*AgentLLMResult, error) {
		return &AgentLLMResult{Content: "streamed", ToolCalls: nil}, nil
	})

	env.ExecuteWorkflow(rt.AgentWorkflow, AgentWorkflowInput{
		UserPrompt:       "hi",
		StreamingEnabled: true,
	})

	require.True(t, env.IsWorkflowCompleted())
	var result types.AgentRunResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "streamed", result.Content)
}

func TestAgentWorkflow_OneToolThenFinal(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	rt := testRuntimeForWorkflow(t)

	var llmCalls int
	env.RegisterWorkflow(rt.AgentWorkflow)
	env.OnActivity(rt.AgentLLMActivity, mock.Anything, mock.Anything).Return(func(ctx context.Context, in AgentLLMInput) (*AgentLLMResult, error) {
		llmCalls++
		if llmCalls == 1 {
			return &AgentLLMResult{
				Content: "using tool",
				ToolCalls: []ToolCallRequest{{
					ToolCallID:      "tc1",
					ToolName:        "echo",
					ToolDisplayName: "Echo",
					Args:            map[string]any{"x": 1},
				}},
			}, nil
		}
		return &AgentLLMResult{Content: "after tool", ToolCalls: nil}, nil
	})
	env.OnActivity(rt.AgentToolExecuteActivity, mock.Anything, mock.Anything).Return(func(ctx context.Context, in AgentToolExecuteInput) (string, error) {
		if in.ToolName != "echo" {
			t.Errorf("tool name = %q", in.ToolName)
		}
		return "echo ok", nil
	})
	env.OnActivity(rt.AgentToolAuthorizeActivity, mock.Anything, mock.Anything).Return(func(ctx context.Context, in AgentToolAuthorizeInput) (AgentToolAuthorizeResult, error) {
		return AgentToolAuthorizeResult{Allowed: true}, nil
	})

	env.ExecuteWorkflow(rt.AgentWorkflow, AgentWorkflowInput{
		UserPrompt: "run",
	})

	require.True(t, env.IsWorkflowCompleted())
	var result types.AgentRunResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "after tool", result.Content)
	require.Equal(t, 2, llmCalls)
}

func TestAgentWorkflow_ToolAuthorizationDenied_SkipsExecute(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	rt := testRuntimeForWorkflow(t)

	var llmCalls int
	env.RegisterWorkflow(rt.AgentWorkflow)
	env.OnActivity(rt.AgentLLMActivity, mock.Anything, mock.Anything).Return(func(ctx context.Context, in AgentLLMInput) (*AgentLLMResult, error) {
		llmCalls++
		if llmCalls == 1 {
			return &AgentLLMResult{
				Content: "using tool",
				ToolCalls: []ToolCallRequest{{
					ToolCallID:      "tc-auth-deny",
					ToolName:        "echo",
					ToolDisplayName: "Echo",
					Args:            map[string]any{"x": 1},
				}},
			}, nil
		}
		return &AgentLLMResult{Content: "after deny", ToolCalls: nil}, nil
	})
	env.OnActivity(rt.AgentToolAuthorizeActivity, mock.Anything, mock.Anything).Return(func(ctx context.Context, in AgentToolAuthorizeInput) (AgentToolAuthorizeResult, error) {
		return AgentToolAuthorizeResult{Allowed: false, Reason: "missing scope"}, nil
	})

	env.ExecuteWorkflow(rt.AgentWorkflow, AgentWorkflowInput{
		UserPrompt: "run",
	})

	require.True(t, env.IsWorkflowCompleted())
	var result types.AgentRunResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "after deny", result.Content)
	require.Equal(t, 2, llmCalls)
}

func TestAgentLLMActivity_MockLLM_TextOnly(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLLM := mocks.NewMockLLMClient(ctrl)
	mockLLM.EXPECT().GetModel().Return("test-model").AnyTimes()
	mockLLM.EXPECT().GetProvider().Return(interfaces.LLMProviderOpenAI).AnyTimes()
	mockLLM.EXPECT().Generate(gomock.Any(), gomock.Any()).Return(&interfaces.LLMResponse{
		Content: "final",
	}, nil)

	rt := &TemporalRuntime{
		Runtime: base.Runtime{
			AgentSpec: sdkruntime.AgentSpec{Name: "ActTest"},
			AgentExecution: sdkruntime.AgentExecution{
				LLM: sdkruntime.AgentLLM{Client: mockLLM},
			},
			Tracer:  observability.DefaultNoopTracer,
			Metrics: observability.DefaultNoopMetrics,
		},
		logger: logger.NoopLogger(),
	}

	actEnv := newActivityTestEnv(t)
	actEnv.RegisterActivity(rt.AgentLLMActivity)
	val, err := actEnv.ExecuteActivity(rt.AgentLLMActivity, AgentLLMInput{
		Messages: []interfaces.Message{{Role: interfaces.MessageRoleUser, Content: "hi"}},
	})
	require.NoError(t, err)

	var got AgentLLMResult
	require.NoError(t, val.Get(&got))
	require.Equal(t, "final", got.Content)
	require.Empty(t, got.ToolCalls)
}

func TestAgentLLMActivity_MockLLM_ToolCalls(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockTool := mocks.NewMockTool(ctrl)
	mockTool.EXPECT().Name().Return("echo").AnyTimes()
	mockTool.EXPECT().DisplayName().Return("Echo").AnyTimes()
	mockTool.EXPECT().Description().Return("d").AnyTimes()
	mockTool.EXPECT().Parameters().Return(interfaces.JSONSchema{}).AnyTimes()

	policy := mocks.NewMockAgentToolApprovalPolicy(ctrl)
	policy.EXPECT().RequiresApproval(gomock.Any()).Return(false).AnyTimes()

	mockLLM := mocks.NewMockLLMClient(ctrl)
	mockLLM.EXPECT().GetModel().Return("test-model").AnyTimes()
	mockLLM.EXPECT().GetProvider().Return(interfaces.LLMProviderOpenAI).AnyTimes()
	mockLLM.EXPECT().Generate(gomock.Any(), gomock.Any()).Return(&interfaces.LLMResponse{
		Content: "call tool",
		ToolCalls: []*interfaces.ToolCall{{
			ToolCallID: "tc1",
			ToolName:   "echo",
			Args:       map[string]any{"x": 1.0},
		}},
	}, nil)

	rt := &TemporalRuntime{
		Runtime: base.Runtime{
			AgentSpec: sdkruntime.AgentSpec{Name: "ActTest"},
			AgentExecution: sdkruntime.AgentExecution{
				LLM:   sdkruntime.AgentLLM{Client: mockLLM},
				Tools: sdkruntime.AgentTools{Tools: []interfaces.Tool{mockTool}, ApprovalPolicy: policy},
			},
			Tracer:  observability.DefaultNoopTracer,
			Metrics: observability.DefaultNoopMetrics,
		},
		logger: logger.NoopLogger(),
	}

	actEnv := newActivityTestEnv(t)
	actEnv.RegisterActivity(rt.AgentLLMActivity)
	val, err := actEnv.ExecuteActivity(rt.AgentLLMActivity, AgentLLMInput{
		Messages: []interfaces.Message{{Role: interfaces.MessageRoleUser, Content: "run"}},
	})
	require.NoError(t, err)

	var got AgentLLMResult
	require.NoError(t, val.Get(&got))
	require.Len(t, got.ToolCalls, 1)
	require.Equal(t, "echo", got.ToolCalls[0].ToolName)
	require.Equal(t, "tc1", got.ToolCalls[0].ToolCallID)
	require.False(t, got.ToolCalls[0].NeedsApproval)
}

func TestAgentLLMActivity_MockLLM_UnknownToolError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLLM := mocks.NewMockLLMClient(ctrl)
	mockLLM.EXPECT().GetModel().Return("test-model").AnyTimes()
	mockLLM.EXPECT().GetProvider().Return(interfaces.LLMProviderOpenAI).AnyTimes()
	mockLLM.EXPECT().Generate(gomock.Any(), gomock.Any()).Return(&interfaces.LLMResponse{
		Content: "x",
		ToolCalls: []*interfaces.ToolCall{{
			ToolCallID: "1",
			ToolName:   "not_registered",
			Args:       nil,
		}},
	}, nil)

	rt := &TemporalRuntime{
		Runtime: base.Runtime{
			AgentExecution: sdkruntime.AgentExecution{
				LLM:   sdkruntime.AgentLLM{Client: mockLLM},
				Tools: sdkruntime.AgentTools{Tools: []interfaces.Tool{}},
			},
			Tracer:  observability.DefaultNoopTracer,
			Metrics: observability.DefaultNoopMetrics,
		},
		logger: logger.NoopLogger(),
	}

	actEnv := newActivityTestEnv(t)
	actEnv.RegisterActivity(rt.AgentLLMActivity)
	_, err := actEnv.ExecuteActivity(rt.AgentLLMActivity, AgentLLMInput{
		Messages: []interfaces.Message{{Role: interfaces.MessageRoleUser, Content: "q"}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown tool")
}

func TestAgentLLMActivity_MockConversationAndLLM(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockConv := mocks.NewMockConversation(ctrl)
	mockConv.EXPECT().ListMessages(gomock.Any(), "conv-1", gomock.Any()).Return(
		[]interfaces.Message{{Role: interfaces.MessageRoleUser, Content: "prior"}},
		nil,
	)

	mockLLM := mocks.NewMockLLMClient(ctrl)
	mockLLM.EXPECT().GetModel().Return("test-model").AnyTimes()
	mockLLM.EXPECT().GetProvider().Return(interfaces.LLMProviderOpenAI).AnyTimes()
	mockLLM.EXPECT().Generate(gomock.Any(), gomock.Any()).Return(&interfaces.LLMResponse{Content: "answer"}, nil)

	rt := &TemporalRuntime{
		Runtime: base.Runtime{
			AgentExecution: sdkruntime.AgentExecution{
				LLM: sdkruntime.AgentLLM{Client: mockLLM},
				Session: sdkruntime.AgentSession{
					Conversation:     mockConv,
					ConversationSize: 10,
				},
			},
			Tracer:  observability.DefaultNoopTracer,
			Metrics: observability.DefaultNoopMetrics,
		},
		logger: logger.NoopLogger(),
	}

	actEnv := newActivityTestEnv(t)
	actEnv.RegisterActivity(rt.AgentLLMActivity)
	val, err := actEnv.ExecuteActivity(rt.AgentLLMActivity, AgentLLMInput{
		ConversationID: "conv-1",
		Messages:       []interfaces.Message{{Role: interfaces.MessageRoleUser, Content: "new"}},
	})
	require.NoError(t, err)

	var got AgentLLMResult
	require.NoError(t, val.Get(&got))
	require.Equal(t, "answer", got.Content)
}

func TestAgentLLMActivity_ConversationNotConfigured(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLLM := mocks.NewMockLLMClient(ctrl)

	rt := &TemporalRuntime{
		Runtime: base.Runtime{
			AgentExecution: sdkruntime.AgentExecution{
				LLM:     sdkruntime.AgentLLM{Client: mockLLM},
				Session: sdkruntime.AgentSession{Conversation: nil},
			},
			Tracer:  observability.DefaultNoopTracer,
			Metrics: observability.DefaultNoopMetrics,
		},
		logger: logger.NoopLogger(),
	}

	actEnv := newActivityTestEnv(t)
	actEnv.RegisterActivity(rt.AgentLLMActivity)
	_, err := actEnv.ExecuteActivity(rt.AgentLLMActivity, AgentLLMInput{
		ConversationID: "any",
		Messages:       []interfaces.Message{{Role: interfaces.MessageRoleUser, Content: "x"}},
	})
	require.Error(t, err)
	require.Contains(t, err.Error(), "conversation is not configured")
}

func TestAgentLLMStreamActivity_MockLLM_FallbackToGenerate(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockLLM := mocks.NewMockLLMClient(ctrl)
	mockLLM.EXPECT().GetModel().Return("test-model").AnyTimes()
	mockLLM.EXPECT().GetProvider().Return(interfaces.LLMProviderOpenAI).AnyTimes()
	mockLLM.EXPECT().IsStreamSupported().Return(false)
	mockLLM.EXPECT().Generate(gomock.Any(), gomock.Any()).Return(&interfaces.LLMResponse{Content: "gen"}, nil)

	rt := &TemporalRuntime{
		Runtime: base.Runtime{
			AgentSpec: sdkruntime.AgentSpec{Name: "StreamAct"},
			AgentExecution: sdkruntime.AgentExecution{
				LLM: sdkruntime.AgentLLM{Client: mockLLM},
			},
			Tracer:  observability.DefaultNoopTracer,
			Metrics: observability.DefaultNoopMetrics,
		},
		logger: logger.NoopLogger(),
	}

	actEnv := newActivityTestEnv(t)
	actEnv.RegisterActivity(rt.AgentLLMStreamActivity)
	val, err := actEnv.ExecuteActivity(rt.AgentLLMStreamActivity, AgentLLMInput{
		AgentName:        "StreamAct",
		Messages:         []interfaces.Message{{Role: interfaces.MessageRoleUser, Content: "s"}},
		LocalChannelName: "ch",
	})
	require.NoError(t, err)

	var got AgentLLMResult
	require.NoError(t, val.Get(&got))
	require.Equal(t, "gen", got.Content)
}

// History CAN runs after a tool round (see agent_workflow.go); no-tool LLM completion exits the loop without that check.
func TestAgentWorkflow_ContinueAsNewOnHistoryLengthAfterTools(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	rt := testRuntimeForWorkflow(t)

	env.SetCurrentHistoryLength(agentWorkflowHistoryLength)

	env.RegisterWorkflow(rt.AgentWorkflow)
	env.OnActivity(rt.AgentLLMActivity, mock.Anything, mock.Anything).Return(func(ctx context.Context, in AgentLLMInput) (*AgentLLMResult, error) {
		return &AgentLLMResult{
			Content: "using tool",
			ToolCalls: []ToolCallRequest{{
				ToolCallID:      "tc-can",
				ToolName:        "echo",
				ToolDisplayName: "Echo",
				Args:            map[string]any{"x": 1},
			}},
		}, nil
	})
	env.OnActivity(rt.AgentToolExecuteActivity, mock.Anything, mock.Anything).Return("echo ok", nil)
	env.OnActivity(rt.AgentToolAuthorizeActivity, mock.Anything, mock.Anything).Return(func(ctx context.Context, in AgentToolAuthorizeInput) (AgentToolAuthorizeResult, error) {
		return AgentToolAuthorizeResult{Allowed: true}, nil
	})

	env.ExecuteWorkflow(rt.AgentWorkflow, AgentWorkflowInput{
		UserPrompt: "run",
	})

	require.True(t, env.IsWorkflowCompleted())
	wfErr := env.GetWorkflowError()
	require.Error(t, wfErr)
	require.True(t, workflow.IsContinueAsNewError(wfErr), "expected continue-as-new, got: %v", wfErr)
}

func TestAgentWorkflow_ContinueAsNewOnHistorySizeAfterTools(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	rt := testRuntimeForWorkflow(t)

	env.SetCurrentHistoryLength(1)
	env.SetCurrentHistorySize(agentWorkflowHistorySizeBytes + 1)

	env.RegisterWorkflow(rt.AgentWorkflow)
	env.OnActivity(rt.AgentLLMActivity, mock.Anything, mock.Anything).Return(func(ctx context.Context, in AgentLLMInput) (*AgentLLMResult, error) {
		return &AgentLLMResult{
			Content: "using tool",
			ToolCalls: []ToolCallRequest{{
				ToolCallID:      "tc-can-size",
				ToolName:        "echo",
				ToolDisplayName: "Echo",
				Args:            map[string]any{"x": 1},
			}},
		}, nil
	})
	env.OnActivity(rt.AgentToolExecuteActivity, mock.Anything, mock.Anything).Return("echo ok", nil)
	env.OnActivity(rt.AgentToolAuthorizeActivity, mock.Anything, mock.Anything).Return(func(ctx context.Context, in AgentToolAuthorizeInput) (AgentToolAuthorizeResult, error) {
		return AgentToolAuthorizeResult{Allowed: true}, nil
	})

	env.ExecuteWorkflow(rt.AgentWorkflow, AgentWorkflowInput{
		UserPrompt: "run",
	})

	require.True(t, env.IsWorkflowCompleted())
	wfErr := env.GetWorkflowError()
	require.Error(t, wfErr)
	require.True(t, workflow.IsContinueAsNewError(wfErr), "expected continue-as-new, got: %v", wfErr)
}

// ---------------------------------------------------------------------------
// AgentRetrieverActivity tests
// ---------------------------------------------------------------------------

func makeRetrieverRuntime(t *testing.T, retrievers []interfaces.Retriever, mode types.RetrieverMode) *TemporalRuntime {
	t.Helper()
	mockLLM := mocks.NewMockLLMClient(gomock.NewController(t))
	mockLLM.EXPECT().GetModel().Return("test-model").AnyTimes()
	mockLLM.EXPECT().GetProvider().Return(interfaces.LLMProviderOpenAI).AnyTimes()
	return &TemporalRuntime{
		Runtime: base.Runtime{
			AgentSpec: sdkruntime.AgentSpec{Name: "RetrieverTest"},
			AgentExecution: sdkruntime.AgentExecution{
				LLM: sdkruntime.AgentLLM{Client: mockLLM},
				Retrievers: sdkruntime.AgentRetrievers{
					Retrievers: retrievers,
					Mode:       mode,
				},
			},
			Tracer:  observability.DefaultNoopTracer,
			Metrics: observability.DefaultNoopMetrics,
		},
		logger: logger.NoopLogger(),
	}
}

func TestAgentRetrieverActivity_NoRetrievers(t *testing.T) {
	rt := makeRetrieverRuntime(t, nil, types.RetrieverModePrefetch)
	actEnv := newActivityTestEnv(t)
	actEnv.RegisterActivity(rt.AgentRetrieverActivity)

	val, err := actEnv.ExecuteActivity(rt.AgentRetrieverActivity, AgentRetrieverInput{UserPrompt: "test"})
	require.NoError(t, err)

	var got AgentRetrieverResult
	require.NoError(t, val.Get(&got))
	require.Empty(t, got.RetrieverContext)
}

func TestAgentRetrieverActivity_SingleRetriever(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockR := mocks.NewMockRetriever(ctrl)
	mockR.EXPECT().Name().Return("kb").AnyTimes()
	mockR.EXPECT().Search(gomock.Any(), "what is Go?").Return([]interfaces.Document{
		{Content: "Go is a language", Source: "docs.go.dev", Score: 0.95},
	}, nil)

	rt := makeRetrieverRuntime(t, []interfaces.Retriever{mockR}, types.RetrieverModePrefetch)
	actEnv := newActivityTestEnv(t)
	actEnv.RegisterActivity(rt.AgentRetrieverActivity)

	val, err := actEnv.ExecuteActivity(rt.AgentRetrieverActivity, AgentRetrieverInput{UserPrompt: "what is Go?"})
	require.NoError(t, err)

	var got AgentRetrieverResult
	require.NoError(t, val.Get(&got))
	require.Contains(t, got.RetrieverContext, "Go is a language")
	require.Contains(t, got.RetrieverContext, "docs.go.dev")
	require.Contains(t, got.RetrieverContext, "0.95")
	// Single retriever: no section header
	require.NotContains(t, got.RetrieverContext, "## kb")
}

func TestAgentRetrieverActivity_MultipleRetrievers_SectionHeaders(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockR1 := mocks.NewMockRetriever(ctrl)
	mockR1.EXPECT().Name().Return("r1").AnyTimes()
	mockR1.EXPECT().Search(gomock.Any(), "q").Return([]interfaces.Document{
		{Content: "doc from r1", Source: "s1", Score: 0.9},
	}, nil)

	mockR2 := mocks.NewMockRetriever(ctrl)
	mockR2.EXPECT().Name().Return("r2").AnyTimes()
	mockR2.EXPECT().Search(gomock.Any(), "q").Return([]interfaces.Document{
		{Content: "doc from r2", Source: "s2", Score: 0.8},
	}, nil)

	rt := makeRetrieverRuntime(t, []interfaces.Retriever{mockR1, mockR2}, types.RetrieverModeHybrid)
	actEnv := newActivityTestEnv(t)
	actEnv.RegisterActivity(rt.AgentRetrieverActivity)

	val, err := actEnv.ExecuteActivity(rt.AgentRetrieverActivity, AgentRetrieverInput{UserPrompt: "q"})
	require.NoError(t, err)

	var got AgentRetrieverResult
	require.NoError(t, val.Get(&got))
	require.Contains(t, got.RetrieverContext, "## r1")
	require.Contains(t, got.RetrieverContext, "doc from r1")
	require.Contains(t, got.RetrieverContext, "## r2")
	require.Contains(t, got.RetrieverContext, "doc from r2")
}

func TestAgentRetrieverActivity_PartialFailure_ContinuesWithPartialContext(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockOK := mocks.NewMockRetriever(ctrl)
	mockOK.EXPECT().Name().Return("ok").AnyTimes()
	mockOK.EXPECT().Search(gomock.Any(), "q").Return([]interfaces.Document{
		{Content: "good doc", Source: "src", Score: 0.88},
	}, nil)

	mockFail := mocks.NewMockRetriever(ctrl)
	mockFail.EXPECT().Name().Return("bad").AnyTimes()
	mockFail.EXPECT().Search(gomock.Any(), "q").Return(nil, fmt.Errorf("connection refused"))

	rt := makeRetrieverRuntime(t, []interfaces.Retriever{mockOK, mockFail}, types.RetrieverModePrefetch)
	actEnv := newActivityTestEnv(t)
	actEnv.RegisterActivity(rt.AgentRetrieverActivity)

	val, err := actEnv.ExecuteActivity(rt.AgentRetrieverActivity, AgentRetrieverInput{UserPrompt: "q"})
	require.NoError(t, err)

	var got AgentRetrieverResult
	require.NoError(t, val.Get(&got))
	require.Contains(t, got.RetrieverContext, "good doc")
	require.NotContains(t, got.RetrieverContext, "bad")
}

func TestAgentRetrieverActivity_AllFail_ReturnsError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockFail := mocks.NewMockRetriever(ctrl)
	mockFail.EXPECT().Name().Return("bad").AnyTimes()
	mockFail.EXPECT().Search(gomock.Any(), "q").Return(nil, fmt.Errorf("timeout"))

	rt := makeRetrieverRuntime(t, []interfaces.Retriever{mockFail}, types.RetrieverModePrefetch)
	actEnv := newActivityTestEnv(t)
	actEnv.RegisterActivity(rt.AgentRetrieverActivity)

	_, err := actEnv.ExecuteActivity(rt.AgentRetrieverActivity, AgentRetrieverInput{UserPrompt: "q"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "all")
}

func TestAgentRetrieverActivity_EmptyDocs_EmptyContext(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockR := mocks.NewMockRetriever(ctrl)
	mockR.EXPECT().Name().Return("kb").AnyTimes()
	mockR.EXPECT().Search(gomock.Any(), "q").Return(nil, nil)

	rt := makeRetrieverRuntime(t, []interfaces.Retriever{mockR}, types.RetrieverModePrefetch)
	actEnv := newActivityTestEnv(t)
	actEnv.RegisterActivity(rt.AgentRetrieverActivity)

	val, err := actEnv.ExecuteActivity(rt.AgentRetrieverActivity, AgentRetrieverInput{UserPrompt: "q"})
	require.NoError(t, err)

	var got AgentRetrieverResult
	require.NoError(t, val.Get(&got))
	require.Empty(t, got.RetrieverContext)
}

// ---------------------------------------------------------------------------
// buildLLMRequest RAG context tests
// ---------------------------------------------------------------------------

// ---------------------------------------------------------------------------
// AgentWorkflow + prefetch mode integration
// ---------------------------------------------------------------------------

func TestAgentWorkflow_PrefetchMode_CallsRetrieverActivityFirst(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockR := mocks.NewMockRetriever(ctrl)
	mockR.EXPECT().Name().Return("kb").AnyTimes()

	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	rt := &TemporalRuntime{
		Runtime: base.Runtime{
			AgentSpec: sdkruntime.AgentSpec{Name: "PrefetchAgent", SystemPrompt: "base prompt"},
			AgentExecution: sdkruntime.AgentExecution{
				LLM:    sdkruntime.AgentLLM{Client: stubLLM{}},
				Limits: sdkruntime.AgentLimits{MaxIterations: 5},
				Retrievers: sdkruntime.AgentRetrievers{
					Retrievers: []interfaces.Retriever{mockR},
					Mode:       types.RetrieverModePrefetch,
				},
			},
			Tracer:  observability.DefaultNoopTracer,
			Metrics: observability.DefaultNoopMetrics,
		},
		logger: logger.NoopLogger(),
	}

	env.RegisterWorkflow(rt.AgentWorkflow)

	retrieverCalled := false
	env.OnActivity(rt.AgentRetrieverActivity, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, in AgentRetrieverInput) (*AgentRetrieverResult, error) {
			retrieverCalled = true
			require.Equal(t, "user query", in.UserPrompt)
			return &AgentRetrieverResult{RetrieverContext: "[1] prefetched doc"}, nil
		})

	env.OnActivity(rt.AgentLLMActivity, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, in AgentLLMInput) (*AgentLLMResult, error) {
			require.Contains(t, in.RetrieverContext, "prefetched doc")
			return &AgentLLMResult{Content: "answer"}, nil
		})

	env.ExecuteWorkflow(rt.AgentWorkflow, AgentWorkflowInput{UserPrompt: "user query"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
	require.True(t, retrieverCalled, "AgentRetrieverActivity must have been called")

	var result types.AgentRunResult
	require.NoError(t, env.GetWorkflowResult(&result))
	require.Equal(t, "answer", result.Content)
}

func TestAgentWorkflow_AgenticMode_SkipsRetrieverActivity(t *testing.T) {
	var suite testsuite.WorkflowTestSuite
	env := suite.NewTestWorkflowEnvironment()
	rt := testRuntimeForWorkflow(t)

	env.RegisterWorkflow(rt.AgentWorkflow)
	env.OnActivity(rt.AgentLLMActivity, mock.Anything, mock.Anything).Return(&AgentLLMResult{Content: "done"}, nil)

	// AgentRetrieverActivity must NOT be called when mode is agentic (default / empty)
	env.OnActivity(rt.AgentRetrieverActivity, mock.Anything, mock.Anything).Return(
		func(ctx context.Context, in AgentRetrieverInput) (*AgentRetrieverResult, error) {
			t.Error("AgentRetrieverActivity must not be called in agentic mode")
			return &AgentRetrieverResult{}, nil
		})

	env.ExecuteWorkflow(rt.AgentWorkflow, AgentWorkflowInput{UserPrompt: "hi"})

	require.True(t, env.IsWorkflowCompleted())
	require.NoError(t, env.GetWorkflowError())
}
