package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/agenticenv/agent-sdk-go/benchmarks/setup"
)

func writeReport(cfg *setup.Config, metrics *BenchmarkMetrics, repoRoot string) error {
	if cfg.Output.Console {
		printReport(cfg, metrics)
	}
	if !cfg.Output.File {
		return nil
	}

	dir := cfg.OutputDir(repoRoot)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	ext := "json"
	if strings.EqualFold(cfg.Output.Format, "text") {
		ext = "txt"
	}
	filename := fmt.Sprintf("benchmark_%s.%s", time.Now().Format("2006-01-02_15-04-05"), ext)
	path := filepath.Join(dir, filename)

	content, err := formatReport(cfg, metrics)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("write report: %w", err)
	}
	return nil
}

func printReport(cfg *setup.Config, metrics *BenchmarkMetrics) {
	content, err := formatReport(cfg, metrics)
	if err != nil {
		fmt.Printf("failed to format report: %v\n", err)
		return
	}
	fmt.Print(string(content))
}

func formatReport(cfg *setup.Config, metrics *BenchmarkMetrics) ([]byte, error) {
	if strings.EqualFold(cfg.Output.Format, "text") {
		return []byte(formatTextReport(cfg, metrics)), nil
	}
	payload := map[string]any{
		"runtime":      cfg.Runtime,
		"config":       cfg,
		"metrics":      metrics,
		"generated_at": time.Now().UTC().Format(time.RFC3339),
	}
	return json.MarshalIndent(payload, "", "  ")
}

func formatTextReport(cfg *setup.Config, metrics *BenchmarkMetrics) string {
	var b strings.Builder
	fmt.Fprintf(&b, "=== Benchmark Report ===\n")
	fmt.Fprintf(&b, "Runtime          : %s\n", cfg.Runtime)
	fmt.Fprintf(&b, "Concurrent       : %t\n", cfg.Agent.Concurrent)
	if cfg.Agent.Concurrent {
		fmt.Fprintf(&b, "Concurrent count : %d\n", cfg.Agent.ConcurrentCount)
	}
	if cfg.UseTemporal() {
		fmt.Fprintf(&b, "External workers : %d\n", cfg.Temporal.WorkersCount)
	}
	fmt.Fprintf(&b, "Total runs       : %d\n", metrics.TotalRuns)
	fmt.Fprintf(&b, "Tools            : %d (%s)\n", cfg.Agent.Tools.Count, cfg.Agent.Tools.Execution)
	fmt.Fprintf(&b, "Sub-agents       : %d (levels %d)\n", cfg.Agent.Subagents.Count, cfg.Agent.Subagents.Levels)
	fmt.Fprintf(&b, "---\n")
	fmt.Fprintf(&b, "Latency p50 (ms) : %.2f\n", metrics.P50Ms)
	fmt.Fprintf(&b, "Latency p95 (ms) : %.2f\n", metrics.P95Ms)
	fmt.Fprintf(&b, "Latency p99 (ms) : %.2f\n", metrics.P99Ms)
	fmt.Fprintf(&b, "Latency avg (ms) : %.2f\n", metrics.AvgMs)
	fmt.Fprintf(&b, "Heap alloc (B)   : %d\n", metrics.HeapAllocBytes)
	fmt.Fprintf(&b, "Total alloc (B)  : %d\n", metrics.TotalAllocBytes)
	fmt.Fprintf(&b, "CPU time (ms)    : %.2f\n", metrics.CPUTimeMs)
	fmt.Fprintf(&b, "Input tokens     : %d\n", metrics.TotalInputTokens)
	fmt.Fprintf(&b, "Output tokens    : %d\n", metrics.TotalOutputTokens)
	fmt.Fprintf(&b, "Est. cost (USD)  : %.4f  # pricing placeholder\n", metrics.EstCostUSD)
	fmt.Fprintf(&b, "Success rate (%%) : %.2f\n", metrics.SuccessRate)
	if cfg.MemoryEnabled() {
		fmt.Fprintf(&b, "Memory recalls   : %d\n", metrics.TotalMemoryRecalls)
		fmt.Fprintf(&b, "Memory stores    : %d\n", metrics.TotalMemoryStores)
	}
	return b.String()
}
