package observability

import (
	"context"
	"fmt"

	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploggrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlplog/otlploghttp"
	sdklog "go.opentelemetry.io/otel/sdk/log"
	"google.golang.org/grpc/credentials/insecure"
)

// Logs wraps [sdklog.LoggerProvider] and implements [interfaces.Logs].
// Construct it with [NewLogs]; the zero value is not usable.
// Call [Logs.Shutdown] when the agent or worker stops to flush buffered log records.
type Logs struct {
	lp *sdklog.LoggerProvider
}

// Provider returns the underlying [sdklog.LoggerProvider]. Pass it to
// [pkg/logger.DefaultLoggerWithOtelProvider] so the slog bridge sends records
// through this provider rather than the (likely-unset) global OTel LoggerProvider.
func (l *Logs) Provider() *sdklog.LoggerProvider {
	return l.lp
}

// Shutdown flushes buffered log records and releases the exporter connection.
func (l *Logs) Shutdown(ctx context.Context) error {
	return l.lp.Shutdown(ctx)
}

// NewLogs constructs a [Logs] from the given options:
//  1. Calls [BuildConfig] to validate and apply defaults.
//  2. Builds an OTLP log exporter (gRPC or HTTP per [Config.Protocol]).
//  3. Wraps it in a [sdklog.BatchProcessor] for async, batched export.
//  4. Creates a [sdklog.LoggerProvider] with the same service resource as tracer/metrics.
//  5. Returns a [Logs] whose [Logs.Provider] can be passed to the logger bridge.
func NewLogs(opts ...Option) (*Logs, error) {
	cfg, err := BuildConfig(opts...)
	if err != nil {
		return nil, err
	}

	res, err := buildResource(cfg)
	if err != nil {
		return nil, fmt.Errorf("observability: build resource for logs: %w", err)
	}

	exp, err := buildLogExporter(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("observability: build log exporter: %w", err)
	}

	batchProc := sdklog.NewBatchProcessor(exp)

	lp := sdklog.NewLoggerProvider(
		sdklog.WithProcessor(batchProc),
		sdklog.WithResource(res),
	)

	return &Logs{lp: lp}, nil
}

// buildLogExporter creates the OTLP log exporter for the chosen protocol,
// mirroring [buildTraceExporter] and [buildMetricExporter].
func buildLogExporter(ctx context.Context, cfg *Config) (sdklog.Exporter, error) {
	switch cfg.Protocol {
	case ProtocolHTTP:
		hopts := []otlploghttp.Option{
			otlploghttp.WithEndpoint(cfg.Endpoint),
		}
		if len(cfg.Headers) > 0 {
			hopts = append(hopts, otlploghttp.WithHeaders(cfg.Headers))
		}
		if cfg.Insecure {
			hopts = append(hopts, otlploghttp.WithInsecure())
		}
		return otlploghttp.New(ctx, hopts...)

	default: // ProtocolGRPC
		gopts := []otlploggrpc.Option{
			otlploggrpc.WithEndpoint(cfg.Endpoint),
		}
		if len(cfg.Headers) > 0 {
			gopts = append(gopts, otlploggrpc.WithHeaders(cfg.Headers))
		}
		if cfg.Insecure {
			gopts = append(gopts, otlploggrpc.WithTLSCredentials(insecure.NewCredentials()))
		}
		return otlploggrpc.New(ctx, gopts...)
	}
}
