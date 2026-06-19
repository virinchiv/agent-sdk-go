package main

import (
	"context"
	"fmt"

	"github.com/agenticenv/agent-sdk-go/eval-harness/runner/setup"
	"github.com/agenticenv/agent-sdk-go/pkg/agent"
)

// Run executes one agent run with mock LLM and mock tools, then closes the agent.
func Run(ctx context.Context, cfg setup.Config) (*agent.AgentRunResult, error) {
	cfg.ApplyDefaults()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	a, err := setup.BuildAgent(cfg)
	if err != nil {
		return nil, err
	}
	defer a.Close()

	result, err := a.Run(ctx, cfg.UserPrompt, nil)
	if err != nil {
		return nil, fmt.Errorf("agent run: %w", err)
	}
	return result, nil
}
