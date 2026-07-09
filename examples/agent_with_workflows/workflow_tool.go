// Package main implements a WorkflowTool that lets the LLM invoke deterministic
// user-defined workflows inside an agent run. The tool contract (name, parameters,
// WorkflowRunner interface) is engine-agnostic — execution is handled by whatever
// WorkflowRunner is wired in main.go (see temporal_runner.go for a Temporal sample).
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/tools"
)

var _ interfaces.Tool = (*WorkflowTool)(nil)

// WorkflowRunner is the engine-agnostic interface for executing a named workflow.
// Implement one runner per orchestration backend — Temporal, Conductor, Argo Workflows,
// Step Functions, or in-process. The WorkflowTool delegates to this interface so the
// LLM-facing tool contract never changes when you swap backends.
type WorkflowRunner interface {
	Run(ctx context.Context, name, input string) (WorkflowResult, error)
}

// WorkflowResult is returned to the LLM after a workflow completes.
// The LLM uses Summary to produce a user-facing reply.
type WorkflowResult struct {
	Workflow string   `json:"workflow"`
	Status   string   `json:"status"`
	Steps    []string `json:"steps"`
	Summary  string   `json:"summary"`
}

// WorkflowTool exposes deterministic user workflows to the LLM as the run_workflow tool.
// Execute blocks until WorkflowRunner.Run returns — size agent and tool timeouts accordingly.
type WorkflowTool struct {
	runner WorkflowRunner
}

// NewWorkflowTool returns a WorkflowTool backed by the given runner.
func NewWorkflowTool(runner WorkflowRunner) *WorkflowTool {
	return &WorkflowTool{runner: runner}
}

func (*WorkflowTool) Name() string { return "run_workflow" }

func (*WorkflowTool) DisplayName() string { return "Run Workflow" }

func (*WorkflowTool) Description() string {
	return "Run a predefined, deterministic workflow. " +
		"Use when the user asks to execute a named multi-step procedure such as onboarding, provisioning, or status checks. " +
		"Supported workflows: onboarding, status_check."
}

func (*WorkflowTool) Parameters() interfaces.JSONSchema {
	return tools.Params(
		map[string]interfaces.JSONSchema{
			"workflow_name": tools.ParamEnum(
				"Name of the workflow to run",
				"onboarding",
				"status_check",
			),
			"input": tools.ParamString(
				"Primary subject for the workflow (user name, service id, or target to act on)",
			),
		},
		"workflow_name", "input",
	)
}

func (t *WorkflowTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	name, _ := args["workflow_name"].(string)
	input, _ := args["input"].(string)
	name = strings.TrimSpace(name)
	input = strings.TrimSpace(input)
	if name == "" {
		return nil, fmt.Errorf("workflow_name is required")
	}
	if input == "" {
		return nil, fmt.Errorf("input is required")
	}
	// Blocks until the workflow reaches a terminal state or ctx deadline fires.
	return t.runner.Run(ctx, name, input)
}
