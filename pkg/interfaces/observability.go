package interfaces

import "context"

//go:generate mockgen -destination=./mocks/mock_observability.go -package=mocks github.com/agenticenv/agent-sdk-go/pkg/interfaces Tracer,Metrics,Span,Logs

// Tracer is the tracing surface used by the SDK for OpenTelemetry-backed implementations (see
// pkg/observability). Callers construct spans around LLM and tool work without importing OTel types.
type Tracer interface {
	// StartSpan creates a span named name and returns a context that carries it plus the span handle.
	StartSpan(ctx context.Context, name string, attrs ...Attribute) (context.Context, Span)

	// Shutdown flushes exporters and releases resources when the agent or worker stops.
	Shutdown(ctx context.Context) error
}

// Metrics records counters and histograms for agent execution without coupling callers to OTel APIs.
type Metrics interface {
	// IncrementCounter adds one to a named counter with optional attributes.
	IncrementCounter(ctx context.Context, name string, attrs ...Attribute)

	// RecordHistogram records a sample on a histogram-style instrument.
	RecordHistogram(ctx context.Context, name string, value float64, attrs ...Attribute)

	// Shutdown flushes exporters and releases resources when the agent or worker stops.
	Shutdown(ctx context.Context) error
}

// Logs manages the OTLP log exporter and flushes buffered log records on Shutdown.
// Obtain one with [pkg/observability.NewLogs]; [pkg/observability.DefaultNoopLogs]
// is used when observability is unconfigured or [ObservabilityConfig.DisableLogs] is true.
type Logs interface {
	// Shutdown flushes buffered log records and releases the exporter connection.
	Shutdown(ctx context.Context) error
}

// Span is an active trace span created by [Tracer.StartSpan].
type Span interface {
	// End completes the span; safe to call once.
	End()

	// SetAttribute attaches a typed attribute to this span.
	SetAttribute(key string, value any)

	// RecordError records err on the span when non-nil.
	RecordError(err error)
}

// Attribute is a simple key-value pair for traces and metrics (no OpenTelemetry dependency in interfaces).
type Attribute struct {
	Key   string
	Value any
}
