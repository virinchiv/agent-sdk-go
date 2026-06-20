package setup

import (
	"fmt"
	"math/rand"
	"time"

	"github.com/agenticenv/agent-sdk-go/pkg/agent"
)

const evalRNGSeed int64 = 42

// BuildAgent constructs an agent from cfg using mock LLM and tools when not overridden.
func BuildAgent(cfg Config) (*agent.Agent, error) {
	rng := rand.New(rand.NewSource(evalRNGSeed))

	llmClient := cfg.LLMClient
	if llmClient == nil {
		llmClient = NewMockLLMClient(cfg.LLM, rng)
	}

	toolRegistry := cfg.ToolRegistry
	if toolRegistry == nil {
		toolRegistry = RegisterMockTools(cfg.ToolCount, cfg.Tool, rng)
	}

	opts := []agent.Option{
		agent.WithName(cfg.AgentName),
		agent.WithDescription("Eval harness agent for single-run testing."),
		agent.WithSystemPrompt(cfg.SystemPrompt),
		agent.WithLLMClient(llmClient),
		agent.WithToolRegistry(toolRegistry),
		agent.WithToolApprovalPolicy(agent.AutoToolApprovalPolicy()),
		agent.WithLogger(cfg.Logger),
	}
	if cfg.UseTemporal() {
		opts = append(opts, agent.WithTemporalConfig(&agent.TemporalConfig{
			Host:      cfg.Temporal.Host,
			Port:      cfg.Temporal.Port,
			Namespace: cfg.Temporal.Namespace,
			TaskQueue: cfg.Temporal.TaskQueue,
		}))
	}

	memOpt, err := MemoryAgentOption(cfg)
	if err != nil {
		return nil, fmt.Errorf("memory option: %w", err)
	}
	if memOpt != nil {
		opts = append(opts, memOpt)
	}

	a, err := agent.NewAgent(opts...)
	if err != nil {
		return nil, fmt.Errorf("new agent: %w", err)
	}
	if cfg.UseTemporal() {
		time.Sleep(300 * time.Millisecond)
	}
	return a, nil
}
