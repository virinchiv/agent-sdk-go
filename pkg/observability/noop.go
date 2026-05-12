package observability

import (
	"context"

	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

// DefaultNoopTracer, DefaultNoopMetrics, and DefaultNoopLogs are shared no-op implementations
// for callers that omit explicit observability wiring (e.g. [internal/runtime/temporal.buildTemporalRuntimeConfig]).
var (
	DefaultNoopTracer  interfaces.Tracer  = &NoopTracer{}
	DefaultNoopMetrics interfaces.Metrics = &NoopMetrics{}
	DefaultNoopLogs    interfaces.Logs    = &NoopLogs{}
)

type NoopTracer struct{}

func (n *NoopTracer) StartSpan(ctx context.Context, name string, attrs ...interfaces.Attribute) (context.Context, interfaces.Span) {
	return ctx, &NoopSpan{}
}

func (n *NoopTracer) Shutdown(ctx context.Context) error { return nil }

type NoopSpan struct{}

func (n *NoopSpan) End()                               {}
func (n *NoopSpan) SetAttribute(key string, value any) {}
func (n *NoopSpan) RecordError(err error)              {}

type NoopMetrics struct{}

func (n *NoopMetrics) IncrementCounter(ctx context.Context, name string, attrs ...interfaces.Attribute) {
}
func (n *NoopMetrics) RecordHistogram(ctx context.Context, name string, value float64, attrs ...interfaces.Attribute) {
}
func (n *NoopMetrics) Shutdown(ctx context.Context) error { return nil }

// NoopLogs is a no-op [interfaces.Logs] used when log export is disabled or
// [WithObservabilityConfig] is not set. Shutdown is a no-op so Agent.Close is always safe to call.
type NoopLogs struct{}

func (n *NoopLogs) Shutdown(ctx context.Context) error { return nil }
