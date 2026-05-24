package agent

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
	mcpclient "github.com/agenticenv/agent-sdk-go/pkg/mcp/client"
	"github.com/agenticenv/agent-sdk-go/pkg/observability"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestBuildAgentConfig_NeitherTemporalConfigNorClient(t *testing.T) {
	_, err := buildAgentConfig([]Option{
		WithName("test"),
		WithLLMClient(stubLLM{}),
	})
	if err == nil || !strings.Contains(err.Error(), "temporal connection is required") {
		t.Fatalf("got %v", err)
	}
}

func TestBuildAgentConfig_DefaultNoopTracerMetrics(t *testing.T) {
	cfg, err := buildAgentConfig([]Option{
		WithName("noop-obs"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.tracer.(*observability.NoopTracer); !ok {
		t.Fatalf("without observability wiring, tracer should be *observability.NoopTracer, got %T", cfg.tracer)
	}
	if _, ok := cfg.metrics.(*observability.NoopMetrics); !ok {
		t.Fatalf("without observability wiring, metrics should be *observability.NoopMetrics, got %T", cfg.metrics)
	}
	if _, ok := cfg.logs.(*observability.NoopLogs); !ok {
		t.Fatalf("without observability wiring, logs should be *observability.NoopLogs, got %T", cfg.logs)
	}
}

func TestBuildAgentConfig_EmptyTaskQueue(t *testing.T) {
	_, err := buildAgentConfig([]Option{
		WithName("test"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: ""}),
		WithLLMClient(stubLLM{}),
	})
	if err == nil || !strings.Contains(err.Error(), "TaskQueue") {
		t.Fatalf("got %v", err)
	}
}

func TestNewMCPTool(t *testing.T) {
	tool := NewMCPTool("srv", interfaces.ToolSpec{Name: "echo", Description: "d", Parameters: nil}, nil)
	if tool.Name() != "mcp_srv_echo" || tool.Description() != "d" {
		t.Fatal()
	}
	p := tool.Parameters()
	if p["type"] != "object" {
		t.Fatalf("%v", p)
	}
}

func TestValidateMCPClients(t *testing.T) {
	t.Run("duplicate_name", func(t *testing.T) {
		noop := types.MCPStdio{Command: "go", Args: []string{"version"}}
		cl1, err := mcpclient.NewClient("a", noop)
		if err != nil {
			t.Fatalf("new client: %v", err)
		}
		cl2, err := mcpclient.NewClient("a", noop)
		if err != nil {
			t.Fatalf("new client: %v", err)
		}
		err = validateMCPClients([]interfaces.MCPClient{cl1, cl2})
		if err == nil || !strings.Contains(err.Error(), "duplicate mcp client name") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("nil", func(t *testing.T) {
		err := validateMCPClients([]interfaces.MCPClient{nil})
		if err == nil || !strings.Contains(err.Error(), "nil") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestBuildAgentConfig_WithMCP(t *testing.T) {
	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	srv := mcp.NewServer(&mcp.Implementation{Name: "test-mcp", Version: "v0.0.1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "keep", Description: "k", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{}, map[string]any{"ok": true}, nil
	})
	mcp.AddTool(srv, &mcp.Tool{Name: "drop", Description: "d", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{}, map[string]any{"ok": true}, nil
	})
	srvSess, err := srv.Connect(ctx, t1, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srvSess.Close() }()

	cfg, err := buildAgentConfig([]Option{
		WithName("test"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithMCPConfig(MCPServers{"srv": MCPConfig{
			Transport:  types.MCPLoopback{Transport: t2},
			ToolFilter: types.MCPToolFilter{AllowTools: []string{"keep"}},
		}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.mcpTools) != 1 || cfg.mcpTools[0].Name() != "mcp_srv_keep" {
		t.Fatalf("mcpTools = %v", cfg.mcpTools)
	}
}

func TestBuildAgentConfig_MCPClients_toolFilter(t *testing.T) {
	ctx := context.Background()
	t1, t2 := mcp.NewInMemoryTransports()
	srv := mcp.NewServer(&mcp.Implementation{Name: "test-mcp", Version: "v0.0.1"}, nil)
	mcp.AddTool(srv, &mcp.Tool{Name: "keep", Description: "k", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{}, map[string]any{"ok": true}, nil
	})
	mcp.AddTool(srv, &mcp.Tool{Name: "drop", Description: "d", InputSchema: map[string]any{"type": "object"}}, func(_ context.Context, _ *mcp.CallToolRequest, _ any) (*mcp.CallToolResult, any, error) {
		return &mcp.CallToolResult{}, map[string]any{"ok": true}, nil
	})
	srvSess, err := srv.Connect(ctx, t1, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = srvSess.Close() }()

	cl, err := mcpclient.NewClient("s", types.MCPLoopback{Transport: t2},
		mcpclient.WithToolFilter(types.MCPToolFilter{AllowTools: []string{"keep"}}))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := buildAgentConfig([]Option{
		WithName("test"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithMCPClients(cl),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.mcpTools) != 1 || cfg.mcpTools[0].Name() != "mcp_s_keep" {
		t.Fatalf("mcpTools = %v", cfg.mcpTools)
	}
}

func TestBuildAgentConfig_MCP_duplicateClientName(t *testing.T) {
	cl, cerr := mcpclient.NewClient("dup", types.MCPStdio{Command: "go", Args: []string{"version"}})
	if cerr != nil {
		t.Fatalf("new client: %v", cerr)
	}
	_, err := buildAgentConfig([]Option{
		WithName("test"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithMCPConfig(MCPServers{"dup": MCPConfig{
			Transport: types.MCPStdio{Command: "go", Args: []string{"env"}},
		}}),
		WithMCPClients(cl),
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate mcp client name") {
		t.Fatalf("got %v", err)
	}
}

func TestAgentConfig_ToolsList(t *testing.T) {
	tool := mockTool{name: "t1"}
	c := &agentConfig{tools: []interfaces.Tool{tool}}
	list := c.toolsList()
	if len(list) != 1 || list[0].Name() != "t1" {
		t.Errorf("toolsList = %v, want [t1]", list)
	}

	reg := &mockRegistry{tools: []interfaces.Tool{tool, mockTool{name: "t2"}}}
	c2 := &agentConfig{toolRegistry: reg}
	list2 := c2.toolsList()
	if len(list2) != 2 {
		t.Errorf("toolsList with registry = %v, want 2 tools", list2)
	}
}

func TestAgentConfig_ResponseFormatForLLM(t *testing.T) {
	c := &agentConfig{}
	rf := c.responseFormatForLLM()
	if rf.Type != interfaces.ResponseFormatText {
		t.Errorf("default responseFormat = %v, want text", rf.Type)
	}

	c.responseFormat = &interfaces.ResponseFormat{Type: interfaces.ResponseFormatJSON}
	rf = c.responseFormatForLLM()
	if rf.Type != interfaces.ResponseFormatJSON {
		t.Errorf("with override = %v, want json", rf.Type)
	}
}

func TestAgentConfig_ApplySamplingToRequest(t *testing.T) {
	req := &interfaces.LLMRequest{}
	c := &agentConfig{}
	c.applySamplingToRequest(req)
	if req.Temperature != nil || req.MaxTokens != 0 {
		t.Error("nil llmSampling should not modify request")
	}

	temp := 0.5
	c.llmSampling = &LLMSampling{Temperature: &temp, MaxTokens: 100}
	c.applySamplingToRequest(req)
	if req.Temperature == nil || *req.Temperature != 0.5 {
		t.Errorf("Temperature = %v, want 0.5", req.Temperature)
	}
	if req.MaxTokens != 100 {
		t.Errorf("MaxTokens = %d, want 100", req.MaxTokens)
	}
}

func TestAgentConfig_RequiresApproval(t *testing.T) {
	approvalTool := mockToolWithApproval{mockTool: mockTool{name: "a"}, needApproval: true}
	noApprovalTool := mockToolWithApproval{mockTool: mockTool{name: "b"}, needApproval: false}

	// No policy: use tool's ApprovalRequired
	c := &agentConfig{}
	if !c.requiresApproval(approvalTool) {
		t.Error("requiresApproval with no policy: approval tool should require approval")
	}
	if c.requiresApproval(noApprovalTool) {
		t.Error("requiresApproval with no policy: non-approval tool should not require approval")
	}

	// With RequireAllToolApprovalPolicy
	c.toolApprovalPolicy = RequireAllToolApprovalPolicy{}
	if !c.requiresApproval(noApprovalTool) {
		t.Error("RequireAllToolApprovalPolicy: all tools should require approval")
	}

	// With AutoToolApprovalPolicy
	c.toolApprovalPolicy = AutoToolApprovalPolicy()
	if c.requiresApproval(approvalTool) {
		t.Error("AutoToolApprovalPolicy: no tool should require approval")
	}
}

func TestAgentConfig_buildSubAgentTools_duplicateRootSubs(t *testing.T) {
	s := &Agent{agentConfig: agentConfig{Name: "Same"}}
	c := &agentConfig{subAgents: []*Agent{s, s}, maxSubAgentDepth: 3}
	err := c.buildSubAgentTools()
	if err == nil || !strings.Contains(err.Error(), "duplicate") {
		t.Fatalf("want duplicate error, got %v", err)
	}
}

func TestAgentConfig_buildSubAgentTools_duplicateDerivedToolName(t *testing.T) {
	a := &Agent{agentConfig: agentConfig{Name: "Dup"}}
	b := &Agent{agentConfig: agentConfig{Name: "Dup"}}
	c := &agentConfig{subAgents: []*Agent{a, b}, maxSubAgentDepth: 3}
	err := c.buildSubAgentTools()
	if err == nil || !strings.Contains(err.Error(), "duplicate sub-agent tool name") {
		t.Fatalf("want duplicate sub-agent tool name error, got %v", err)
	}
}

func TestAgentConfig_buildSubAgentTools_nilSubAgent(t *testing.T) {
	c := &agentConfig{subAgents: []*Agent{nil}, maxSubAgentDepth: 3}
	err := c.buildSubAgentTools()
	if err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("want nil sub-agent error, got %v", err)
	}
}

func TestAgentConfig_buildSubAgentTools_invalidSubAgentName(t *testing.T) {
	emptyName := &Agent{agentConfig: agentConfig{Name: "", ID: "id-only"}}
	c := &agentConfig{subAgents: []*Agent{emptyName}, maxSubAgentDepth: 3}
	if err := c.buildSubAgentTools(); err == nil {
		t.Fatal("expected error for empty sub-agent name")
	}
	symbolsOnly := &Agent{agentConfig: agentConfig{Name: "@@@"}}
	c2 := &agentConfig{subAgents: []*Agent{symbolsOnly}, maxSubAgentDepth: 3}
	if err := c2.buildSubAgentTools(); err == nil {
		t.Fatal("expected error for sub-agent name with no alphanumeric characters")
	}
}

func TestAgentConfig_buildSubAgentTools_cycleAB(t *testing.T) {
	a := &Agent{agentConfig: agentConfig{Name: "A"}}
	b := &Agent{agentConfig: agentConfig{Name: "B"}}
	a.subAgents = []*Agent{b}
	b.subAgents = []*Agent{a}
	c := &agentConfig{subAgents: []*Agent{a}, maxSubAgentDepth: 5}
	err := c.buildSubAgentTools()
	if err == nil || !strings.Contains(err.Error(), "cycle") {
		t.Fatalf("want cycle error, got %v", err)
	}
}

func TestAgentConfig_buildSubAgentTools_depthExceeded(t *testing.T) {
	d1 := &Agent{agentConfig: agentConfig{Name: "d1"}}
	d2 := &Agent{agentConfig: agentConfig{Name: "d2"}}
	d3 := &Agent{agentConfig: agentConfig{Name: "d3"}}
	d4 := &Agent{agentConfig: agentConfig{Name: "d4"}}
	d1.subAgents = []*Agent{d2}
	d2.subAgents = []*Agent{d3}
	d3.subAgents = []*Agent{d4}
	c := &agentConfig{subAgents: []*Agent{d1}, maxSubAgentDepth: 3}
	err := c.buildSubAgentTools()
	if err == nil || !strings.Contains(err.Error(), "depth") {
		t.Fatalf("want depth error, got %v", err)
	}
}

func TestAgentConfig_buildSubAgentTools_okWithinDepth(t *testing.T) {
	d1 := &Agent{agentConfig: agentConfig{Name: "d1"}}
	d2 := &Agent{agentConfig: agentConfig{Name: "d2"}}
	d3 := &Agent{agentConfig: agentConfig{Name: "d3"}}
	d1.subAgents = []*Agent{d2}
	d2.subAgents = []*Agent{d3}
	c := &agentConfig{subAgents: []*Agent{d1}, maxSubAgentDepth: 3}
	if err := c.buildSubAgentTools(); err != nil {
		t.Fatal(err)
	}
}

func TestAgentConfig_validateToolNames_conflict(t *testing.T) {
	sub := &Agent{agentConfig: agentConfig{Name: "Math"}}
	c := &agentConfig{
		tools:     []interfaces.Tool{mockTool{name: "subagent_Math"}},
		subAgents: []*Agent{sub},
	}
	if err := c.buildSubAgentTools(); err != nil {
		t.Fatal(err)
	}
	err := c.validateToolNames()
	if err == nil || (!strings.Contains(err.Error(), "duplicate tool name") && !strings.Contains(err.Error(), "conflicts")) {
		t.Fatalf("want duplicate / conflict error, got %v", err)
	}
}

func TestAgentConfig_toolsList_includesSubAgents(t *testing.T) {
	sub := &Agent{agentConfig: agentConfig{Name: "Helper", ID: "id-sub"}}
	c := &agentConfig{
		tools:     []interfaces.Tool{mockTool{name: "echo"}},
		subAgents: []*Agent{sub},
	}
	if err := c.buildSubAgentTools(); err != nil {
		t.Fatal(err)
	}
	list := c.toolsList()
	if len(list) != 2 {
		t.Fatalf("toolsList len = %d, want 2", len(list))
	}
	if list[0].Name() != "echo" {
		t.Errorf("first tool = %s", list[0].Name())
	}
	if list[1].Name() != "subagent_Helper" {
		t.Errorf("sub tool name = %s", list[1].Name())
	}
	at, ok := list[1].(AgentTool)
	if !ok || at.SubAgent() != sub {
		t.Errorf("second tool should be AgentTool wrapping sub")
	}
}

func TestAgentConfig_HasApprovalTools(t *testing.T) {
	c := &agentConfig{
		tools:              []interfaces.Tool{mockToolWithApproval{mockTool: mockTool{name: "x"}, needApproval: true}},
		toolApprovalPolicy: RequireAllToolApprovalPolicy{},
	}
	if !c.hasApprovalTools() {
		t.Error("hasApprovalTools should be true when tools require approval")
	}

	c2 := &agentConfig{
		tools:              []interfaces.Tool{mockToolWithApproval{mockTool: mockTool{name: "x"}, needApproval: false}},
		toolApprovalPolicy: AutoToolApprovalPolicy(),
	}
	if c2.hasApprovalTools() {
		t.Error("hasApprovalTools should be false when no tool requires approval")
	}
}

type mockRegistry struct {
	tools []interfaces.Tool
}

func (m *mockRegistry) Register(interfaces.Tool) {}
func (m *mockRegistry) Get(name string) (interfaces.Tool, bool) {
	for _, t := range m.tools {
		if t.Name() == name {
			return t, true
		}
	}
	return nil, false
}
func (m *mockRegistry) Tools() []interfaces.Tool { return m.tools }

type mockToolWithApproval struct {
	mockTool
	needApproval bool
}

func (m mockToolWithApproval) ApprovalRequired() bool { return m.needApproval }

// Ensure mockToolWithApproval implements both interfaces
var _ interfaces.Tool = (*mockToolWithApproval)(nil)
var _ interfaces.ToolApproval = (*mockToolWithApproval)(nil)

// ---------------------------------------------------------------------------
// A2A test helpers
// ---------------------------------------------------------------------------

// newTestA2ACardServer starts an httptest server that serves a minimal agent card at the
// well-known path. It registers t.Cleanup to stop the server automatically.
func newTestA2ACardServer(t *testing.T, skills []a2a.AgentSkill) string {
	t.Helper()
	card := &a2a.AgentCard{
		Name:    "Test Agent",
		Version: "1.0",
		Skills:  skills,
	}
	mux := http.NewServeMux()
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// stubA2AClient is a minimal [interfaces.A2AClient] for config/runtime unit tests.
type stubA2AClient struct {
	name   string
	skills []interfaces.A2ASkillSpec
}

func (s *stubA2AClient) Name() string { return s.name }
func (s *stubA2AClient) Ping(_ context.Context) error {
	return nil
}
func (s *stubA2AClient) ResolveCard(_ context.Context) (interfaces.A2AAgentCard, error) {
	return interfaces.A2AAgentCard{Name: s.name}, nil
}
func (s *stubA2AClient) ListSkills(_ context.Context) ([]interfaces.A2ASkillSpec, error) {
	return s.skills, nil
}
func (s *stubA2AClient) SendMessage(_ context.Context, _ interfaces.A2ASendMessageRequest) (interfaces.A2ASendMessageResult, error) {
	return interfaces.A2ASendMessageResult{}, nil
}
func (s *stubA2AClient) Close() error { return nil }

var _ interfaces.A2AClient = (*stubA2AClient)(nil)

type stubRetriever struct{}

func (stubRetriever) Name() string { return "stub" }

func (stubRetriever) Search(context.Context, string) ([]interfaces.Document, error) {
	return nil, nil
}

var _ interfaces.Retriever = stubRetriever{}

type namedStubRetriever string

func (n namedStubRetriever) Name() string { return string(n) }

func (namedStubRetriever) Search(context.Context, string) ([]interfaces.Document, error) {
	return nil, nil
}

var _ interfaces.Retriever = namedStubRetriever("")

// ---------------------------------------------------------------------------
// Retriever config tests
// ---------------------------------------------------------------------------

func TestValidateRetrieverMode(t *testing.T) {
	t.Run("default", func(t *testing.T) {
		mode, err := validateRetrieverMode("")
		if err != nil || mode != RetrieverModeAgentic {
			t.Fatalf("mode=%q err=%v", mode, err)
		}
	})
	t.Run("valid", func(t *testing.T) {
		for _, want := range []RetrieverMode{
			RetrieverModeAgentic,
			RetrieverModePrefetch,
			RetrieverModeHybrid,
		} {
			mode, err := validateRetrieverMode(want)
			if err != nil || mode != want {
				t.Fatalf("want %q got %q err=%v", want, mode, err)
			}
		}
	})
	t.Run("invalid", func(t *testing.T) {
		_, err := validateRetrieverMode(RetrieverMode("bogus"))
		if err == nil || !strings.Contains(err.Error(), "invalid retriever mode") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestValidateRetrievers(t *testing.T) {
	t.Run("nil", func(t *testing.T) {
		err := validateRetrievers([]interfaces.Retriever{nil})
		if err == nil || !strings.Contains(err.Error(), "nil") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("ok", func(t *testing.T) {
		if err := validateRetrievers([]interfaces.Retriever{stubRetriever{}, stubRetriever{}}); err != nil {
			t.Fatalf("got %v", err)
		}
	})
}

func TestBuildRetrieverTools(t *testing.T) {
	t.Run("agentic_builds_tools", func(t *testing.T) {
		c := &agentConfig{
			retrieverMode: RetrieverModeAgentic,
			retrievers:    []interfaces.Retriever{namedStubRetriever("kb")},
		}
		if err := c.buildRetrieverTools(); err != nil {
			t.Fatal(err)
		}
		if len(c.retrieverTools) != 1 || c.retrieverTools[0].Name() != "retriever_kb" {
			t.Fatalf("retrieverTools = %v", c.retrieverTools)
		}
	})
	t.Run("hybrid_builds_tools", func(t *testing.T) {
		c := &agentConfig{
			retrieverMode: RetrieverModeHybrid,
			retrievers:    []interfaces.Retriever{stubRetriever{}},
		}
		if err := c.buildRetrieverTools(); err != nil {
			t.Fatal(err)
		}
		if len(c.retrieverTools) != 1 {
			t.Fatalf("len = %d", len(c.retrieverTools))
		}
	})
	t.Run("prefetch_skips_tools", func(t *testing.T) {
		c := &agentConfig{
			retrieverMode: RetrieverModePrefetch,
			retrievers:    []interfaces.Retriever{stubRetriever{}},
		}
		if err := c.buildRetrieverTools(); err != nil {
			t.Fatal(err)
		}
		if c.retrieverTools != nil {
			t.Fatalf("retrieverTools = %v, want nil", c.retrieverTools)
		}
	})
	t.Run("no_retrievers", func(t *testing.T) {
		c := &agentConfig{retrieverMode: RetrieverModeAgentic}
		if err := c.buildRetrieverTools(); err != nil {
			t.Fatal(err)
		}
		if c.retrieverTools != nil {
			t.Fatalf("retrieverTools = %v, want nil", c.retrieverTools)
		}
	})
	t.Run("duplicate_name", func(t *testing.T) {
		c := &agentConfig{
			retrieverMode: RetrieverModeAgentic,
			retrievers:    []interfaces.Retriever{namedStubRetriever("x"), namedStubRetriever("x")},
		}
		err := c.buildRetrieverTools()
		if err == nil || !strings.Contains(err.Error(), "duplicate retriever name") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("empty_name", func(t *testing.T) {
		c := &agentConfig{
			retrieverMode: RetrieverModeAgentic,
			retrievers:    []interfaces.Retriever{namedStubRetriever("  ")},
		}
		err := c.buildRetrieverTools()
		if err == nil || !strings.Contains(err.Error(), "must not be empty") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestBuildAgentConfig_WithRetrievers(t *testing.T) {
	r1, r2 := namedStubRetriever("kb-a"), namedStubRetriever("kb-b")
	cfg, err := buildAgentConfig([]Option{
		WithName("test"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithRetrievers(r1, r2),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.retrievers) != 2 {
		t.Fatalf("retrievers len = %d", len(cfg.retrievers))
	}
	if len(cfg.retrieverTools) != 2 {
		t.Fatalf("retrieverTools len = %d, want 2 (default agentic mode)", len(cfg.retrieverTools))
	}
}

func TestBuildAgentConfig_RetrieverMode_prefetchNoTools(t *testing.T) {
	cfg, err := buildAgentConfig([]Option{
		WithName("test"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithRetrievers(stubRetriever{}),
		WithRetrieverMode(RetrieverModePrefetch),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.retrieverTools) != 0 {
		t.Fatalf("retrieverTools len = %d, want 0 for prefetch", len(cfg.retrieverTools))
	}
}

func TestBuildAgentConfig_RetrieverMode_agenticBuildsTools(t *testing.T) {
	cfg, err := buildAgentConfig([]Option{
		WithName("test"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithRetrievers(stubRetriever{}),
		WithRetrieverMode(RetrieverModeAgentic),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.retrieverTools) != 1 || cfg.retrieverTools[0].Name() != "retriever_stub" {
		t.Fatalf("retrieverTools = %v", cfg.retrieverTools)
	}
}

func TestBuildAgentConfig_AgenticNoRetrievers_NoTools(t *testing.T) {
	cfg, err := buildAgentConfig([]Option{
		WithName("test"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithRetrieverMode(RetrieverModeAgentic),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.retrieverTools) != 0 {
		t.Fatalf("retrieverTools len = %d, want 0", len(cfg.retrieverTools))
	}
}

func TestBuildAgentConfig_RetrieverMode_hybridBuildsTools(t *testing.T) {
	cfg, err := buildAgentConfig([]Option{
		WithName("test"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithRetrievers(stubRetriever{}),
		WithRetrieverMode(RetrieverModeHybrid),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.retrieverTools) != 1 || cfg.retrieverTools[0].Name() != "retriever_stub" {
		t.Fatalf("retrieverTools = %v", cfg.retrieverTools)
	}
}

func TestBuildAgentConfig_RetrieverDuplicateName(t *testing.T) {
	_, err := buildAgentConfig([]Option{
		WithName("test"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithRetrievers(namedStubRetriever("dup"), namedStubRetriever("dup")),
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate retriever name") {
		t.Fatalf("got %v", err)
	}
}

func TestBuildAgentConfig_toolsList_includesRetrieverTools(t *testing.T) {
	cfg, err := buildAgentConfig([]Option{
		WithName("test"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithTools(mockTool{name: "echo"}),
		WithRetrievers(stubRetriever{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	list := cfg.toolsList()
	if len(list) != 2 {
		t.Fatalf("toolsList len = %d, want 2", len(list))
	}
	if list[1].Name() != "retriever_stub" {
		t.Fatalf("tool[1].Name = %q", list[1].Name())
	}
}

func TestBuildAgentConfig_validateToolNames_RetrieverConflict(t *testing.T) {
	c := &agentConfig{
		tools: []interfaces.Tool{mockTool{name: "retriever_stub"}},
		retrieverTools: []interfaces.Tool{
			NewRetrieverTool(stubRetriever{}),
		},
	}
	err := c.validateToolNames()
	if err == nil || !strings.Contains(err.Error(), "retriever tool conflicts") {
		t.Fatalf("got %v", err)
	}
}

func TestBuildAgentConfig_validateToolNames_nilRetrieverTool(t *testing.T) {
	c := &agentConfig{retrieverTools: []interfaces.Tool{nil}}
	err := c.validateToolNames()
	if err == nil || !strings.Contains(err.Error(), "retriever tool must not be nil") {
		t.Fatalf("got %v", err)
	}
}

func TestBuildAgentConfig_WithRetrievers_nilEntry(t *testing.T) {
	_, err := buildAgentConfig([]Option{
		WithName("test"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithRetrievers(stubRetriever{}, nil),
	})
	if err == nil || !strings.Contains(err.Error(), "nil") {
		t.Fatalf("got %v", err)
	}
}

func TestBuildAgentConfig_WithRetrievers_emptyClears(t *testing.T) {
	cfg, err := buildAgentConfig([]Option{
		WithName("test"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithRetrievers(stubRetriever{}),
		WithRetrievers(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.retrievers != nil {
		t.Fatalf("retrievers = %v, want nil", cfg.retrievers)
	}
	if len(cfg.retrieverTools) != 0 {
		t.Fatalf("retrieverTools len = %d, want 0", len(cfg.retrieverTools))
	}
}

func TestBuildAgentConfig_RetrieverMode_default(t *testing.T) {
	cfg, err := buildAgentConfig([]Option{
		WithName("test"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.retrieverMode != RetrieverModeAgentic {
		t.Fatalf("retrieverMode = %q, want %q", cfg.retrieverMode, RetrieverModeAgentic)
	}
}

func TestBuildAgentConfig_RetrieverMode_explicit(t *testing.T) {
	for _, mode := range []RetrieverMode{
		RetrieverModeAgentic,
		RetrieverModePrefetch,
		RetrieverModeHybrid,
	} {
		t.Run(string(mode), func(t *testing.T) {
			cfg, err := buildAgentConfig([]Option{
				WithName("test"),
				WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
				WithLLMClient(stubLLM{}),
				WithRetrieverMode(mode),
			})
			if err != nil {
				t.Fatal(err)
			}
			if cfg.retrieverMode != mode {
				t.Fatalf("retrieverMode = %q, want %q", cfg.retrieverMode, mode)
			}
		})
	}
}

func TestAgentConfigFingerprint_RetrieverModeChangesDigest(t *testing.T) {
	baseOpts := []Option{
		WithName("test"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
	}
	build := func(mode RetrieverMode) string {
		t.Helper()
		opts := append(append([]Option(nil), baseOpts...), WithRetrieverMode(mode))
		cfg, err := buildAgentConfig(opts)
		if err != nil {
			t.Fatal(err)
		}
		return cfg.agentConfigFingerprint()
	}
	fpAgentic := build(RetrieverModeAgentic)
	fpPrefetch := build(RetrieverModePrefetch)
	fpHybrid := build(RetrieverModeHybrid)
	if fpAgentic == fpPrefetch {
		t.Fatal("expected different fingerprints for agentic vs prefetch retriever mode")
	}
	if fpAgentic == fpHybrid {
		t.Fatal("expected different fingerprints for agentic vs hybrid retriever mode")
	}
	if fpPrefetch == fpHybrid {
		t.Fatal("expected different fingerprints for prefetch vs hybrid retriever mode")
	}
}

func TestBuildAgentConfig_toolsList_includesRetrieverTools_hybrid(t *testing.T) {
	cfg, err := buildAgentConfig([]Option{
		WithName("test"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithTools(mockTool{name: "echo"}),
		WithRetrievers(stubRetriever{}),
		WithRetrieverMode(RetrieverModeHybrid),
	})
	if err != nil {
		t.Fatal(err)
	}
	list := cfg.toolsList()
	if len(list) != 2 {
		t.Fatalf("toolsList len = %d, want 2 (base tool + retriever tool)", len(list))
	}
	if list[1].Name() != "retriever_stub" {
		t.Fatalf("tool[1].Name = %q, want retriever_stub", list[1].Name())
	}
}

func TestAgentConfigFingerprint_AgenticRetrieverNamesChangesDigest(t *testing.T) {
	baseOpts := []Option{
		WithName("test"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithRetrieverMode(RetrieverModeAgentic),
	}
	cfgNoR, err := buildAgentConfig(baseOpts)
	if err != nil {
		t.Fatal(err)
	}
	cfgWithR, err := buildAgentConfig(append(baseOpts, WithRetrievers(namedStubRetriever("wiki"))))
	if err != nil {
		t.Fatal(err)
	}
	if cfgNoR.agentConfigFingerprint() == cfgWithR.agentConfigFingerprint() {
		t.Fatal("expected different fingerprints for agentic mode with vs without retriever names")
	}
}

func TestBuildAgentConfig_RetrieverMode_invalid(t *testing.T) {
	_, err := buildAgentConfig([]Option{
		WithName("test"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithRetrieverMode(RetrieverMode("bogus")),
	})
	if err == nil || !strings.Contains(err.Error(), "invalid retriever mode") {
		t.Fatalf("got %v", err)
	}
}

// ---------------------------------------------------------------------------
// A2A config tests
// ---------------------------------------------------------------------------

func TestValidateA2AClients(t *testing.T) {
	t.Run("nil_client", func(t *testing.T) {
		err := validateA2AClients([]interfaces.A2AClient{nil})
		if err == nil || !strings.Contains(err.Error(), "nil") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("empty_name", func(t *testing.T) {
		err := validateA2AClients([]interfaces.A2AClient{&stubA2AClient{name: "  "}})
		if err == nil || !strings.Contains(err.Error(), "empty") {
			t.Fatalf("got %v", err)
		}
	})
	t.Run("duplicate_name", func(t *testing.T) {
		c1 := &stubA2AClient{name: "agent"}
		c2 := &stubA2AClient{name: "agent"}
		err := validateA2AClients([]interfaces.A2AClient{c1, c2})
		if err == nil || !strings.Contains(err.Error(), "duplicate") {
			t.Fatalf("got %v", err)
		}
	})
}

func TestBuildAgentConfig_WithA2AConfig(t *testing.T) {
	url := newTestA2ACardServer(t, []a2a.AgentSkill{
		{ID: "search", Name: "Search", Description: "search tool"},
		{ID: "summarize", Name: "Summarize", Description: "summarize tool"},
	})
	cfg, err := buildAgentConfig([]Option{
		WithName("test"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithA2AConfig(A2AServers{"agent": A2AConfig{URL: url}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.a2aTools) != 2 {
		t.Fatalf("a2aTools len = %d, want 2", len(cfg.a2aTools))
	}
	if cfg.a2aTools[0].Name() != "a2a_agent_search" {
		t.Errorf("tool[0].Name = %q, want a2a_agent_search", cfg.a2aTools[0].Name())
	}
	if cfg.a2aTools[1].Name() != "a2a_agent_summarize" {
		t.Errorf("tool[1].Name = %q, want a2a_agent_summarize", cfg.a2aTools[1].Name())
	}
}

func TestBuildAgentConfig_WithA2AConfig_SkillFilter(t *testing.T) {
	url := newTestA2ACardServer(t, []a2a.AgentSkill{
		{ID: "keep", Name: "Keep", Description: "keep"},
		{ID: "drop", Name: "Drop", Description: "drop"},
	})
	cfg, err := buildAgentConfig([]Option{
		WithName("test"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithA2AConfig(A2AServers{"agent": A2AConfig{
			URL:         url,
			SkillFilter: types.A2ASkillFilter{AllowSkills: []string{"keep"}},
		}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.a2aTools) != 1 || cfg.a2aTools[0].Name() != "a2a_agent_keep" {
		t.Fatalf("a2aTools = %v, want [a2a_agent_keep]", cfg.a2aTools)
	}
}

func TestBuildAgentConfig_WithA2AClients(t *testing.T) {
	cl := &stubA2AClient{
		name:   "agent1",
		skills: []interfaces.A2ASkillSpec{{ID: "echo", Description: "echo back"}},
	}
	cfg, err := buildAgentConfig([]Option{
		WithName("test"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithA2AClients(cl),
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.a2aTools) != 1 || cfg.a2aTools[0].Name() != "a2a_agent1_echo" {
		t.Fatalf("a2aTools = %v, want [a2a_agent1_echo]", cfg.a2aTools)
	}
}

func TestBuildAgentConfig_WithA2ADefaultServer(t *testing.T) {
	cfg, err := buildAgentConfig([]Option{
		WithName("test"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithA2ADefaultServer(),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.a2aServerConfig == nil {
		t.Fatal("expected a2aServerConfig")
	}
	if cfg.a2aServerConfig.Hostname != defaultA2AHostname || cfg.a2aServerConfig.Port != defaultA2APort {
		t.Fatalf("defaults: got %+v, want hostname=%q port=%d", cfg.a2aServerConfig, defaultA2AHostname, defaultA2APort)
	}
	if len(cfg.a2aServerConfig.BearerTokens) != 0 {
		t.Fatalf("BearerTokens should be empty by default, got %v", cfg.a2aServerConfig.BearerTokens)
	}
}

func TestBuildAgentConfig_WithA2AServer(t *testing.T) {
	t.Run("custom_host_port_and_bearer_tokens", func(t *testing.T) {
		cfg, err := buildAgentConfig([]Option{
			WithName("test"),
			WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
			WithLLMClient(stubLLM{}),
			WithA2AServer(&A2AServerConfig{
				Hostname:     "0.0.0.0",
				Port:         8080,
				BearerTokens: []string{"alpha", "beta"},
			}),
		})
		if err != nil {
			t.Fatal(err)
		}
		s := cfg.a2aServerConfig
		if s == nil || s.Hostname != "0.0.0.0" || s.Port != 8080 || len(s.BearerTokens) != 2 {
			t.Fatalf("got %+v", s)
		}
		if s.BearerTokens[0] != "alpha" || s.BearerTokens[1] != "beta" {
			t.Fatalf("BearerTokens = %v", s.BearerTokens)
		}
	})

	t.Run("nil_config_same_as_defaults", func(t *testing.T) {
		cfg, err := buildAgentConfig([]Option{
			WithName("test"),
			WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
			WithLLMClient(stubLLM{}),
			WithA2AServer(nil),
		})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.a2aServerConfig == nil {
			t.Fatal("expected a2aServerConfig")
		}
		if cfg.a2aServerConfig.Hostname != defaultA2AHostname || cfg.a2aServerConfig.Port != defaultA2APort {
			t.Fatalf("nil config should default hostname/port: got %+v", cfg.a2aServerConfig)
		}
	})

	t.Run("empty_hostname_gets_default", func(t *testing.T) {
		cfg, err := buildAgentConfig([]Option{
			WithName("test"),
			WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
			WithLLMClient(stubLLM{}),
			WithA2AServer(&A2AServerConfig{Hostname: "", Port: 4000}),
		})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.a2aServerConfig.Hostname != defaultA2AHostname || cfg.a2aServerConfig.Port != 4000 {
			t.Fatalf("got %+v", cfg.a2aServerConfig)
		}
	})

	t.Run("zero_port_gets_default", func(t *testing.T) {
		cfg, err := buildAgentConfig([]Option{
			WithName("test"),
			WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
			WithLLMClient(stubLLM{}),
			WithA2AServer(&A2AServerConfig{Hostname: "127.0.0.1", Port: 0}),
		})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.a2aServerConfig.Hostname != "127.0.0.1" || cfg.a2aServerConfig.Port != defaultA2APort {
			t.Fatalf("got %+v", cfg.a2aServerConfig)
		}
	})

	t.Run("later_WithA2AServer_overrides_WithA2ADefaultServer", func(t *testing.T) {
		cfg, err := buildAgentConfig([]Option{
			WithName("test"),
			WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
			WithLLMClient(stubLLM{}),
			WithA2ADefaultServer(),
			WithA2AServer(&A2AServerConfig{Hostname: "custom.example", Port: 1111}),
		})
		if err != nil {
			t.Fatal(err)
		}
		if cfg.a2aServerConfig.Hostname != "custom.example" || cfg.a2aServerConfig.Port != 1111 {
			t.Fatalf("got %+v", cfg.a2aServerConfig)
		}
	})
}

// TestAgentConfigFingerprint_InboundA2AServerIgnored documents that Temporal agent fingerprint
// hashes outbound A2A client wiring only; inbound RunA2A listen config (including BearerTokens)
// must not affect caller/worker digest comparison.
func TestAgentConfigFingerprint_InboundA2AServerIgnored(t *testing.T) {
	base := []Option{
		WithName("fp-test"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
	}
	cfgNoInbound, err := buildAgentConfig(base)
	if err != nil {
		t.Fatal(err)
	}
	cfgWithInbound, err := buildAgentConfig(append(base,
		WithA2AServer(&A2AServerConfig{
			Hostname:     "0.0.0.0",
			Port:         7777,
			BearerTokens: []string{"secret-one", "secret-two"},
		}),
	))
	if err != nil {
		t.Fatal(err)
	}
	if cfgNoInbound.agentConfigFingerprint() != cfgWithInbound.agentConfigFingerprint() {
		t.Fatalf("inbound A2AServerConfig should not change agent fingerprint: %q vs %q",
			cfgNoInbound.agentConfigFingerprint(), cfgWithInbound.agentConfigFingerprint())
	}
}

func TestBuildAgentConfig_WithA2AConfig_URLRequired(t *testing.T) {
	_, err := buildAgentConfig([]Option{
		WithName("test"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithA2AConfig(A2AServers{"agent": A2AConfig{URL: ""}}),
	})
	if err == nil || !strings.Contains(err.Error(), "URL is required") {
		t.Fatalf("got %v", err)
	}
}

func TestBuildAgentConfig_A2A_duplicateClientName(t *testing.T) {
	// Config creates a client named "dup"; explicit client also named "dup" → duplicate.
	// "http://127.0.0.1:1" is a non-routable address; NewClient is lazy so no network call is made.
	cl := &stubA2AClient{name: "dup", skills: []interfaces.A2ASkillSpec{{ID: "s", Description: "s"}}}
	_, err := buildAgentConfig([]Option{
		WithName("test"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithA2AConfig(A2AServers{"dup": A2AConfig{URL: "http://127.0.0.1:1"}}),
		WithA2AClients(cl),
	})
	if err == nil || !strings.Contains(err.Error(), "duplicate a2a client name") {
		t.Fatalf("got %v", err)
	}
}

func TestAgentConfig_toolsList_includesA2ATools(t *testing.T) {
	echo := mockTool{name: "echo"}
	a2aTool := NewA2ATool("agent1", interfaces.ToolSpec{Name: "search", Description: "d"}, interfaces.A2ASkillSpec{}, nil)
	c := &agentConfig{
		tools:    []interfaces.Tool{echo},
		a2aTools: []interfaces.Tool{a2aTool},
	}
	list := c.toolsList()
	if len(list) != 2 {
		t.Fatalf("toolsList len = %d, want 2", len(list))
	}
	if list[0].Name() != "echo" {
		t.Errorf("list[0].Name = %q, want echo", list[0].Name())
	}
	if list[1].Name() != "a2a_agent1_search" {
		t.Errorf("list[1].Name = %q, want a2a_agent1_search", list[1].Name())
	}
}

func TestAgentConfig_validateToolNames_A2AConflict(t *testing.T) {
	a2aTool := NewA2ATool("srv", interfaces.ToolSpec{Name: "s", Description: "d"}, interfaces.A2ASkillSpec{}, nil)
	c := &agentConfig{
		tools:    []interfaces.Tool{mockTool{name: a2aTool.Name()}},
		a2aTools: []interfaces.Tool{a2aTool},
	}
	err := c.validateToolNames()
	if err == nil || (!strings.Contains(err.Error(), "duplicate tool name") && !strings.Contains(err.Error(), "conflicts")) {
		t.Fatalf("want duplicate/conflict error, got %v", err)
	}
}

func TestObservabilityConfigFingerprint_defaultProtocolMatchesExplicitGRPC(t *testing.T) {
	implicit := observabilityConfigFingerprint(&ObservabilityConfig{Endpoint: "localhost:4317"})
	explicit := observabilityConfigFingerprint(&ObservabilityConfig{
		Endpoint: "localhost:4317",
		Protocol: OTLPProtocolGRPC,
	})
	if implicit != explicit {
		t.Fatalf("empty Protocol should fingerprint same as grpc: %q vs %q", implicit, explicit)
	}
}

func TestObservabilityConfigFingerprint_protocolAndInsecureChangeDigest(t *testing.T) {
	base := observabilityConfigFingerprint(&ObservabilityConfig{Endpoint: "localhost:4317"})
	withHTTP := observabilityConfigFingerprint(&ObservabilityConfig{
		Endpoint: "localhost:4317",
		Protocol: OTLPProtocolHTTP,
	})
	if base == withHTTP {
		t.Fatal("expected different digest when Protocol changes")
	}
	insecure := observabilityConfigFingerprint(&ObservabilityConfig{
		Endpoint: "localhost:4317",
		Insecure: true,
	})
	if base == insecure {
		t.Fatal("expected different digest when Insecure changes")
	}
}

func TestObservabilityOptions_appliesTypesDefaults(t *testing.T) {
	oc := &ObservabilityConfig{
		Endpoint: "collector.example:4317",
		Protocol: OTLPProtocolHTTP,
		Insecure: true,
	}
	ac := &agentConfig{
		Name:                "my-agent",
		logger:              logger.DefaultLogger("error"),
		observabilityConfig: oc,
	}
	cfg, err := observability.BuildConfig(observabilityOptions(ac)...)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Name != "my-agent" || cfg.Endpoint != oc.Endpoint {
		t.Fatalf("cfg = %+v", cfg)
	}
	if cfg.Protocol != observability.ProtocolHTTP {
		t.Fatalf("Protocol = %q", cfg.Protocol)
	}
	if !cfg.Insecure {
		t.Fatal("want Insecure true")
	}
	if cfg.ExportTimeout != types.DefaultOTLPExportTimeout {
		t.Fatalf("ExportTimeout = %v", cfg.ExportTimeout)
	}
	if cfg.BatchTimeout != types.DefaultOTLPBatchTimeout {
		t.Fatalf("BatchTimeout = %v", cfg.BatchTimeout)
	}
	if cfg.MetricsInterval != types.DefaultOTLPMetricsInterval {
		t.Fatalf("MetricsInterval = %v", cfg.MetricsInterval)
	}
}

func TestBuildAgentConfig_WithObservabilityConfig_HTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(strings.TrimPrefix(srv.URL, "http://"), "https://")

	cfg, err := buildAgentConfig([]Option{
		WithName("obs-agent"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithObservabilityConfig(&ObservabilityConfig{
			Endpoint: host,
			Protocol: OTLPProtocolHTTP,
			Insecure: true,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.tracer == nil || cfg.metrics == nil {
		t.Fatalf("want tracer and metrics from observability config, tracer=%v metrics=%v", cfg.tracer, cfg.metrics)
	}
	if _, ok := cfg.logs.(*observability.Logs); !ok {
		t.Fatalf("want OTLP *observability.Logs from observability config, got %T", cfg.logs)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := cfg.tracer.Shutdown(ctx); err != nil {
		t.Errorf("tracer Shutdown: %v", err)
	}
	if err := cfg.metrics.Shutdown(ctx); err != nil {
		t.Errorf("metrics Shutdown: %v", err)
	}
	if err := cfg.logs.Shutdown(ctx); err != nil {
		t.Errorf("logs Shutdown: %v", err)
	}
}

func TestBuildAgentConfig_WithObservabilityConfig_DisableTraces_keepsInjectedTracer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(strings.TrimPrefix(srv.URL, "http://"), "https://")

	stub := presStubTracer{}
	cfg, err := buildAgentConfig([]Option{
		WithName("disable-traces-keep-tracer"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithTracer(stub),
		WithObservabilityConfig(&ObservabilityConfig{
			Endpoint:      host,
			Protocol:      OTLPProtocolHTTP,
			Insecure:      true,
			DisableTraces: true,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.tracer != stub {
		t.Fatalf("expected WithTracer to remain when DisableTraces=true, got %T", cfg.tracer)
	}
}

func TestBuildAgentConfig_WithObservabilityConfig_DisableMetrics_keepsInjectedMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(strings.TrimPrefix(srv.URL, "http://"), "https://")

	stub := presStubMetrics{}
	cfg, err := buildAgentConfig([]Option{
		WithName("disable-metrics-keep-metrics"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithMetrics(stub),
		WithObservabilityConfig(&ObservabilityConfig{
			Endpoint:       host,
			Protocol:       OTLPProtocolHTTP,
			Insecure:       true,
			DisableMetrics: true,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.metrics != stub {
		t.Fatalf("expected WithMetrics to remain when DisableMetrics=true, got %T", cfg.metrics)
	}
}

func TestBuildAgentConfig_WithObservabilityConfig_replacesInjectedTracer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(strings.TrimPrefix(srv.URL, "http://"), "https://")

	tr, err := observability.NewTracer(
		observability.WithEndpoint(host),
		observability.WithName("pre-inject-tracer"),
		observability.WithProtocol(observability.ProtocolHTTP),
		observability.WithInsecure(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = tr.Shutdown(ctx)
	}()

	cfg, err := buildAgentConfig([]Option{
		WithName("obs-replace-tracer"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithTracer(tr),
		WithObservabilityConfig(&ObservabilityConfig{
			Endpoint: host,
			Protocol: OTLPProtocolHTTP,
			Insecure: true,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.tracer == tr {
		t.Fatal("expected observability-built tracer to replace injected WithTracer, same pointer")
	}
	if _, ok := cfg.tracer.(*observability.Tracer); !ok {
		t.Fatalf("want *observability.Tracer from observability config, got %T", cfg.tracer)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = cfg.tracer.Shutdown(ctx)
}

func TestBuildAgentConfig_WithObservabilityConfig_replacesInjectedMetrics(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(strings.TrimPrefix(srv.URL, "http://"), "https://")

	mt, err := observability.NewMetrics(
		observability.WithEndpoint(host),
		observability.WithName("pre-inject-metrics"),
		observability.WithProtocol(observability.ProtocolHTTP),
		observability.WithInsecure(true),
		observability.WithMetricsInterval(40*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = mt.Shutdown(ctx)
	}()

	cfg, err := buildAgentConfig([]Option{
		WithName("obs-replace-metrics"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithMetrics(mt),
		WithObservabilityConfig(&ObservabilityConfig{
			Endpoint: host,
			Protocol: OTLPProtocolHTTP,
			Insecure: true,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.metrics == mt {
		t.Fatal("expected observability-built metrics to replace injected WithMetrics, same pointer")
	}
	if _, ok := cfg.metrics.(*observability.Metrics); !ok {
		t.Fatalf("want *observability.Metrics from observability config, got %T", cfg.metrics)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = cfg.metrics.Shutdown(ctx)
}

func TestBuildAgentConfig_WithObservabilityConfig_replacesInjectedTracerMetricsLogsTogether(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(strings.TrimPrefix(srv.URL, "http://"), "https://")

	tr, err := observability.NewTracer(
		observability.WithEndpoint(host),
		observability.WithName("triple-pre-tr"),
		observability.WithProtocol(observability.ProtocolHTTP),
		observability.WithInsecure(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	mt, err := observability.NewMetrics(
		observability.WithEndpoint(host),
		observability.WithName("triple-pre-mt"),
		observability.WithProtocol(observability.ProtocolHTTP),
		observability.WithInsecure(true),
		observability.WithMetricsInterval(40*time.Millisecond),
	)
	if err != nil {
		_ = tr.Shutdown(context.Background())
		t.Fatal(err)
	}
	lg, err := observability.NewLogs(
		observability.WithEndpoint(host),
		observability.WithName("triple-pre-lg"),
		observability.WithProtocol(observability.ProtocolHTTP),
		observability.WithInsecure(true),
	)
	if err != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = tr.Shutdown(ctx)
		_ = mt.Shutdown(ctx)
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = tr.Shutdown(ctx)
		_ = mt.Shutdown(ctx)
		_ = lg.Shutdown(ctx)
	}()

	cfg, err := buildAgentConfig([]Option{
		WithName("triple-replace"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithTracer(tr),
		WithMetrics(mt),
		WithLogs(lg),
		WithObservabilityConfig(&ObservabilityConfig{
			Endpoint: host,
			Protocol: OTLPProtocolHTTP,
			Insecure: true,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.tracer == tr || cfg.metrics == mt || cfg.logs == lg {
		t.Fatalf("expected all three signals replaced by observability build: tracerSame=%v metricsSame=%v logsSame=%v",
			cfg.tracer == tr, cfg.metrics == mt, cfg.logs == lg)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = cfg.tracer.Shutdown(ctx)
	_ = cfg.metrics.Shutdown(ctx)
	_ = cfg.logs.Shutdown(ctx)
}

func TestBuildAgentConfig_injectedStubLogs_withoutObs_doesNotWireOtelLogger(t *testing.T) {
	cfg, err := buildAgentConfig([]Option{
		WithName("stub-logs-no-otel"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithLogs(&stubLogs{}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if otlpLogsClientConfigured(cfg.logs) {
		t.Fatal("stub WithLogs must not count as OTLP *observability.Logs for logger wiring")
	}
}

type stubLogs struct {
	shutdowns int
}

func (s *stubLogs) Shutdown(_ context.Context) error {
	s.shutdowns++
	return nil
}

// presStubTracer / presStubMetrics are minimal [interfaces.Tracer] / [interfaces.Metrics] for precedence tests.
type presStubTracer struct{}

func (presStubTracer) StartSpan(ctx context.Context, name string, attrs ...interfaces.Attribute) (context.Context, interfaces.Span) {
	return ctx, &observability.NoopSpan{}
}

func (presStubTracer) Shutdown(context.Context) error { return nil }

type presStubMetrics struct{}

func (presStubMetrics) IncrementCounter(context.Context, string, ...interfaces.Attribute) {}

func (presStubMetrics) RecordHistogram(context.Context, string, float64, ...interfaces.Attribute) {}

func (presStubMetrics) Shutdown(context.Context) error { return nil }

func TestBuildAgentConfig_WithLogs_withoutObservability(t *testing.T) {
	stub := &stubLogs{}
	cfg, err := buildAgentConfig([]Option{
		WithName("logs-inject"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithLogs(stub),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.logs != stub {
		t.Fatalf("expected injected WithLogs to be kept without WithObservabilityConfig, got %T", cfg.logs)
	}
}

func TestBuildAgentConfig_WithObservabilityConfig_overwritesWithLogs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(strings.TrimPrefix(srv.URL, "http://"), "https://")

	stub := &stubLogs{}
	cfg, err := buildAgentConfig([]Option{
		WithName("obs-overwrites-logs"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithLogs(stub),
		WithObservabilityConfig(&ObservabilityConfig{
			Endpoint: host,
			Protocol: OTLPProtocolHTTP,
			Insecure: true,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := cfg.logs.(*observability.Logs); !ok {
		t.Fatalf("expected built OTLP *observability.Logs to replace WithLogs, got %T", cfg.logs)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = cfg.logs.Shutdown(ctx)
}

func TestBuildAgentConfig_NewLogs_injected_alone_autoWiresDefaultLogger(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(strings.TrimPrefix(srv.URL, "http://"), "https://")

	lg, err := observability.NewLogs(
		observability.WithEndpoint(host),
		observability.WithName("inject-only-logs"),
		observability.WithProtocol(observability.ProtocolHTTP),
		observability.WithInsecure(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = lg.Shutdown(ctx)
	}()

	cfg, err := buildAgentConfig([]Option{
		WithName("inject-logs-wire"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithLogs(lg),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.logs != lg {
		t.Fatalf("expected WithLogs instance to be kept without observability config, got %T", cfg.logs)
	}
	ctx := context.Background()
	cfg.logger.Info(ctx, "smoke after auto-wire")
}

func TestBuildAgentConfig_WithObservabilityConfig_replacesInjectedOTLPLogs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(strings.TrimPrefix(srv.URL, "http://"), "https://")

	lg, err := observability.NewLogs(
		observability.WithEndpoint(host),
		observability.WithName("pre-inject"),
		observability.WithProtocol(observability.ProtocolHTTP),
		observability.WithInsecure(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = lg.Shutdown(ctx)
	}()

	cfg, err := buildAgentConfig([]Option{
		WithName("obs-replace-inject"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithLogs(lg),
		WithObservabilityConfig(&ObservabilityConfig{
			Endpoint: host,
			Protocol: OTLPProtocolHTTP,
			Insecure: true,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.logs == lg {
		t.Fatal("expected observability-built Logs to replace injected WithLogs, not the same pointer")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = cfg.logs.Shutdown(ctx)
}

func TestBuildAgentConfig_WithObservabilityConfig_DisableLogs_keepsWithLogs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(strings.TrimPrefix(srv.URL, "http://"), "https://")

	stub := &stubLogs{}
	cfg, err := buildAgentConfig([]Option{
		WithName("disable-logs-keep-inject"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithLogs(stub),
		WithObservabilityConfig(&ObservabilityConfig{
			Endpoint:    host,
			Protocol:    OTLPProtocolHTTP,
			Insecure:    true,
			DisableLogs: true,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.logs != stub {
		t.Fatalf("expected WithLogs to remain when DisableLogs=true, got %T", cfg.logs)
	}
}

func TestBuildAgentConfig_WithObservabilityConfig_DisableLogs_injectedOTLPLogs_wiresDefaultLogger(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(strings.TrimPrefix(srv.URL, "http://"), "https://")

	lg, err := observability.NewLogs(
		observability.WithEndpoint(host),
		observability.WithName("disable-logs-otlp-inject"),
		observability.WithProtocol(observability.ProtocolHTTP),
		observability.WithInsecure(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = lg.Shutdown(ctx)
	}()

	cfg, err := buildAgentConfig([]Option{
		WithName("disable-logs-wire-otel"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithLogs(lg),
		WithObservabilityConfig(&ObservabilityConfig{
			Endpoint:    host,
			Protocol:    OTLPProtocolHTTP,
			Insecure:    true,
			DisableLogs: true,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if cfg.logs != lg {
		t.Fatalf("expected injected OTLP Logs to remain when DisableLogs=true, got %T", cfg.logs)
	}
	if !otlpLogsClientConfigured(cfg.logs) {
		t.Fatal("expected *observability.Logs so default logger can bridge to OTLP")
	}
	ctx := context.Background()
	cfg.logger.Info(ctx, "smoke with DisableLogs and injected OTLP Logs")
}

func TestBuildAgentConfig_WithObservabilityConfig_customLogger_warnsAboutLogs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	host := strings.TrimPrefix(strings.TrimPrefix(srv.URL, "http://"), "https://")

	var buf bytes.Buffer
	custom := logger.NewWriterLogger(&buf, "warn", "text", false)

	_, err := buildAgentConfig([]Option{
		WithName("custom-log-warn"),
		WithTemporalConfig(&TemporalConfig{TaskQueue: "q"}),
		WithLLMClient(stubLLM{}),
		WithLogger(custom),
		WithObservabilityConfig(&ObservabilityConfig{
			Endpoint: host,
			Protocol: OTLPProtocolHTTP,
			Insecure: true,
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "custom WithLogger") {
		t.Fatalf("expected warning about custom WithLogger and OTLP logs; buf=%q", out)
	}
}
