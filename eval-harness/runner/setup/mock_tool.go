package setup

import (
	"context"
	"fmt"
	"math/rand"

	"github.com/agenticenv/agent-sdk-go/pkg/agent"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/tools"
)

const mockToolPrefix = "eval_tool_"

// MockTool is a mock tool for eval harness runs.
type MockTool struct {
	name string
	cfg  ToolConfig
	rng  *rand.Rand
}

func newMockTool(index int, cfg ToolConfig, rng *rand.Rand) *MockTool {
	return &MockTool{
		name: fmt.Sprintf("%s%d", mockToolPrefix, index),
		cfg:  cfg,
		rng:  rng,
	}
}

func (t *MockTool) Name() string { return t.name }

func (t *MockTool) DisplayName() string { return t.name }

func (t *MockTool) Description() string {
	return "Eval harness mock tool."
}

func (t *MockTool) Parameters() interfaces.JSONSchema {
	return tools.Params(map[string]interfaces.JSONSchema{
		"input": tools.ParamString("Input payload for the eval tool."),
	}, "input")
}

func (t *MockTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	input, _ := args["input"].(string)
	if input == "" {
		input = "eval"
	}
	return map[string]any{"tool": t.name, "input": input, "status": "ok"}, nil
}

// RegisterMockTools registers count mock tools on a new registry.
func RegisterMockTools(count int, cfg ToolConfig, rng *rand.Rand) agent.ToolRegistry {
	reg := agent.NewToolRegistry()
	for i := 1; i <= count; i++ {
		if err := reg.Register(newMockTool(i, cfg, rng)); err != nil {
			panic(err)
		}
	}
	return reg
}
