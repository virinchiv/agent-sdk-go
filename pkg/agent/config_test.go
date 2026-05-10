package agent

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	mcpclient "github.com/agenticenv/agent-sdk-go/pkg/mcp/client"
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
