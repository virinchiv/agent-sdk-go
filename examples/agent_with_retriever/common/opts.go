package common

import (
	"fmt"

	"github.com/agenticenv/agent-sdk-go/pkg/agent"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
)

// AgentOptions builds shared agent options: Temporal, LLM, retriever mode, and system prompt.
// backendLabel is shown in the agent name/description (e.g. "weaviate" or "pgvector").
func AgentOptions(
	host string,
	port int,
	namespace, taskQueue string,
	llmClient interfaces.LLMClient,
	log logger.Logger,
	settings *Settings,
	backendLabel string,
) []agent.Option {
	mode := settings.RetrieverMode
	prompt := fmt.Sprintf(
		"You are a helpful assistant with access to a %s knowledge base (%s mode). "+
			"Use retrieved documents to answer questions accurately. "+
			"When in agentic or hybrid mode, call the retriever tool when you need facts from the knowledge base. "+
			"Cite sources when possible.",
		backendLabel,
		mode,
	)
	return []agent.Option{
		agent.WithName(fmt.Sprintf("agent-with-retriever-%s", backendLabel)),
		agent.WithDescription(fmt.Sprintf("Agent with %s retriever (%s)", backendLabel, mode)),
		agent.WithSystemPrompt(prompt),
		agent.WithTemporalConfig(&agent.TemporalConfig{
			Host:      host,
			Port:      port,
			Namespace: namespace,
			TaskQueue: taskQueue,
		}),
		agent.WithLLMClient(llmClient),
		agent.WithLogger(log),
		agent.WithRetrieverMode(mode),
		agent.WithToolApprovalPolicy(agent.AutoToolApprovalPolicy()),
	}
}
