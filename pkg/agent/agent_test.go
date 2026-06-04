package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang/mock/gomock"

	"github.com/agenticenv/agent-sdk-go/internal/eventbus"
	"github.com/agenticenv/agent-sdk-go/internal/events"
	"github.com/agenticenv/agent-sdk-go/internal/runtime"
	rtmocks "github.com/agenticenv/agent-sdk-go/internal/runtime/mocks"
	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
	"github.com/agenticenv/agent-sdk-go/pkg/observability"
)

func testAgentWithRuntime(rt runtime.Runtime) *Agent {
	return &Agent{
		agentConfig: agentConfig{
			Name:             "TestAgent",
			logger:           logger.DefaultLogger("error"),
			maxSubAgentDepth: 2,
			tracer:           observability.DefaultNoopTracer,
			metrics:          observability.DefaultNoopMetrics,
		},
		runtime: rt,
	}
}

func TestAgent_Run_ForwardsRequestAndReturnsResponse(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockRT := rtmocks.NewMockRuntime(ctrl)
	mockRT.EXPECT().Execute(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, req *runtime.ExecuteRequest) (*types.AgentRunResult, error) {
		if req.StreamingEnabled {
			t.Error("Run must set StreamingEnabled false")
		}
		if req.UserPrompt != "hello" {
			t.Errorf("UserPrompt = %q", req.UserPrompt)
		}
		name := ""
		if req.AgentSpec != nil {
			name = req.AgentSpec.Name
		}
		return &types.AgentRunResult{Content: "reply", AgentName: name, Model: "m1"}, nil
	})

	a := testAgentWithRuntime(mockRT)
	resp, err := a.Run(context.Background(), "hello", "")
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "reply" || resp.Model != "m1" || resp.AgentName != "TestAgent" {
		t.Fatalf("response = %+v", resp)
	}
}

func TestAgent_Stream_SetsStreamingEnabled(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockRT := rtmocks.NewMockRuntime(ctrl)
	var streamReq *runtime.ExecuteRequest
	mockRT.EXPECT().ExecuteStream(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, req *runtime.ExecuteRequest) (<-chan events.AgentEvent, error) {
		streamReq = req
		ch := make(chan events.AgentEvent, 2)
		evName := ""
		if req.AgentSpec != nil {
			evName = req.AgentSpec.Name
		}
		ch <- events.NewAgentRunFinishedEvent("", "", &types.AgentRunResult{AgentName: evName, Content: "done"})
		close(ch)
		var recv <-chan events.AgentEvent = ch
		return recv, nil
	})

	a := testAgentWithRuntime(mockRT)
	ch, err := a.Stream(context.Background(), "prompt", "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		for range ch {
		}
	}()
	if streamReq == nil || !streamReq.StreamingEnabled {
		t.Fatalf("Stream request = %+v", streamReq)
	}
	if streamReq.UserPrompt != "prompt" {
		t.Errorf("UserPrompt = %q", streamReq.UserPrompt)
	}
	if ch == nil {
		t.Fatal("Stream returned nil channel")
	}
	ev := <-ch
	if ev == nil {
		t.Fatal("nil event")
	}
	if ev.Type() != events.AgentEventTypeRunFinished {
		t.Fatalf("want RunFinished, got type %v", ev.Type())
	}
	fin, ok := ev.(*events.AgentRunFinishedEvent)
	if !ok || fin == nil {
		t.Fatalf("event not *AgentRunFinishedEvent: %+v", ev)
	}
	result, ok := fin.Result.(*types.AgentRunResult)
	if !ok || result == nil {
		t.Fatalf("Result not *AgentRunResult: %+v", fin.Result)
	}
	if result.Content != "done" {
		t.Fatalf("result.Content = %q", result.Content)
	}
}

func TestAgent_RunAsync_DeliversResult(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockRT := rtmocks.NewMockRuntime(ctrl)
	mockRT.EXPECT().Execute(gomock.Any(), gomock.Any()).Return(&types.AgentRunResult{Content: "mock", AgentName: "TestAgent", Model: "stub"}, nil)

	a := testAgentWithRuntime(mockRT)
	resCh, apprCh, err := a.RunAsync(context.Background(), "async", "")
	if err != nil {
		t.Fatal(err)
	}
	select {
	case r := <-resCh:
		if r.Error != nil {
			t.Fatal(r.Error)
		}
		if r.Result == nil || r.Result.Content != "mock" {
			t.Fatalf("result = %+v", r)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for RunAsync result")
	}
	for range apprCh {
		t.Fatal("unexpected approval request")
	}
}

func TestAgent_Stream_CustomStreamFn(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockRT := rtmocks.NewMockRuntime(ctrl)
	mockRT.EXPECT().ExecuteStream(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, req *runtime.ExecuteRequest) (<-chan events.AgentEvent, error) {
		ch := make(chan events.AgentEvent, 1)
		ch <- events.NewAgentTextMessageContentEvent("", "partial")
		close(ch)
		return ch, nil
	})

	a := testAgentWithRuntime(mockRT)
	ch, err := a.Stream(context.Background(), "x", "")
	if err != nil {
		t.Fatal(err)
	}
	ev := <-ch
	if ev == nil || ev.Type() != events.AgentEventTypeTextMessageContent {
		ev, ok := ev.(*events.AgentTextMessageContentEvent)
		if !ok {
			t.Fatalf("ev = %+v", ev)
		}
		if ev.Delta != "partial" {
			t.Fatalf("ev = %+v", ev)
		}
		t.Fatalf("ev = %+v", ev)
	}
}

// stubLLM is a minimal [interfaces.LLMClient] for config/runtime unit tests.
type stubLLM struct{}

func (stubLLM) Generate(ctx context.Context, req *interfaces.LLMRequest) (*interfaces.LLMResponse, error) {
	return &interfaces.LLMResponse{}, nil
}
func (stubLLM) GenerateStream(ctx context.Context, req *interfaces.LLMRequest) (interfaces.LLMStream, error) {
	return nil, errors.New("stub")
}
func (stubLLM) GetModel() string                    { return "stub" }
func (stubLLM) GetProvider() interfaces.LLMProvider { return interfaces.LLMProviderOpenAI }
func (stubLLM) IsStreamSupported() bool             { return false }

func TestCopyApprovalArgs(t *testing.T) {
	if copyApprovalArgs(nil) != nil {
		t.Error("copyApprovalArgs(nil) should return nil")
	}
	src := map[string]any{"a": 1, "b": "x"}
	dst := copyApprovalArgs(src)
	if dst == nil {
		t.Fatal("copyApprovalArgs should not return nil for non-nil input")
	}
	if dst["a"] != 1 || dst["b"] != "x" {
		t.Errorf("copyApprovalArgs = %v, want %v", dst, src)
	}
	// Ensure copy is independent (modify src, dst should be unchanged)
	src["c"] = 99
	if _, ok := dst["c"]; ok {
		t.Error("copyApprovalArgs should return a copy, not share the map")
	}
}

func TestAgent_ValidateConversationID(t *testing.T) {
	l := logger.DefaultLogger("error")
	a := &Agent{agentConfig: agentConfig{logger: l}}

	if err := a.validateConversationID(""); err != nil {
		t.Errorf("empty conversationID with no conversation: %v", err)
	}
	if err := a.validateConversationID("conv1"); err == nil {
		t.Error("non-empty conversationID with no conversation should error")
	}

	a.conversation = &mockConversation{}
	if err := a.validateConversationID(""); err == nil {
		t.Error("empty conversationID with conversation should error")
	}
	if err := a.validateConversationID("conv1"); err != nil {
		t.Errorf("valid conversationID with conversation: %v", err)
	}
}

type mockConversation struct{}

func (m *mockConversation) AddMessage(ctx context.Context, id string, msg interfaces.Message) error {
	return nil
}
func (m *mockConversation) ListMessages(ctx context.Context, id string, opts ...interfaces.ListMessagesOption) ([]interfaces.Message, error) {
	return nil, nil
}
func (m *mockConversation) Clear(ctx context.Context, id string) error { return nil }
func (m *mockConversation) IsDistributed() bool                        { return false }

// stubRuntime is a minimal Runtime implementation for tests.
type stubRuntime struct{}

func (s *stubRuntime) Execute(_ context.Context, _ *runtime.ExecuteRequest) (*types.AgentRunResult, error) {
	return nil, nil
}
func (s *stubRuntime) ExecuteStream(_ context.Context, _ *runtime.ExecuteRequest) (<-chan events.AgentEvent, error) {
	return nil, nil
}
func (s *stubRuntime) Approve(_ context.Context, _ string, _ types.ApprovalStatus) error { return nil }
func (s *stubRuntime) Close()                                                            {}

func TestBuildSubAgentSpecs_flat(t *testing.T) {
	childRT := &stubRuntime{}
	child := &Agent{agentConfig: agentConfig{Name: "Child"}, runtime: childRT}
	parent := &Agent{agentConfig: agentConfig{Name: "Parent", subAgents: []*Agent{child}}, runtime: &stubRuntime{}}

	got := parent.buildSubAgentSpecs()
	if len(got) != 1 {
		t.Fatalf("want 1 spec, got %d", len(got))
	}
	key, err := subAgentToolName(child.Name)
	if err != nil {
		t.Fatal(err)
	}
	spec := got[0]
	if spec.ToolName != key {
		t.Fatalf("ToolName = %q, want %q", spec.ToolName, key)
	}
	if spec.Name != child.Name {
		t.Fatalf("Name = %q, want %q", spec.Name, child.Name)
	}
	if spec.Runtime != childRT {
		t.Fatal("Runtime mismatch")
	}
	if spec.Children != nil {
		t.Fatalf("expected no children, got %v", spec.Children)
	}
}

func TestBuildSubAgentSpecs_nested(t *testing.T) {
	leafRT := &stubRuntime{}
	leaf := &Agent{agentConfig: agentConfig{Name: "Leaf"}, runtime: leafRT}
	midRT := &stubRuntime{}
	mid := &Agent{agentConfig: agentConfig{Name: "Mid", subAgents: []*Agent{leaf}}, runtime: midRT}
	root := &Agent{agentConfig: agentConfig{Name: "Root", subAgents: []*Agent{mid}}, runtime: &stubRuntime{}}

	got := root.buildSubAgentSpecs()
	if len(got) != 1 {
		t.Fatalf("want 1 top-level spec, got %d", len(got))
	}
	midSpec := got[0]
	if midSpec.Runtime != midRT {
		t.Fatal("mid Runtime mismatch")
	}
	if len(midSpec.Children) != 1 {
		t.Fatalf("want 1 child spec, got %d", len(midSpec.Children))
	}
	leafSpec := midSpec.Children[0]
	if leafSpec.Runtime != leafRT {
		t.Fatal("leaf Runtime mismatch")
	}
	if len(leafSpec.Children) != 0 {
		t.Fatalf("leaf should have no children, got %d", len(leafSpec.Children))
	}
}

func TestBuildSubAgentSpecs_noRuntimeStillBuilds(t *testing.T) {
	// Sub-agent with no runtime still gets a spec — runtime decides what to do with it.
	sub := &Agent{agentConfig: agentConfig{Name: "X"}}
	parent := &Agent{agentConfig: agentConfig{subAgents: []*Agent{sub}}}

	got := parent.buildSubAgentSpecs()
	if len(got) != 1 {
		t.Fatalf("want 1 spec, got %v", got)
	}
	if got[0].ToolName != "subagent_X" {
		t.Fatalf("ToolName = %q", got[0].ToolName)
	}
	if got[0].Runtime != nil {
		t.Fatalf("expected nil runtime, got %v", got[0].Runtime)
	}
}

func TestBuildAgent_DisableLocalWorkerWithStreamRequiresEnableRemoteWorkers(t *testing.T) {
	_, err := buildAgent([]Option{
		WithName("x"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		DisableLocalWorker(),
		WithStream(true),
	})
	if err == nil || !strings.Contains(err.Error(), "EnableRemoteWorkers") {
		t.Fatalf("got %v", err)
	}
}

func TestAgent_Run_RequiresApprovalHandlerWhenToolsNeedApproval(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockRT := rtmocks.NewMockRuntime(ctrl)

	a := &Agent{
		agentConfig: agentConfig{
			Name:    "A",
			logger:  logger.DefaultLogger("error"),
			tracer:  observability.DefaultNoopTracer,
			metrics: observability.DefaultNoopMetrics,
			tools: []interfaces.Tool{
				mockToolWithApproval{mockTool: mockTool{name: "need"}, needApproval: true},
			},
		},
		runtime: mockRT,
	}
	_, err := a.Run(context.Background(), "hi", "")
	if err == nil || !strings.Contains(err.Error(), "WithApprovalHandler") {
		t.Fatalf("got %v", err)
	}
}

func TestAgent_OnApproval(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	mockRT := rtmocks.NewMockRuntime(ctrl)
	mockRT.EXPECT().Approve(gomock.Any(), "tok", types.ApprovalStatusApproved).Return(nil)

	a := testAgentWithRuntime(mockRT)
	if err := a.OnApproval(context.Background(), "tok", types.ApprovalStatusApproved); err != nil {
		t.Fatal(err)
	}
}

func TestWireInMemoryEventChannelToSubAgents(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()
	bus := eventbus.NewInmem(logger.DefaultLogger("error"))

	parentRT := rtmocks.NewMockEventBusRuntime(ctrl)
	parentRT.EXPECT().SetEventBus(bus)

	childRT := rtmocks.NewMockEventBusRuntime(ctrl)
	childRT.EXPECT().SetEventBus(bus)

	child := &Agent{
		agentConfig: agentConfig{Name: "Child", taskQueue: "q-c"},
		runtime:     childRT,
	}
	parent := &Agent{
		agentConfig: agentConfig{Name: "Parent", taskQueue: "q-p", subAgents: []*Agent{child}},
		runtime:     parentRT,
	}
	wireInMemoryEventChannelToSubAgents(bus, parent)
}
