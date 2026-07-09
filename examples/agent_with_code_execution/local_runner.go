package main

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// localRunner is a SandboxRuntime that executes code directly on the host using os/exec.
// It needs no external infrastructure — use it to understand the pattern without Docker.
// For isolated execution swap it for docker_runner.go (SANDBOX_ENV=docker), AWS Lambda,
// Modal, E2B, or any remote sandbox that implements SandboxRuntime.
//
// Requirements: Python (python3) and/or Node.js (node) installed on the host.
type localRunner struct{}

func newLocalRunner() SandboxRuntime { return &localRunner{} }

func (r *localRunner) Execute(ctx context.Context, language, code string) (ExecutionResult, error) {
	binary, ext, err := resolveInterpreter(language)
	if err != nil {
		return ExecutionResult{}, err
	}

	// Write code to a temp file so stack traces show a real filename.
	tmpFile, err := os.CreateTemp("", "agent-code-*"+ext)
	if err != nil {
		return ExecutionResult{}, fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() { _ = os.Remove(tmpPath) }()

	if _, err := tmpFile.WriteString(code); err != nil {
		_ = tmpFile.Close()
		return ExecutionResult{}, fmt.Errorf("write code: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		return ExecutionResult{}, fmt.Errorf("close temp file: %w", err)
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, binary, tmpPath)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Dir = filepath.Dir(tmpPath)

	runErr := cmd.Run()

	combined := strings.TrimSpace(stdout.String())
	if stderr.Len() > 0 {
		if combined != "" {
			combined += "\n"
		}
		combined += strings.TrimSpace(stderr.String())
	}

	exitCode := 0
	if runErr != nil {
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			return ExecutionResult{}, fmt.Errorf("exec: %w", runErr)
		}
	}

	return ExecutionResult{
		Language: language,
		Output:   combined,
		ExitCode: exitCode,
	}, nil
}

// resolveInterpreter returns the host binary and temp file extension for a language.
func resolveInterpreter(language string) (binary, ext string, err error) {
	switch language {
	case "python":
		// Prefer python3; fall back to python.
		if path, e := exec.LookPath("python3"); e == nil {
			return path, ".py", nil
		}
		if path, e := exec.LookPath("python"); e == nil {
			return path, ".py", nil
		}
		return "", "", fmt.Errorf("python3 (or python) not found on PATH — install Python or use SANDBOX_ENV=docker")
	case "javascript":
		if path, e := exec.LookPath("node"); e == nil {
			return path, ".js", nil
		}
		return "", "", fmt.Errorf("node not found on PATH — install Node.js or use SANDBOX_ENV=docker")
	case "shell":
		if path, e := exec.LookPath("sh"); e == nil {
			return path, ".sh", nil
		}
		return "", "", fmt.Errorf("sh not found on PATH")
	default:
		return "", "", fmt.Errorf("unsupported language %q (supported: python, javascript, shell)", language)
	}
}
