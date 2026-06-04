package agent

import (
	"context"
	"fmt"
	"time"

	"log/slog"

	"github.com/agenticenv/agent-sdk-go/internal/runtime"
)

// AgentWorker runs the execution runtime's worker for an agent (polls the task queue and executes runs).
type AgentWorker struct {
	agentConfig
	runtime runtime.Runtime
}

// NewAgentWorker creates an AgentWorker that polls and executes runs for the configured backend.
// Same options as [NewAgent]. Use when the agent is created with [DisableLocalWorker].
// AgentWorker requires a Temporal backend (WithTemporalConfig or WithTemporalClient).
func NewAgentWorker(opts ...Option) (*AgentWorker, error) {
	cfg, err := buildAgentConfig(opts)
	if err != nil {
		return nil, err
	}
	if !cfg.hasTemporalRuntime() {
		return nil, fmt.Errorf("AgentWorker requires a Temporal backend: use WithTemporalConfig or WithTemporalClient")
	}
	cfg.remoteWorker = true
	if cfg.disableFingerprintCheck {
		return nil, fmt.Errorf("WithDisableFingerprintCheck is not allowed on AgentWorker (remote worker process)")
	}
	rt, err := cfg.buildAgentRuntime(true)
	if err != nil {
		return nil, err
	}
	return &AgentWorker{agentConfig: *cfg, runtime: rt}, nil
}

// Start starts the worker (blocks until Stop is called).
// Returns an error if [Runtime] does not implement [runtime.WorkerRuntime] (in-process polling not supported).
func (aw *AgentWorker) Start(ctx context.Context) error {
	aw.logger.Info(ctx, "agent worker starting", slog.String("scope", "agent"), slog.String("taskQueue", aw.taskQueue))
	wr, ok := aw.runtime.(runtime.WorkerRuntime)
	if !ok {
		return fmt.Errorf("runtime does not implement WorkerRuntime (in-process Start/Stop); use a backend that supports local workers")
	}
	return wr.Start(ctx)
}

// Stop stops the worker if [Runtime] implements [runtime.WorkerRuntime].
func (aw *AgentWorker) Stop() {
	aw.logger.Info(context.Background(), "agent worker stopping", slog.String("scope", "agent"), slog.String("taskQueue", aw.taskQueue))
	if wr, ok := aw.runtime.(runtime.WorkerRuntime); ok {
		wr.Stop()
	}
	// Standalone remote worker process: flush OTLP (embedded local worker uses [Agent.Close] only).
	if aw.remoteWorker {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_ = aw.tracer.Shutdown(shutdownCtx)
		_ = aw.metrics.Shutdown(shutdownCtx)
		_ = aw.logs.Shutdown(shutdownCtx)
		cancel()
	}
}
