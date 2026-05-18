package observability

import (
	"context"
	"fmt"

	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc/credentials/insecure"
)

// Tracer implements [interfaces.Tracer] backed by an OTLP [sdktrace.TracerProvider].
//
// Construct it with [NewTracer]; the zero value is not usable.
// Call [Tracer.Shutdown] when the agent or worker stops to flush buffered spans.
type Tracer struct {
	tp     *sdktrace.TracerProvider
	tracer trace.Tracer
	logger logger.Logger
}

// NewTracer constructs a [Tracer] from the given options:
//  1. Calls [BuildConfig] to validate and apply defaults.
//  2. Builds an OTLP span exporter (gRPC or HTTP per [Config.Protocol]).
//  3. Wraps it in a [sdktrace.BatchSpanProcessor] for async, batched export.
//  4. Creates a [sdktrace.TracerProvider] with an OTLP resource (service name, version, environment).
//  5. Returns a [Tracer] scoped to [Config.Name].
func NewTracer(opts ...Option) (interfaces.Tracer, error) {
	cfg, err := BuildConfig(opts...)
	if err != nil {
		return nil, err
	}

	res, err := buildResource(cfg)
	if err != nil {
		return nil, fmt.Errorf("observability: build resource: %w", err)
	}

	exp, err := buildTraceExporter(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("observability: build trace exporter: %w", err)
	}

	bsp := sdktrace.NewBatchSpanProcessor(
		exp,
		sdktrace.WithExportTimeout(cfg.ExportTimeout),
		sdktrace.WithBatchTimeout(cfg.BatchTimeout),
	)

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithSpanProcessor(bsp),
		sdktrace.WithResource(res),
		sdktrace.WithSampler(buildSampler(cfg.SamplingRatio)),
	)

	return &Tracer{
		tp:     tp,
		tracer: tp.Tracer(cfg.Name),
		logger: cfg.Logger,
	}, nil
}

// StartSpan begins a new span named name under ctx. Attributes are converted from
// [interfaces.Attribute] to OpenTelemetry key-value pairs. The returned context
// carries the active span so nested StartSpan calls form a parent-child tree.
func (t *Tracer) StartSpan(ctx context.Context, name string, attrs ...interfaces.Attribute) (context.Context, interfaces.Span) {
	kvs := attrsToOtel(attrs)
	ctx, s := t.tracer.Start(ctx, name, trace.WithAttributes(kvs...))
	return ctx, &spanAdapter{s: s}
}

// Shutdown flushes buffered spans and releases the exporter connection.
// It must be called once when the agent or worker exits.
func (t *Tracer) Shutdown(ctx context.Context) error {
	return t.tp.Shutdown(ctx)
}

// OTelTracer returns the underlying OpenTelemetry Tracer.
// This is an optional OTel specific extension to the [interfaces.OTelTracer] interface.
func (t *Tracer) OTelTracer() trace.Tracer {
	return t.tracer
}

// spanAdapter adapts an OpenTelemetry [trace.Span] to [interfaces.Span].
type spanAdapter struct {
	s trace.Span
}

// End completes the span. Safe to call once; subsequent calls are no-ops per OTel spec.
func (a *spanAdapter) End() { a.s.End() }

// SetAttribute attaches a typed key-value to the span. The value type is inferred:
// string, bool, int/int64, float64 are mapped directly; everything else is formatted with fmt.Sprint.
func (a *spanAdapter) SetAttribute(key string, value any) {
	a.s.SetAttributes(convertAttr(key, value))
}

// RecordError marks the span with an error event. No-op when err is nil.
func (a *spanAdapter) RecordError(err error) {
	if err != nil {
		a.s.RecordError(err)
	}
}

// buildTraceExporter creates the OTLP span exporter for the chosen protocol.
func buildTraceExporter(ctx context.Context, cfg *Config) (sdktrace.SpanExporter, error) {
	switch cfg.Protocol {
	case ProtocolHTTP:
		hopts := []otlptracehttp.Option{
			otlptracehttp.WithEndpoint(cfg.Endpoint),
			otlptracehttp.WithTimeout(cfg.ExportTimeout),
		}
		if len(cfg.Headers) > 0 {
			hopts = append(hopts, otlptracehttp.WithHeaders(cfg.Headers))
		}
		if cfg.Insecure {
			hopts = append(hopts, otlptracehttp.WithInsecure())
		}
		return otlptracehttp.New(ctx, hopts...)

	default: // ProtocolGRPC
		gopts := []otlptracegrpc.Option{
			otlptracegrpc.WithEndpoint(cfg.Endpoint),
			otlptracegrpc.WithTimeout(cfg.ExportTimeout),
		}
		if len(cfg.Headers) > 0 {
			gopts = append(gopts, otlptracegrpc.WithHeaders(cfg.Headers))
		}
		if cfg.Insecure {
			gopts = append(gopts, otlptracegrpc.WithTLSCredentials(insecure.NewCredentials()))
		}
		return otlptracegrpc.New(ctx, gopts...)
	}
}

// buildSampler returns an OTel Sampler for the configured ratio:
//   - ratio in (0, 1): TraceIDRatioBased(ratio) — parent-based so child spans inherit decision.
//   - ratio ≤ 0 or > 1: AlwaysSample (keep everything).
func buildSampler(ratio float64) sdktrace.Sampler {
	if ratio > 0 && ratio <= 1.0 {
		return sdktrace.ParentBased(sdktrace.TraceIDRatioBased(ratio))
	}
	return sdktrace.AlwaysSample()
}

// attrsToOtel converts a slice of [interfaces.Attribute] to OTel key-value pairs.
func attrsToOtel(attrs []interfaces.Attribute) []attribute.KeyValue {
	if len(attrs) == 0 {
		return nil
	}
	kvs := make([]attribute.KeyValue, 0, len(attrs))
	for _, a := range attrs {
		kvs = append(kvs, convertAttr(a.Key, a.Value))
	}
	return kvs
}

// convertAttr converts a single key + value to an OTel [attribute.KeyValue].
// Supported Go types: string, bool, int, int32, int64, float32, float64.
// All other types are stringified with fmt.Sprint.
func convertAttr(key string, value any) attribute.KeyValue {
	switch v := value.(type) {
	case string:
		return attribute.String(key, v)
	case bool:
		return attribute.Bool(key, v)
	case int:
		return attribute.Int(key, v)
	case int32:
		return attribute.Int64(key, int64(v))
	case int64:
		return attribute.Int64(key, v)
	case float32:
		return attribute.Float64(key, float64(v))
	case float64:
		return attribute.Float64(key, v)
	default:
		return attribute.String(key, fmt.Sprint(v))
	}
}

// buildResource creates an OTel [resource.Resource] with service.name plus optional
// service.version and deployment.environment from [Config].
func buildResource(cfg *Config) (*resource.Resource, error) {
	attrs := []attribute.KeyValue{
		// "service.name" is the canonical OTel semantic convention key.
		attribute.String("service.name", cfg.Name),
	}
	if cfg.ServiceVersion != "" {
		attrs = append(attrs, attribute.String("service.version", cfg.ServiceVersion))
	}
	if cfg.DeploymentEnvironment != "" {
		attrs = append(attrs, attribute.String("deployment.environment", cfg.DeploymentEnvironment))
	}
	return resource.New(
		context.Background(),
		resource.WithAttributes(attrs...),
		// Includes process, OS, host, and SDK attributes automatically.
		resource.WithProcess(),
		resource.WithHost(),
		resource.WithTelemetrySDK(),
	)
}
