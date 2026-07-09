// Package main — sample WorkflowRunner backed by Temporal.
//
// This file is the Temporal-specific orchestration layer for the agent_with_workflows
// example. It satisfies the WorkflowRunner interface defined in workflow_tool.go.
//
// To use a different engine (Conductor, Argo Workflows, Step Functions, in-process),
// add a sibling file (e.g. conductor_runner.go) implementing WorkflowRunner and
// wire it in main.go instead of NewTemporalRunner.
package main

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"time"

	config "github.com/agenticenv/agent-sdk-go/examples"
	"github.com/agenticenv/agent-sdk-go/internal/runtime/temporal"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	sdklog "go.temporal.io/sdk/log"
	sdktemporal "go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

const orchestrationQueueSuffix = "-workflows"

// NewTemporalRunner returns a WorkflowRunner that executes workflows via Temporal.
// It also starts an in-process Temporal worker for the sample orchestration queue.
// The returned stop function shuts down the worker and closes the Temporal client.
func NewTemporalRunner(cfg *config.Config, logger sdklog.Logger) (WorkflowRunner, func(), error) {
	hostPort := cfg.Host + ":" + strconv.Itoa(cfg.Port)
	tc, err := client.Dial(client.Options{
		HostPort:  hostPort,
		Namespace: cfg.Namespace,
		Logger:    logger,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("temporal client: %w", err)
	}

	queue := cfg.TaskQueue + orchestrationQueueSuffix
	stop := startOrchestrationWorker(tc, queue)

	return &temporalRunner{client: tc, taskQueue: queue}, func() {
		stop()
		tc.Close()
	}, nil
}

type temporalRunner struct {
	client    client.Client
	taskQueue string
}

// Run starts a SampleWorkflow execution and blocks until it completes.
// The ctx deadline propagates — if it fires, Run returns context.DeadlineExceeded
// and the Temporal workflow may still be running.
func (r *temporalRunner) Run(ctx context.Context, name, input string) (WorkflowResult, error) {
	workflowID := fmt.Sprintf("workflow-%s-%s", name, strings.ReplaceAll(input, " ", "-"))
	run, err := r.client.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: r.taskQueue,
	}, SampleWorkflow, sampleWorkflowInput{
		Name:  name,
		Input: input,
	})
	if err != nil {
		return WorkflowResult{}, fmt.Errorf("start workflow: %w", err)
	}

	var out WorkflowResult
	if err := run.Get(ctx, &out); err != nil {
		return WorkflowResult{}, fmt.Errorf("workflow failed: %w", err)
	}
	return out, nil
}

type sampleWorkflowInput struct {
	Name  string `json:"name"`
	Input string `json:"input"`
}

// SampleWorkflow is a Temporal workflow function that dispatches to named workflow steps.
// It represents sample user-defined orchestration logic — replace with your own procedures.
// Steps run deterministically: each activity executes in order, retries on failure,
// and the workflow can survive worker restarts via Temporal's event-sourced execution.
func SampleWorkflow(ctx workflow.Context, in sampleWorkflowInput) (WorkflowResult, error) {
	ao := workflow.ActivityOptions{
		StartToCloseTimeout: 30 * time.Second,
		RetryPolicy: &sdktemporal.RetryPolicy{
			MaximumAttempts: 3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Step 1: validate input before executing any side effects.
	var validated string
	if err := workflow.ExecuteActivity(ctx, validateInput, in).Get(ctx, &validated); err != nil {
		return WorkflowResult{}, err
	}

	var steps []string
	switch strings.ToLower(strings.TrimSpace(in.Name)) {
	case "onboarding":
		// Steps run in order; each is retried independently on failure.
		var step string
		if err := workflow.ExecuteActivity(ctx, onboardingWelcome, in.Input).Get(ctx, &step); err != nil {
			return WorkflowResult{}, err
		}
		steps = append(steps, step)
		if err := workflow.ExecuteActivity(ctx, onboardingProvision, in.Input).Get(ctx, &step); err != nil {
			return WorkflowResult{}, err
		}
		steps = append(steps, step)
	case "status_check":
		var step string
		if err := workflow.ExecuteActivity(ctx, statusCheck, in.Input).Get(ctx, &step); err != nil {
			return WorkflowResult{}, err
		}
		steps = append(steps, step)
	default:
		return WorkflowResult{}, fmt.Errorf("unknown workflow %q (supported: onboarding, status_check)", in.Name)
	}

	return WorkflowResult{
		Workflow: in.Name,
		Status:   "completed",
		Steps:    steps,
		Summary:  fmt.Sprintf("workflow %q finished with %d step(s)", in.Name, len(steps)),
	}, nil
}

func validateInput(ctx context.Context, in sampleWorkflowInput) (string, error) {
	activity.GetLogger(ctx).Info("validating workflow input", "name", in.Name, "input", in.Input)
	if strings.TrimSpace(in.Name) == "" {
		return "", fmt.Errorf("workflow name is required")
	}
	if strings.TrimSpace(in.Input) == "" {
		return "", fmt.Errorf("workflow input is required")
	}
	return fmt.Sprintf("validated %s for %q", in.Input, in.Name), nil
}

func onboardingWelcome(ctx context.Context, subject string) (string, error) {
	activity.GetLogger(ctx).Info("onboarding: welcome", "subject", subject)
	return fmt.Sprintf("welcome email sent to %s", strings.TrimSpace(subject)), nil
}

func onboardingProvision(ctx context.Context, subject string) (string, error) {
	activity.GetLogger(ctx).Info("onboarding: provision", "subject", subject)
	return fmt.Sprintf("workspace provisioned for %s", strings.TrimSpace(subject)), nil
}

func statusCheck(ctx context.Context, target string) (string, error) {
	activity.GetLogger(ctx).Info("status check", "target", target)
	return fmt.Sprintf("status for %s: healthy", strings.TrimSpace(target)), nil
}

// startOrchestrationWorker starts an in-process Temporal worker for the sample engine.
// In production your orchestration worker runs as a separate process or service.
func startOrchestrationWorker(tc client.Client, taskQueue string) func() {
	w := worker.New(tc, taskQueue, worker.Options{})
	w.RegisterWorkflow(SampleWorkflow)
	w.RegisterActivity(validateInput)
	w.RegisterActivity(onboardingWelcome)
	w.RegisterActivity(onboardingProvision)
	w.RegisterActivity(statusCheck)

	errCh := make(chan error, 1)
	go func() {
		if err := w.Run(worker.InterruptCh()); err != nil {
			errCh <- err
		}
		close(errCh)
	}()

	return func() {
		w.Stop()
		if err := <-errCh; err != nil {
			log.Printf("orchestration worker stopped: %v", err)
		}
	}
}

// temporalLogAdapter bridges the example logger to the Temporal SDK logger interface.
func temporalLogAdapter(cfg *config.Config) sdklog.Logger {
	return temporal.NewLogAdapter(config.NewLoggerFromLogConfig(cfg))
}
