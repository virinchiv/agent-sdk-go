package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/agenticenv/agent-sdk-go/benchmarks/setup"
)

type BenchmarkMetrics struct {
	P50Ms float64 `json:"p50_ms"`
	P95Ms float64 `json:"p95_ms"`
	P99Ms float64 `json:"p99_ms"`
	AvgMs float64 `json:"avg_ms"`

	HeapAllocBytes  uint64 `json:"heap_alloc_bytes"`
	TotalAllocBytes uint64 `json:"total_alloc_bytes"`

	CPUTimeMs float64 `json:"cpu_time_ms"`

	TotalInputTokens  int     `json:"total_input_tokens"`
	TotalOutputTokens int     `json:"total_output_tokens"`
	EstCostUSD        float64 `json:"est_cost_usd"`

	TotalRuns   int     `json:"total_runs"`
	SuccessRate float64 `json:"success_rate"`
}

func main() {
	configPath := flag.String("config", "", "path to benchmark config.yaml (default: benchmarks/config.yaml)")
	flag.Parse()

	cfg, err := setup.LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	repoRoot, err := setup.FindRepoRoot(".")
	if err != nil {
		log.Fatalf("find repo root: %v", err)
	}

	resolvedConfig := *configPath
	if resolvedConfig == "" {
		resolvedConfig = setup.DefaultConfigPath()
	}
	absConfig, err := filepath.Abs(resolvedConfig)
	if err != nil {
		log.Fatalf("resolve config path: %v", err)
	}

	lgr, closeLogger, err := setup.SetupAgentLogger(cfg, repoRoot)
	if err != nil {
		log.Fatalf("setup logger: %v", err)
	}
	defer closeLogger()

	stats := setup.NewLLMStats()
	runRng := setup.RunRNG()
	llm := setup.NewMockLLMClient(cfg.LLM, stats, runRng)

	fmt.Println("================================================================")
	fmt.Printf("Starting agent-sdk-go benchmark (%s runtime)\n", cfg.Runtime)
	fmt.Printf("Runs: %d  Concurrent: %t  Tools: %d  Sub-agents: %d (levels %d)\n",
		cfg.Agent.Runs, cfg.Agent.Concurrent, cfg.Agent.Tools.Count, cfg.Agent.Subagents.Count, cfg.Agent.Subagents.Levels)
	if cfg.UseTemporal() {
		fmt.Printf("External workers : %d\n", cfg.Temporal.WorkersCount)
	}
	if cfg.Logger.Enabled {
		fmt.Printf("Logger         : enabled (%s)\n", cfg.LogDir(repoRoot))
	}
	fmt.Println("================================================================")

	ctx := context.Background()

	var workers *externalWorkerManager
	if cfg.ExternalWorkersEnabled() {
		workers, err = startExternalWorkers(ctx, absConfig, repoRoot, cfg.Temporal.WorkersCount)
		if err != nil {
			log.Fatalf("start external workers: %v", err)
		}
		defer func() {
			if stopErr := workers.stop(); stopErr != nil {
				log.Printf("stop external workers: %v", stopErr)
			}
		}()
	}

	metrics, err := runBenchmark(ctx, cfg, llm, lgr, runRng)
	if err != nil {
		log.Fatalf("benchmark failed: %v", err)
	}

	if err := writeReport(cfg, metrics, repoRoot); err != nil {
		log.Fatalf("write report: %v", err)
	}

	if metrics.SuccessRate < 100 {
		os.Exit(1)
	}
}
