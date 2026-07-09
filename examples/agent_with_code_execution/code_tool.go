// Package main implements a CodeTool that lets the LLM write and execute code inside
// an agent run. The tool contract (name, parameters, SandboxRuntime interface) is
// sandbox-agnostic — execution is handled by whatever SandboxRuntime is wired in main.go.
//
// local_runner.go  — default runner using os/exec (needs Python or Node installed locally)
// docker_runner.go — isolated runner using Docker (SANDBOX_ENV=docker)
package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/tools"
)

var _ interfaces.Tool = (*CodeTool)(nil)

// SandboxRuntime is the sandbox-agnostic interface for executing LLM-generated code.
// Implement one runtime per execution environment — local os/exec, Docker, AWS Lambda,
// Modal, E2B, or any cloud sandbox API. The CodeTool delegates to this interface so the
// LLM-facing tool contract never changes when you swap sandboxes.
type SandboxRuntime interface {
	Execute(ctx context.Context, language, code string) (ExecutionResult, error)
}

// ExecutionResult is returned to the LLM after code executes.
// The LLM uses Output to answer the user and ExitCode to detect failures.
type ExecutionResult struct {
	Language string `json:"language"`
	Output   string `json:"output"`
	ExitCode int    `json:"exit_code"`
}

// CodeTool exposes sandboxed code execution to the LLM as the execute_code tool.
// Execute blocks until SandboxRuntime.Execute returns — size timeouts for your sandbox.
type CodeTool struct {
	sandbox SandboxRuntime
}

// NewCodeTool returns a CodeTool backed by the given sandbox runtime.
func NewCodeTool(sandbox SandboxRuntime) *CodeTool {
	return &CodeTool{sandbox: sandbox}
}

func (*CodeTool) Name() string { return "execute_code" }

func (*CodeTool) DisplayName() string { return "Execute Code" }

func (*CodeTool) Description() string {
	return "Write and execute a short code snippet in an isolated sandbox. " +
		"Use when the user asks to run, compute, or verify something with code. " +
		"Returns the program output (stdout + stderr) and exit code. " +
		"Supported languages: python, javascript, shell."
}

func (*CodeTool) Parameters() interfaces.JSONSchema {
	return tools.Params(
		map[string]interfaces.JSONSchema{
			"language": tools.ParamEnum(
				"Language to execute the code in",
				"python",
				"javascript",
				"shell",
			),
			"code": tools.ParamString(
				"The code to execute. Keep scripts self-contained — no file I/O or network calls.",
			),
		},
		"language", "code",
	)
}

func (t *CodeTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	language, _ := args["language"].(string)
	code, _ := args["code"].(string)
	language = strings.ToLower(strings.TrimSpace(language))
	code = strings.TrimSpace(code)
	if language == "" {
		return nil, fmt.Errorf("language is required")
	}
	if code == "" {
		return nil, fmt.Errorf("code is required")
	}
	// Blocks until sandbox completes or ctx deadline fires.
	return t.sandbox.Execute(ctx, language, code)
}
