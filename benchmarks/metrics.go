package main

import (
	"math"
	"runtime"
	"sort"
)

func aggregateMetrics(outcomes []runOutcome, memBefore, memAfter runtime.MemStats, cpuMs float64, inputTokens, outputTokens int) *BenchmarkMetrics {
	latencies := make([]float64, 0, len(outcomes))
	successes := 0
	var totalRecalls, totalStores int64
	for _, o := range outcomes {
		latencies = append(latencies, o.latencyMs)
		if o.success {
			successes++
		}
		totalRecalls += o.memoryRecalls
		totalStores += o.memoryStores
	}
	sort.Float64s(latencies)

	totalRuns := len(outcomes)
	successRate := 0.0
	if totalRuns > 0 {
		successRate = float64(successes) / float64(totalRuns) * 100
	}

	return &BenchmarkMetrics{
		P50Ms:              percentile(latencies, 50),
		P95Ms:              percentile(latencies, 95),
		P99Ms:              percentile(latencies, 99),
		AvgMs:              average(latencies),
		HeapAllocBytes:     deltaUint64(memAfter.Alloc, memBefore.Alloc),
		TotalAllocBytes:    deltaUint64(memAfter.TotalAlloc, memBefore.TotalAlloc),
		CPUTimeMs:          cpuMs,
		TotalInputTokens:   inputTokens,
		TotalOutputTokens:  outputTokens,
		EstCostUSD:         0, // pricing to be defined later
		TotalRuns:          totalRuns,
		SuccessRate:        successRate,
		TotalMemoryRecalls: totalRecalls,
		TotalMemoryStores:  totalStores,
	}
}

func percentile(sorted []float64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	if len(sorted) == 1 {
		return sorted[0]
	}
	rank := (p / 100) * float64(len(sorted)-1)
	lower := int(math.Floor(rank))
	upper := int(math.Ceil(rank))
	if lower == upper {
		return sorted[lower]
	}
	weight := rank - float64(lower)
	return sorted[lower]*(1-weight) + sorted[upper]*weight
}

func average(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	sum := 0.0
	for _, v := range values {
		sum += v
	}
	return sum / float64(len(values))
}

func deltaUint64(after, before uint64) uint64 {
	if after >= before {
		return after - before
	}
	return after
}
