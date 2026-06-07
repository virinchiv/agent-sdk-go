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
	b.WriteString("=== Benchmark Report ===\n")
	b.WriteString(fmt.Sprintf("Runtime          : %s\n", cfg.Runtime))
	b.WriteString(fmt.Sprintf("Concurrent       : %t\n", cfg.Agent.Concurrent))
	if cfg.Agent.Concurrent {
		b.WriteString(fmt.Sprintf("Concurrent count : %d\n", cfg.Agent.ConcurrentCount))
	}
	if cfg.UseTemporal() {
		b.WriteString(fmt.Sprintf("External workers : %d\n", cfg.Temporal.WorkersCount))
	}
	b.WriteString(fmt.Sprintf("Total runs       : %d\n", metrics.TotalRuns))
	b.WriteString(fmt.Sprintf("Tools            : %d (%s)\n", cfg.Agent.Tools.Count, cfg.Agent.Tools.Execution))
	b.WriteString(fmt.Sprintf("Sub-agents       : %d (levels %d)\n", cfg.Agent.Subagents.Count, cfg.Agent.Subagents.Levels))
	b.WriteString("---\n")
	b.WriteString(fmt.Sprintf("Latency p50 (ms) : %.2f\n", metrics.P50Ms))
	b.WriteString(fmt.Sprintf("Latency p95 (ms) : %.2f\n", metrics.P95Ms))
	b.WriteString(fmt.Sprintf("Latency p99 (ms) : %.2f\n", metrics.P99Ms))
	b.WriteString(fmt.Sprintf("Latency avg (ms) : %.2f\n", metrics.AvgMs))
	b.WriteString(fmt.Sprintf("Heap alloc (B)   : %d\n", metrics.HeapAllocBytes))
	b.WriteString(fmt.Sprintf("Total alloc (B)  : %d\n", metrics.TotalAllocBytes))
	b.WriteString(fmt.Sprintf("CPU time (ms)    : %.2f\n", metrics.CPUTimeMs))
	b.WriteString(fmt.Sprintf("Input tokens     : %d\n", metrics.TotalInputTokens))
	b.WriteString(fmt.Sprintf("Output tokens    : %d\n", metrics.TotalOutputTokens))
	b.WriteString(fmt.Sprintf("Est. cost (USD)  : %.4f  # pricing placeholder\n", metrics.EstCostUSD))
	b.WriteString(fmt.Sprintf("Success rate (%%) : %.2f\n", metrics.SuccessRate))
	return b.String()
}
