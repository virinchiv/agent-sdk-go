package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/viper"
)

const BenchmarkTreeSeed int64 = 42

type Config struct {
	Runtime  string         `mapstructure:"runtime"`
	Temporal TemporalConfig `mapstructure:"temporal"`
	LLM      LLMConfig      `mapstructure:"llm"`
	Tool     ToolConfig     `mapstructure:"tool"`
	Agent    AgentConfig    `mapstructure:"agent"`
	Logger   LoggerConfig   `mapstructure:"logger"`
	Output   OutputConfig   `mapstructure:"output"`
}

type TemporalConfig struct {
	Host         string `mapstructure:"host"`
	Port         int    `mapstructure:"port"`
	Namespace    string `mapstructure:"namespace"`
	TaskQueue    string `mapstructure:"task_queue"`
	WorkersCount int    `mapstructure:"workers_count"`
}

type LLMConfig struct {
	LatencyMs  int `mapstructure:"latency_ms"`
	JitterMs   int `mapstructure:"jitter_ms"`
	MockTokens int `mapstructure:"mock_tokens"`
}

type ToolConfig struct {
	LatencyMs int `mapstructure:"latency_ms"`
	JitterMs  int `mapstructure:"jitter_ms"`
}

type AgentConfig struct {
	Runs            int              `mapstructure:"runs"`
	Concurrent      bool             `mapstructure:"concurrent"`
	ConcurrentCount int              `mapstructure:"concurrent_count"`
	Tools           AgentToolsConfig `mapstructure:"tools"`
	Subagents       SubagentsConfig  `mapstructure:"subagents"`
}

type AgentToolsConfig struct {
	Count     int    `mapstructure:"count"`
	Execution string `mapstructure:"execution"`
}

type SubagentsConfig struct {
	Count  int `mapstructure:"count"`
	Levels int `mapstructure:"levels"`
}

type LoggerConfig struct {
	Enabled bool   `mapstructure:"enabled"`
	Dir     string `mapstructure:"dir"`
	Level   string `mapstructure:"level"`
}

type OutputConfig struct {
	Console bool   `mapstructure:"console"`
	File    bool   `mapstructure:"file"`
	Dir     string `mapstructure:"dir"`
	Format  string `mapstructure:"format"`
}

func (c *Config) UseTemporal() bool {
	return c != nil && strings.EqualFold(strings.TrimSpace(c.Runtime), "temporal")
}

func (c *Config) ExternalWorkersEnabled() bool {
	return c.UseTemporal() && c.Temporal.WorkersCount > 0
}

func LoadConfig(path string) (*Config, error) {
	if path == "" {
		path = defaultConfigPath()
	}
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

func (c *Config) validate() error {
	if c.Agent.Runs <= 0 {
		return fmt.Errorf("agent.runs must be > 0")
	}
	if c.Agent.Concurrent && c.Agent.ConcurrentCount <= 0 {
		return fmt.Errorf("agent.concurrent_count must be > 0 when concurrent is true")
	}
	if c.Agent.Tools.Count <= 0 {
		return fmt.Errorf("agent.tools.count must be > 0")
	}
	if c.Agent.Subagents.Levels < 0 {
		return fmt.Errorf("agent.subagents.levels must be >= 0")
	}
	if c.Agent.Subagents.Levels > 5 {
		return fmt.Errorf("agent.subagents.levels must be <= 5")
	}
	if c.Agent.Subagents.Count < 0 {
		return fmt.Errorf("agent.subagents.count must be >= 0")
	}
	if c.Temporal.WorkersCount < 0 {
		return fmt.Errorf("temporal.workers_count must be >= 0")
	}
	if c.LLM.MockTokens <= 0 {
		c.LLM.MockTokens = 500
	}
	if c.Logger.Dir == "" {
		c.Logger.Dir = "benchmarks/logs"
	}
	if strings.TrimSpace(c.Logger.Level) == "" {
		c.Logger.Level = "info"
	}
	if c.Output.Dir == "" {
		c.Output.Dir = "benchmarks/reports"
	}
	if c.Output.Format == "" {
		c.Output.Format = "json"
	}
	if c.Temporal.TaskQueue == "" {
		c.Temporal.TaskQueue = "agent-sdk-go"
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
	return nil
}

func defaultConfigPath() string {
	for _, candidate := range []string{"benchmarks/config.yaml", "config.yaml"} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return "benchmarks/config.yaml"
}

// DefaultConfigPath returns the default benchmark config file path.
func DefaultConfigPath() string { return defaultConfigPath() }

func FindRepoRoot(from string) (string, error) {
	dir, err := filepath.Abs(from)
	if err != nil {
		return "", err
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("go.mod not found from %s", from)
		}
		dir = parent
	}
}

func (c *Config) OutputDir(repoRoot string) string {
	return resolveRepoPath(repoRoot, c.Output.Dir)
}

func (c *Config) LogDir(repoRoot string) string {
	return resolveRepoPath(repoRoot, c.Logger.Dir)
}

func resolveRepoPath(repoRoot, dir string) string {
	dir = strings.TrimSpace(dir)
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(repoRoot, dir)
}
