package setup

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/viper"
)

// FileConfig is the YAML configuration for eval-harness runs.
type FileConfig struct {
	Runtime    string           `mapstructure:"runtime"`
	UserPrompt string           `mapstructure:"user_prompt"`
	Agent      FileAgentConfig  `mapstructure:"agent"`
	Memory     FileMemoryConfig `mapstructure:"memory"`
	Temporal   TemporalConfig   `mapstructure:"temporal"`
}

// FileMemoryConfig holds memory fields from YAML.
type FileMemoryConfig struct {
	Enabled      bool   `mapstructure:"enabled"`
	StoreMode    string `mapstructure:"store_mode"`
	UserID       string `mapstructure:"user_id"`
	Scenario     string `mapstructure:"scenario"`
	StorePrompt  string `mapstructure:"store_prompt"`
	RecallPrompt string `mapstructure:"recall_prompt"`
}

// FileAgentConfig holds agent fields from YAML.
type FileAgentConfig struct {
	Name         string `mapstructure:"name"`
	SystemPrompt string `mapstructure:"system_prompt"`
	ToolCount    int    `mapstructure:"tool_count"`
}

// Config returns a runner Config from the file config.
func (f *FileConfig) Config() Config {
	if f == nil {
		return Config{}
	}
	storeMode, _ := ParseMemoryStoreMode(f.Memory.StoreMode)
	return Config{
		UserPrompt:   f.UserPrompt,
		Runtime:      Runtime(f.Runtime),
		Temporal:     f.Temporal,
		AgentName:    f.Agent.Name,
		SystemPrompt: f.Agent.SystemPrompt,
		ToolCount:    f.Agent.ToolCount,
		Memory: MemoryConfig{
			Enabled:      f.Memory.Enabled,
			StoreMode:    storeMode,
			UserID:       f.Memory.UserID,
			Scenario:     f.Memory.Scenario,
			StorePrompt:  f.Memory.StorePrompt,
			RecallPrompt: f.Memory.RecallPrompt,
		},
	}
}

// LoadConfig reads and validates eval-harness config from a YAML file.
func LoadConfig(path string) (*FileConfig, error) {
	if path == "" {
		path = defaultConfigPath()
	}
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")
	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config %q: %w", path, err)
	}
	var cfg FileConfig
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// DefaultConfigPath returns the default eval-harness config file path.
func DefaultConfigPath() string { return defaultConfigPath() }

func defaultConfigPath() string {
	for _, candidate := range []string{
		"eval-harness/runner/config.yaml",
		"runner/config.yaml",
		"config.yaml",
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return "config.yaml"
}

func (f *FileConfig) validate() error {
	if f == nil {
		return fmt.Errorf("config is required")
	}
	if strings.TrimSpace(f.UserPrompt) == "" && !strings.EqualFold(strings.TrimSpace(f.Memory.Scenario), MemoryScenarioStoreRecall) {
		return fmt.Errorf("user_prompt is required")
	}
	switch strings.ToLower(strings.TrimSpace(f.Runtime)) {
	case "", string(RuntimeLocal):
		if f.Runtime == "" {
			f.Runtime = string(RuntimeLocal)
		}
	case string(RuntimeTemporal):
	default:
		return fmt.Errorf("runtime must be %q or %q", RuntimeLocal, RuntimeTemporal)
	}
	if f.Agent.ToolCount <= 0 && !f.Memory.Enabled {
		f.Agent.ToolCount = DefaultToolCount
	}
	if f.Agent.Name == "" {
		f.Agent.Name = DefaultAgentName
	}
	if f.Agent.SystemPrompt == "" {
		f.Agent.SystemPrompt = DefaultSystemPrompt
	}
	if f.Temporal.TaskQueue == "" {
		f.Temporal.TaskQueue = "eval-harness"
	}
	if f.Temporal.Port == 0 {
		f.Temporal.Port = 7233
	}
	if f.Temporal.Host == "" {
		f.Temporal.Host = "localhost"
	}
	if f.Temporal.Namespace == "" {
		f.Temporal.Namespace = "default"
	}
	if f.Memory.Enabled {
		if _, err := ParseMemoryStoreMode(f.Memory.StoreMode); err != nil {
			return err
		}
		if strings.EqualFold(strings.TrimSpace(f.Memory.Scenario), MemoryScenarioStoreRecall) {
			if strings.TrimSpace(f.Memory.StorePrompt) == "" {
				return fmt.Errorf("memory.store_prompt is required when memory.scenario is %q", MemoryScenarioStoreRecall)
			}
			if strings.TrimSpace(f.Memory.RecallPrompt) == "" {
				return fmt.Errorf("memory.recall_prompt is required when memory.scenario is %q", MemoryScenarioStoreRecall)
			}
		}
	}
	return nil
}
