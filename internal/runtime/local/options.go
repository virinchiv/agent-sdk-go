package local

import (
	"context"
	"fmt"
	"log/slog"

	sdkruntime "github.com/agenticenv/agent-sdk-go/internal/runtime"
	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
	"github.com/agenticenv/agent-sdk-go/pkg/observability"
)

type Option func(*LocalRuntime)

func WithLogger(l logger.Logger) Option {
	return func(r *LocalRuntime) {
		if l != nil {
			r.logger = l
		}
	}
}

func WithAgentSpec(spec sdkruntime.AgentSpec) Option {
	return func(r *LocalRuntime) {
		r.AgentSpec = spec
	}
}

func WithAgentExecution(execution sdkruntime.AgentExecution) Option {
	return func(r *LocalRuntime) {
		r.AgentExecution = execution
	}
}

func WithTracer(tracer interfaces.Tracer) Option {
	return func(r *LocalRuntime) {
		r.Tracer = tracer
	}
}

func WithMetrics(metrics interfaces.Metrics) Option {
	return func(r *LocalRuntime) {
		r.Metrics = metrics
	}
}

func WithToolExecutionMode(mode types.AgentToolExecutionMode) Option {
	return func(r *LocalRuntime) {
		r.ToolExecutionMode = mode
	}
}

func buildLocalRuntime(opts ...Option) (*LocalRuntime, error) {
	r := &LocalRuntime{logger: logger.NoopLogger()}
	for _, opt := range opts {
		opt(r)
	}

	if r.AgentExecution.LLM.Client == nil {
		return nil, fmt.Errorf("llm client is required")
	}

	if r.Tracer == nil {
		r.Tracer = observability.DefaultNoopTracer
	}
	if r.Metrics == nil {
		r.Metrics = observability.DefaultNoopMetrics
	}

	r.logger.Debug(context.Background(), "runtime config resolved",
		slog.String("scope", "runtime"),
		slog.String("agentName", r.AgentSpec.Name),
		slog.Bool("hasTracer", r.Tracer != nil),
		slog.Bool("hasMetrics", r.Metrics != nil),
	)
	return r, nil
}
