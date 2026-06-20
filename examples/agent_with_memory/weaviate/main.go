// Example agent using Weaviate for long-term memory.
//
// Run from examples/ (no args = two-turn store then recall demo):
//
//	go run ./agent_with_memory/weaviate
//	MEMORY_STORE_MODE=always go run ./agent_with_memory/weaviate
package main

import (
	"context"
	"fmt"
	"log"

	examplecfg "github.com/agenticenv/agent-sdk-go/examples"
	"github.com/agenticenv/agent-sdk-go/examples/agent_with_memory/common"
	"github.com/agenticenv/agent-sdk-go/pkg/agent"
	wmem "github.com/agenticenv/agent-sdk-go/pkg/memory/weaviate"
)

func main() {
	cfg := examplecfg.LoadFromEnv()
	memCfg, err := common.LoadSettings()
	if err != nil {
		log.Fatalf("memory config: %v", err)
	}

	llmClient, err := examplecfg.NewLLMClientFromConfig(cfg)
	if err != nil {
		log.Fatalf("failed to create LLM client: %v", err)
	}
	logr := examplecfg.NewLoggerFromLogConfig(cfg)

	wOpts := []wmem.Option{
		wmem.WithHost(memCfg.WeaviateHost),
		wmem.WithScheme(memCfg.WeaviateScheme),
		wmem.WithClassName(memCfg.WeaviateMemoryClass),
		wmem.WithDefaultLimit(memCfg.RecallLimit),
		wmem.WithDefaultMinScore(memCfg.RecallMinScore),
		wmem.WithLogger(logr),
	}

	store, err := wmem.NewMemory(wOpts...)
	if err != nil {
		log.Fatalf("weaviate memory: %v", err)
	}

	memoryConfig := common.MemoryConfig(store, memCfg, memCfg.StoreMode)
	opts := common.AgentOptions(cfg, llmClient, logr, memCfg, memoryConfig, "weaviate")

	a, err := agent.NewAgent(opts...)
	if err != nil {
		log.Fatal(examplecfg.FormatNewAgentError("failed to create agent", err))
	}
	defer a.Close()

	fmt.Printf("backend: weaviate  class: %s  user: %s  store: %s  recall: %v  limit: %d\n",
		memCfg.WeaviateMemoryClass, memCfg.UserID, memCfg.StoreMode, memCfg.RecallEnabled, memCfg.RecallLimit)
	fmt.Println("hint:", common.StoreModeHint(memCfg.StoreMode))

	common.RunFromArgs(context.Background(), a, memCfg.UserID, memCfg.StoreMode)
}
