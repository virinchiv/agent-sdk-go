// Package main — SandboxRuntime backed by Docker.
//
// This file is the Docker-specific execution layer for the agent_with_code_execution
// example. It satisfies the SandboxRuntime interface defined in code_tool.go.
//
// To use a different sandbox (AWS Lambda, Modal, E2B, Firecracker, etc.), add a sibling
// file implementing SandboxRuntime and wire it in main.go instead of newDockerRunner.
//
// Requires: Docker daemon running and internet access to pull language images on first run.
package main

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

// dockerRunner executes code inside an ephemeral Docker container.
// Each Execute call starts a fresh container, runs the code, and removes the container.
// This gives the LLM a clean, isolated environment with no state carried between calls.
type dockerRunner struct{}

// dockerImages maps languages to official Docker images used as execution sandboxes.
// Swap these for pinned internal images in production to avoid pulling from Docker Hub.
var dockerImages = map[string]struct {
	image string
	flag  string // -c for shell interpreters, -e for node
}{
	"python":     {image: "python:3-slim", flag: "-c"},
	"javascript": {image: "node:alpine", flag: "-e"},
	"shell":      {image: "alpine", flag: "-c"},
}

// dockerInterpreter maps languages to their in-container interpreter binaries.
var dockerInterpreter = map[string]string{
	"python":     "python3",
	"javascript": "node",
	"shell":      "sh",
}

func newDockerRunner() (SandboxRuntime, error) {
	// Verify Docker is available before returning the runner.
	if _, err := exec.LookPath("docker"); err != nil {
		return nil, fmt.Errorf("docker not found on PATH — install Docker or use the default local sandbox")
	}
	return &dockerRunner{}, nil
}

// Execute runs code in a sandboxed Docker container and returns combined output.
// The container is removed automatically (--rm). The ctx deadline propagates via
// exec.CommandContext so a slow container does not outlive the agent tool timeout.
func (r *dockerRunner) Execute(ctx context.Context, language, code string) (ExecutionResult, error) {
	cfg, ok := dockerImages[language]
	if !ok {
		return ExecutionResult{}, fmt.Errorf("unsupported language %q (supported: python, javascript, shell)", language)
	}
	interpreter := dockerInterpreter[language]

	// docker run --rm <image> <interpreter> <flag> <code>
	// --rm:      remove container after exit
	// --network none: no outbound network access from LLM-generated code
	// --memory:  cap memory to prevent runaway scripts
	args := []string{
		"run", "--rm",
		"--network", "none",
		"--memory", "128m",
		cfg.image,
		interpreter, cfg.flag, code,
	}

	var stdout, stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, "docker", args...)
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

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
			return ExecutionResult{}, fmt.Errorf("docker exec: %w", runErr)
		}
	}

	return ExecutionResult{
		Language: language,
		Output:   combined,
		ExitCode: exitCode,
	}, nil
}
