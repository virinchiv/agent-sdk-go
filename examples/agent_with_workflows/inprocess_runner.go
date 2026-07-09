package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// inProcessRunner is a WorkflowRunner that executes workflow steps directly in-process.
// It needs no external infrastructure — use it to understand the pattern without Temporal.
// For production durable execution swap it for temporal_runner.go (ORCHESTRATION_ENGINE=temporal).
type inProcessRunner struct{}

func newInProcessRunner() WorkflowRunner { return &inProcessRunner{} }

func (r *inProcessRunner) Run(_ context.Context, name, input string) (WorkflowResult, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "onboarding":
		return r.runOnboarding(input)
	case "status_check":
		return r.runStatusCheck(input)
	default:
		return WorkflowResult{}, fmt.Errorf("unknown workflow %q (supported: onboarding, status_check)", name)
	}
}

func (r *inProcessRunner) runOnboarding(subject string) (WorkflowResult, error) {
	// Simulate deterministic step sequence: validate → welcome → provision.
	// Each step runs in fixed order; results accumulate into the final summary.
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return WorkflowResult{}, fmt.Errorf("subject is required for onboarding workflow")
	}

	// Simulate step latency (in a real runner this is real I/O or an RPC wait).
	time.Sleep(100 * time.Millisecond)
	steps := []string{
		fmt.Sprintf("welcome email sent to %s", subject),
		fmt.Sprintf("workspace provisioned for %s", subject),
	}
	return WorkflowResult{
		Workflow: "onboarding",
		Status:   "completed",
		Steps:    steps,
		Summary:  fmt.Sprintf("onboarding for %s completed: %d steps", subject, len(steps)),
	}, nil
}

func (r *inProcessRunner) runStatusCheck(target string) (WorkflowResult, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return WorkflowResult{}, fmt.Errorf("target is required for status_check workflow")
	}

	time.Sleep(50 * time.Millisecond)
	steps := []string{fmt.Sprintf("status for %s: healthy", target)}
	return WorkflowResult{
		Workflow: "status_check",
		Status:   "completed",
		Steps:    steps,
		Summary:  fmt.Sprintf("status check for %s: healthy", target),
	}, nil
}
