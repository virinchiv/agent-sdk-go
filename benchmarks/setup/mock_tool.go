package setup

import (
	"context"
	"fmt"
	"math/rand"

	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/tools"
)

const BenchmarkToolPrefix = "benchmark_tool_"

type MockBenchmarkTool struct {
	name string
	cfg  ToolConfig
	rng  *rand.Rand
}

func NewMockBenchmarkTool(index int, cfg ToolConfig, rng *rand.Rand) *MockBenchmarkTool {
	return &MockBenchmarkTool{
		name: fmt.Sprintf("%s%d", BenchmarkToolPrefix, index),
		cfg:  cfg,
		rng:  rng,
	}
}

func (t *MockBenchmarkTool) Name() string { return t.name }

func (t *MockBenchmarkTool) DisplayName() string { return t.name }

func (t *MockBenchmarkTool) Description() string {
	return "Benchmark mock tool for load testing."
}

func (t *MockBenchmarkTool) Parameters() interfaces.JSONSchema {
	return tools.Params(map[string]interfaces.JSONSchema{
		"input": tools.ParamString("Input payload for the benchmark tool."),
	}, "input")
}

func (t *MockBenchmarkTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	if err := sleepWithJitter(ctx, t.cfg.LatencyMs, t.cfg.JitterMs, t.rng); err != nil {
		return nil, err
	}
	input, _ := args["input"].(string)
	if input == "" {
		input = "benchmark"
	}
	return map[string]any{"tool": t.name, "input": input, "status": "ok"}, nil
}

func RegisterBenchmarkTools(count int, cfg ToolConfig, rng *rand.Rand) *tools.Registry {
	reg := tools.NewRegistry()
	for i := 1; i <= count; i++ {
		reg.Register(NewMockBenchmarkTool(i, cfg, rng))
	}
	return reg
}
