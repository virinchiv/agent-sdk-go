package common

import (
	"fmt"

	excfg "github.com/agenticenv/agent-sdk-go/examples"
	"github.com/agenticenv/agent-sdk-go/pkg/agent"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
	"github.com/agenticenv/agent-sdk-go/pkg/memory"
)

// MemoryConfig builds [memory.Config] from settings, backend, and store mode.
func MemoryConfig(store interfaces.Memory, settings *Settings, mode memory.StoreMode) memory.Config {
	cfg := memory.DefaultConfig(store)
	cfg.Store.Mode = mode
	cfg.Recall = memory.RecallConfig{
		Enabled:  settings.RecallEnabled,
		Limit:    settings.RecallLimit,
		MinScore: settings.RecallMinScore,
	}
	return cfg
}

// AgentOptions builds shared agent options: runtime, LLM, memory, and system prompt.
func AgentOptions(
	cfg *excfg.Config,
	llmClient interfaces.LLMClient,
	log logger.Logger,
	settings *Settings,
	memCfg memory.Config,
	backendLabel string,
) []agent.Option {
	recallNote := "recall enabled before each run"
	if !settings.RecallEnabled {
		recallNote = "store-only (recall disabled)"
	}
	prompt := fmt.Sprintf(
		"You are a helpful assistant with long-term memory backed by %s (%s). "+
			"When the system prompt includes a Relevant Memories section, treat those as facts from prior runs and answer from them.",
		backendLabel,
		recallNote,
	)
	if memCfg.Store.Mode == memory.StoreModeOnDemand {
		prompt += " When the user asks you to remember something for future runs, persist it with your tools before acknowledging."
	}
	opts := []agent.Option{
		agent.WithName(fmt.Sprintf("agent-with-memory-%s", backendLabel)),
		agent.WithDescription(fmt.Sprintf("Agent with %s long-term memory", backendLabel)),
		agent.WithSystemPrompt(prompt),
		agent.WithLLMClient(llmClient),
		agent.WithLogger(log),
		agent.WithMemory(memCfg),
		agent.WithToolApprovalPolicy(agent.AutoToolApprovalPolicy()),
	}
	return append(opts, excfg.RuntimeOption(cfg)...)
}
