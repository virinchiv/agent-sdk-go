package setup

import (
	"fmt"
	"strings"

	testutil "github.com/agenticenv/agent-sdk-go/internal/testing"
	"github.com/agenticenv/agent-sdk-go/pkg/agent"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
	"github.com/agenticenv/agent-sdk-go/pkg/memory"
)

const (
	DefaultAgentName          = "eval-agent"
	DefaultToolCount          = 3
	DefaultMockTokens         = 500
	DefaultSystemPrompt       = "You are an evaluation agent. Use available tools when helpful, then answer concisely."
	DefaultRuntime            = RuntimeLocal
	DefaultMemoryUserID       = "eval-user"
	DefaultMemoryStoreMode    = memory.StoreModeOnDemand
	MemoryScenarioStoreRecall = "store_recall"
)

// Runtime selects the agent execution backend.
type Runtime string

const (
	RuntimeLocal    Runtime = "local"
	RuntimeTemporal Runtime = "temporal"
)

// LLMConfig configures the built-in mock LLM (internal defaults, not in YAML).
type LLMConfig struct {
	MockTokens int
}

// ToolConfig configures mock tools (internal defaults, not in YAML).
type ToolConfig struct{}

// TemporalConfig configures Temporal when Runtime is temporal.
type TemporalConfig struct {
	Host      string `mapstructure:"host"`
	Port      int    `mapstructure:"port"`
	Namespace string `mapstructure:"namespace"`
	TaskQueue string `mapstructure:"task_queue"`
}

// MemoryConfig configures long-term memory for eval harness runs.
type MemoryConfig struct {
	Enabled      bool
	StoreMode    memory.StoreMode
	UserID       string
	Scenario     string
	StorePrompt  string
	RecallPrompt string
}

// Config holds settings for a single eval agent run.
type Config struct {
	UserPrompt   string
	Runtime      Runtime
	Temporal     TemporalConfig
	AgentName    string
	SystemPrompt string
	LLM          LLMConfig
	Tool         ToolConfig
	ToolCount    int
	Memory       MemoryConfig
	LLMClient    interfaces.LLMClient
	ToolRegistry agent.ToolRegistry
	Logger       logger.Logger
}

// UseTemporal reports whether cfg selects the Temporal runtime.
func (c *Config) UseTemporal() bool {
	return c != nil && strings.EqualFold(strings.TrimSpace(string(c.Runtime)), string(RuntimeTemporal))
}

// MemoryEnabled reports whether memory is wired for this run.
func (c *Config) MemoryEnabled() bool {
	return c != nil && c.Memory.Enabled
}

// UsesMemoryScenario reports whether the runner executes a multi-step memory scenario.
func (c *Config) UsesMemoryScenario() bool {
	return c.MemoryEnabled() && strings.EqualFold(strings.TrimSpace(c.Memory.Scenario), MemoryScenarioStoreRecall)
}

// ApplyMemoryDefaults fills unset memory config fields.
func (m *MemoryConfig) ApplyMemoryDefaults() {
	if m == nil {
		return
	}
	if strings.TrimSpace(m.UserID) == "" {
		m.UserID = DefaultMemoryUserID
	}
	if strings.TrimSpace(string(m.StoreMode)) == "" {
		m.StoreMode = DefaultMemoryStoreMode
	}
}

// ApplyDefaults fills unset config fields.
func (c *Config) ApplyDefaults() {
	if c == nil {
		return
	}
	if strings.TrimSpace(string(c.Runtime)) == "" {
		c.Runtime = DefaultRuntime
	}
	if c.AgentName == "" {
		c.AgentName = DefaultAgentName
	}
	if c.SystemPrompt == "" {
		c.SystemPrompt = DefaultSystemPrompt
	}
	if c.ToolCount <= 0 && !c.MemoryEnabled() {
		c.ToolCount = DefaultToolCount
	}
	if c.LLM.MockTokens <= 0 {
		c.LLM.MockTokens = DefaultMockTokens
	}
	if c.Logger == nil {
		c.Logger = logger.NoopLogger()
	}
	c.Memory.ApplyMemoryDefaults()
	if c.Temporal.TaskQueue == "" {
		c.Temporal.TaskQueue = "eval-harness"
	}
	if c.Temporal.Port == 0 {
		c.Temporal.Port = 7233
	}
	if c.Temporal.Host == "" {
		c.Temporal.Host = "localhost"
	}
	if c.Temporal.Namespace == "" {
		c.Temporal.Namespace = "default"
	}
}

// ValidateMemory checks memory-related config when enabled.
func (c *Config) ValidateMemory() error {
	if c == nil || !c.Memory.Enabled {
		return nil
	}
	c.Memory.ApplyMemoryDefaults()
	switch c.Memory.StoreMode {
	case memory.StoreModeOnDemand, memory.StoreModeAlways:
	default:
		return fmt.Errorf("memory.store_mode must be %q or %q", memory.StoreModeOnDemand, memory.StoreModeAlways)
	}
	if !c.UsesMemoryScenario() {
		return nil
	}
	if strings.TrimSpace(c.Memory.StorePrompt) == "" {
		return fmt.Errorf("memory.store_prompt is required when memory.scenario is %q", MemoryScenarioStoreRecall)
	}
	if strings.TrimSpace(c.Memory.RecallPrompt) == "" {
		return fmt.Errorf("memory.recall_prompt is required when memory.scenario is %q", MemoryScenarioStoreRecall)
	}
	return nil
}

// Validate checks required config fields.
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("config is required")
	}
	switch strings.ToLower(strings.TrimSpace(string(c.Runtime))) {
	case string(RuntimeLocal), string(RuntimeTemporal):
	default:
		return fmt.Errorf("runtime must be %q or %q", RuntimeLocal, RuntimeTemporal)
	}
	if !c.UsesMemoryScenario() && c.UserPrompt == "" {
		return fmt.Errorf("user prompt is required")
	}
	return c.ValidateMemory()
}

// ParseMemoryStoreMode parses eval harness store mode strings.
func ParseMemoryStoreMode(raw string) (memory.StoreMode, error) {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(memory.StoreModeOnDemand), "on-demand", "on_demand":
		return memory.StoreModeOnDemand, nil
	case string(memory.StoreModeAlways):
		return memory.StoreModeAlways, nil
	default:
		return "", fmt.Errorf("memory store mode must be %q or %q", memory.StoreModeOnDemand, memory.StoreModeAlways)
	}
}

// MemoryAgentOption returns WithMemory when memory is enabled.
func MemoryAgentOption(cfg Config) (agent.Option, error) {
	if !cfg.MemoryEnabled() {
		return nil, nil
	}
	cfg.Memory.ApplyMemoryDefaults()
	memCfg := memory.DefaultConfig(testutil.NewInmemMemory())
	memCfg.Store.Mode = cfg.Memory.StoreMode
	memCfg.Recall.Enabled = true
	return agent.WithMemory(memCfg), nil
}
