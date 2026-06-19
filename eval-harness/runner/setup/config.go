package setup

import (
	"fmt"
	"strings"

	"github.com/agenticenv/agent-sdk-go/pkg/agent"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
)

const (
	DefaultAgentName    = "eval-agent"
	DefaultToolCount    = 3
	DefaultMockTokens   = 500
	DefaultSystemPrompt = "You are an evaluation agent. Use available tools when helpful, then answer concisely."
	DefaultRuntime      = RuntimeLocal
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
	LLMClient    interfaces.LLMClient
	ToolRegistry agent.ToolRegistry
	Logger       logger.Logger
}

// UseTemporal reports whether cfg selects the Temporal runtime.
func (c *Config) UseTemporal() bool {
	return c != nil && strings.EqualFold(strings.TrimSpace(string(c.Runtime)), string(RuntimeTemporal))
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
	if c.ToolCount <= 0 {
		c.ToolCount = DefaultToolCount
	}
	if c.LLM.MockTokens <= 0 {
		c.LLM.MockTokens = DefaultMockTokens
	}
	if c.Logger == nil {
		c.Logger = logger.NoopLogger()
	}
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

// Validate checks required config fields.
func (c *Config) Validate() error {
	if c == nil {
		return fmt.Errorf("config is required")
	}
	if c.UserPrompt == "" {
		return fmt.Errorf("user prompt is required")
	}
	switch strings.ToLower(strings.TrimSpace(string(c.Runtime))) {
	case string(RuntimeLocal), string(RuntimeTemporal):
	default:
		return fmt.Errorf("runtime must be %q or %q", RuntimeLocal, RuntimeTemporal)
	}
	return nil
}
