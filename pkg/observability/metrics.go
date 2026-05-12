package observability

import (
	"context"
	"fmt"
	"sync"

	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"google.golang.org/grpc/credentials/insecure"
)

// Metrics implements [interfaces.Metrics] backed by an OTLP [sdkmetric.MeterProvider].
//
// Counters and histograms are created lazily on first use and cached for the lifetime of
// the Metrics instance. Construct it with [NewMetrics]; the zero value is not usable.
// Call [Metrics.Shutdown] when the agent or worker stops to flush pending metric data.
type Metrics struct {
	mp     *sdkmetric.MeterProvider
	meter  metric.Meter
	logger logger.Logger

	// counters and histograms are lazy-initialised on first use to avoid the
	// overhead of pre-registering every possible instrument name at startup.
	countersMu sync.RWMutex
	counters   map[string]metric.Int64Counter

	histogramsMu sync.RWMutex
	histograms   map[string]metric.Float64Histogram
}

// NewMetrics constructs a [Metrics] from the given options:
//  1. Calls [BuildConfig] to validate and apply defaults.
//  2. Builds an OTLP metrics exporter (gRPC or HTTP per [Config.Protocol]).
//  3. Wraps it in a [sdkmetric.PeriodicReader] that pushes at [Config.MetricsInterval].
//  4. Creates a [sdkmetric.MeterProvider] with an OTLP resource (service name, version, environment).
//  5. Returns a [Metrics] scoped to [Config.Name].
func NewMetrics(opts ...Option) (interfaces.Metrics, error) {
	cfg, err := BuildConfig(opts...)
	if err != nil {
		return nil, err
	}

	res, err := buildResource(cfg)
	if err != nil {
		return nil, fmt.Errorf("observability: build resource: %w", err)
	}

	exp, err := buildMetricExporter(context.Background(), cfg)
	if err != nil {
		return nil, fmt.Errorf("observability: build metric exporter: %w", err)
	}

	reader := sdkmetric.NewPeriodicReader(
		exp,
		sdkmetric.WithInterval(cfg.MetricsInterval),
		sdkmetric.WithTimeout(cfg.ExportTimeout),
	)

	mp := sdkmetric.NewMeterProvider(
		sdkmetric.WithReader(reader),
		sdkmetric.WithResource(res),
	)

	return &Metrics{
		mp:         mp,
		meter:      mp.Meter(cfg.Name),
		logger:     cfg.Logger,
		counters:   make(map[string]metric.Int64Counter),
		histograms: make(map[string]metric.Float64Histogram),
	}, nil
}

// IncrementCounter adds 1 to the named Int64Counter, creating it lazily on first call.
// Attributes are converted from [interfaces.Attribute] to OTel key-value pairs.
func (m *Metrics) IncrementCounter(ctx context.Context, name string, attrs ...interfaces.Attribute) {
	c, err := m.getOrCreateCounter(name)
	if err != nil {
		m.logger.Warn(ctx, "observability: increment counter: "+err.Error())
		return
	}
	c.Add(ctx, 1, metric.WithAttributes(attrsToOtel(attrs)...))
}

// RecordHistogram records value on the named Float64Histogram, creating it lazily on first call.
// Attributes are converted from [interfaces.Attribute] to OTel key-value pairs.
func (m *Metrics) RecordHistogram(ctx context.Context, name string, value float64, attrs ...interfaces.Attribute) {
	h, err := m.getOrCreateHistogram(name)
	if err != nil {
		m.logger.Warn(ctx, "observability: record histogram: "+err.Error())
		return
	}
	h.Record(ctx, value, metric.WithAttributes(attrsToOtel(attrs)...))
}

// Shutdown flushes all pending metric data and releases the exporter connection.
// It must be called once when the agent or worker exits.
func (m *Metrics) Shutdown(ctx context.Context) error {
	return m.mp.Shutdown(ctx)
}

// getOrCreateCounter returns a cached counter or registers a new one under name.
func (m *Metrics) getOrCreateCounter(name string) (metric.Int64Counter, error) {
	m.countersMu.RLock()
	if c, ok := m.counters[name]; ok {
		m.countersMu.RUnlock()
		return c, nil
	}
	m.countersMu.RUnlock()

	m.countersMu.Lock()
	defer m.countersMu.Unlock()
	// Double-checked locking: another goroutine may have added it while we waited.
	if c, ok := m.counters[name]; ok {
		return c, nil
	}
	c, err := m.meter.Int64Counter(name)
	if err != nil {
		return nil, fmt.Errorf("register counter %q: %w", name, err)
	}
	m.counters[name] = c
	return c, nil
}

// getOrCreateHistogram returns a cached histogram or registers a new one under name.
func (m *Metrics) getOrCreateHistogram(name string) (metric.Float64Histogram, error) {
	m.histogramsMu.RLock()
	if h, ok := m.histograms[name]; ok {
		m.histogramsMu.RUnlock()
		return h, nil
	}
	m.histogramsMu.RUnlock()

	m.histogramsMu.Lock()
	defer m.histogramsMu.Unlock()
	if h, ok := m.histograms[name]; ok {
		return h, nil
	}
	h, err := m.meter.Float64Histogram(name)
	if err != nil {
		return nil, fmt.Errorf("register histogram %q: %w", name, err)
	}
	m.histograms[name] = h
	return h, nil
}

// buildMetricExporter creates the OTLP metrics exporter for the chosen protocol.
func buildMetricExporter(ctx context.Context, cfg *Config) (sdkmetric.Exporter, error) {
	switch cfg.Protocol {
	case ProtocolHTTP:
		hopts := []otlpmetrichttp.Option{
			otlpmetrichttp.WithEndpoint(cfg.Endpoint),
			otlpmetrichttp.WithTimeout(cfg.ExportTimeout),
		}
		if len(cfg.Headers) > 0 {
			hopts = append(hopts, otlpmetrichttp.WithHeaders(cfg.Headers))
		}
		if cfg.Insecure {
			hopts = append(hopts, otlpmetrichttp.WithInsecure())
		}
		return otlpmetrichttp.New(ctx, hopts...)

	default: // ProtocolGRPC
		gopts := []otlpmetricgrpc.Option{
			otlpmetricgrpc.WithEndpoint(cfg.Endpoint),
			otlpmetricgrpc.WithTimeout(cfg.ExportTimeout),
		}
		if len(cfg.Headers) > 0 {
			gopts = append(gopts, otlpmetricgrpc.WithHeaders(cfg.Headers))
		}
		if cfg.Insecure {
			gopts = append(gopts, otlpmetricgrpc.WithTLSCredentials(insecure.NewCredentials()))
		}
		return otlpmetricgrpc.New(ctx, gopts...)
	}
}
