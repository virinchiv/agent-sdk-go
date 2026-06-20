// Example agent using PostgreSQL pgvector for long-term memory.
//
// Run from examples/ (no args = two-turn store then recall demo):
//
//	go run ./agent_with_memory/pgvector
//	MEMORY_STORE_MODE=always go run ./agent_with_memory/pgvector
package main

import (
	"context"
	"fmt"
	"log"

	examplecfg "github.com/agenticenv/agent-sdk-go/examples"
	"github.com/agenticenv/agent-sdk-go/examples/agent_with_memory/common"
	"github.com/agenticenv/agent-sdk-go/pkg/agent"
	pgmem "github.com/agenticenv/agent-sdk-go/pkg/memory/pgvector"
)

func main() {
	cfg := examplecfg.LoadFromEnv()
	memCfg, err := common.LoadSettings()
	if err != nil {
		log.Fatalf("memory config: %v", err)
	}
	if memCfg.PGDSN == "" {
		log.Fatal("PGVECTOR_DSN is required for the pgvector memory example; see ../README.md")
	}
	if err := common.ValidateEmbeddingConfig(memCfg); err != nil {
		log.Fatalf("embedding config: %v", err)
	}

	llmClient, err := examplecfg.NewLLMClientFromConfig(cfg)
	if err != nil {
		log.Fatalf("failed to create LLM client: %v", err)
	}
	logr := examplecfg.NewLoggerFromLogConfig(cfg)

	embed, err := common.OpenAIEmbedFunc(memCfg)
	if err != nil {
		log.Fatalf("embed func: %v", err)
	}

	store, err := pgmem.NewMemory(embed,
		pgmem.WithDSN(memCfg.PGDSN),
		pgmem.WithTable(memCfg.PGMemoryTable),
		pgmem.WithEmbeddingCol(memCfg.PGEmbeddingCol),
		pgmem.WithDefaultLimit(memCfg.RecallLimit),
		pgmem.WithDefaultMinScore(memCfg.RecallMinScore),
		pgmem.WithLogger(logr),
	)
	if err != nil {
		log.Fatalf("pgvector memory: %v", err)
	}

	memoryConfig := common.MemoryConfig(store, memCfg, memCfg.StoreMode)
	opts := common.AgentOptions(cfg, llmClient, logr, memCfg, memoryConfig, "pgvector")

	a, err := agent.NewAgent(opts...)
	if err != nil {
		log.Fatal(examplecfg.FormatNewAgentError("failed to create agent", err))
	}
	defer a.Close()

	fmt.Printf("backend: pgvector  table: %s  user: %s  store: %s  recall: %v  limit: %d\n",
		memCfg.PGMemoryTable, memCfg.UserID, memCfg.StoreMode, memCfg.RecallEnabled, memCfg.RecallLimit)
	fmt.Println("hint:", common.StoreModeHint(memCfg.StoreMode))

	common.RunFromArgs(context.Background(), a, memCfg.UserID, memCfg.StoreMode)
}
