package observability

import (
	"fmt"
	"time"

	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
)

// Protocol is an alias for [types.OTLPProtocol] re-exported so callers of this package
// do not need to import internal/types directly.
type Protocol = types.OTLPProtocol

const (
	// ProtocolGRPC exports telemetry over gRPC (default; most collectors support it).
	ProtocolGRPC Protocol = types.OTLPProtocolGRPC
	// ProtocolHTTP exports telemetry over HTTP/protobuf (useful when gRPC is blocked).
	ProtocolHTTP Protocol = types.OTLPProtocolHTTP
)

// Config holds all settings for constructing OTLP-backed [Tracer] and [Metrics] clients.
// Build it with [BuildConfig] after applying functional [Option]s.
//
// All fields that affect exporter timing and behaviour are exposed here so that
// callers using this package directly have full control. When observability is
// configured through [pkg/agent.ObservabilityConfig] the agent SDK applies the
// [types.DefaultOTLP*] constants automatically.
type Config struct {
	// Logger receives exporter and diagnostics messages when wiring fails or during shutdown.
	Logger logger.Logger
	// LogLevel is used when Logger is unset (same strings as [logger.DefaultLogger]: debug, info, warn, error).
	LogLevel string

	// Endpoint is the OTLP collector URL, e.g. "collector:4317" (gRPC) or
	// "http://collector:4318" (HTTP). Required.
	Endpoint string

	// Name is the service / scope name attached to all telemetry. Required.
	Name string

	// Protocol selects the OTLP wire transport. Defaults to [ProtocolGRPC].
	Protocol Protocol

	// Headers are extra gRPC metadata or HTTP headers sent with every export request
	// (e.g. {"Authorization": "Bearer <token>"} for SaaS backends). Optional.
	Headers map[string]string

	// Insecure disables TLS. Set true for local / dev collectors that have no cert.
	Insecure bool

	// ServiceVersion is added to the OTLP resource as "service.version". Optional.
	ServiceVersion string

	// DeploymentEnvironment is added to the OTLP resource as "deployment.environment"
	// (e.g. "production", "staging"). Optional.
	DeploymentEnvironment string

	// ExportTimeout is the per-export call deadline.
	// Defaults to [types.DefaultOTLPExportTimeout] (30 s).
	ExportTimeout time.Duration

	// BatchTimeout is the maximum delay before a trace batch is flushed.
	// Lower values reduce trace latency at the cost of throughput.
	// Defaults to [types.DefaultOTLPBatchTimeout] (5 s).
	BatchTimeout time.Duration

	// MetricsInterval is how often the metrics periodic reader pushes to the collector.
	// Defaults to [types.DefaultOTLPMetricsInterval] (60 s).
	MetricsInterval time.Duration

	// SamplingRatio controls trace sampling between 0.0 (drop all) and 1.0 (keep all).
	// Values ≤ 0 or > 1 fall back to AlwaysSample.
	SamplingRatio float64
}

// Option mutates [Config] when passed to [BuildConfig], [NewTracer], or [NewMetrics].
type Option func(*Config)

// WithLogger sets structured logging for OTLP setup and lifecycle.
func WithLogger(l logger.Logger) Option {
	return func(c *Config) { c.Logger = l }
}

// WithLogLevel sets the level used when [WithLogger] is not set (same strings as
// [logger.DefaultLogger]: debug, info, warn, error). Empty defaults to "error" in [BuildConfig].
func WithLogLevel(level string) Option {
	return func(c *Config) { c.LogLevel = level }
}

// WithEndpoint sets the OTLP collector URL. Required.
//
//   - gRPC (default): "collector:4317" or "localhost:4317"
//   - HTTP:           "http://collector:4318" or "https://collector:4318"
func WithEndpoint(endpoint string) Option {
	return func(c *Config) { c.Endpoint = endpoint }
}

// WithName sets the telemetry scope name (typically the agent name). Required.
func WithName(name string) Option {
	return func(c *Config) { c.Name = name }
}

// WithProtocol selects the OTLP wire transport. Defaults to [ProtocolGRPC].
func WithProtocol(p Protocol) Option {
	return func(c *Config) { c.Protocol = p }
}

// WithHeaders sets extra per-request headers (gRPC metadata or HTTP headers).
// Common use: auth tokens for hosted OTLP backends.
func WithHeaders(headers map[string]string) Option {
	return func(c *Config) { c.Headers = headers }
}

// WithInsecure disables TLS for the OTLP connection. Use only in development.
func WithInsecure(insecure bool) Option {
	return func(c *Config) { c.Insecure = insecure }
}

// WithServiceVersion attaches the service version to the OTLP resource ("service.version").
func WithServiceVersion(v string) Option {
	return func(c *Config) { c.ServiceVersion = v }
}

// WithDeploymentEnvironment attaches the deployment environment to the OTLP resource
// ("deployment.environment"), e.g. "production" or "staging".
func WithDeploymentEnvironment(env string) Option {
	return func(c *Config) { c.DeploymentEnvironment = env }
}

// WithExportTimeout sets the per-export call deadline.
// Defaults to [types.DefaultOTLPExportTimeout] (30 s).
func WithExportTimeout(d time.Duration) Option {
	return func(c *Config) { c.ExportTimeout = d }
}

// WithBatchTimeout sets the maximum delay before a trace span batch is flushed.
// Defaults to [types.DefaultOTLPBatchTimeout] (5 s).
func WithBatchTimeout(d time.Duration) Option {
	return func(c *Config) { c.BatchTimeout = d }
}

// WithMetricsInterval sets how often the periodic metrics reader pushes to the collector.
// Defaults to [types.DefaultOTLPMetricsInterval] (60 s).
func WithMetricsInterval(d time.Duration) Option {
	return func(c *Config) { c.MetricsInterval = d }
}

// WithSamplingRatio sets the trace sampling probability in [0.0, 1.0].
// Values outside the range fall back to AlwaysSample (keep everything).
func WithSamplingRatio(r float64) Option {
	return func(c *Config) { c.SamplingRatio = r }
}

// BuildConfig merges options into [Config] and applies defaults:
//   - LogLevel:       "error"
//   - Logger:         stderr slog logger at LogLevel
//   - Protocol:       [ProtocolGRPC] (= [types.OTLPProtocolGRPC])
//   - ExportTimeout:  [types.DefaultOTLPExportTimeout]  (30 s)
//   - BatchTimeout:   [types.DefaultOTLPBatchTimeout]   (5 s)
//   - MetricsInterval:[types.DefaultOTLPMetricsInterval] (60 s)
//
// Returns an error when Endpoint or Name is empty.
func BuildConfig(opts ...Option) (*Config, error) {
	c := &Config{}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	if c.LogLevel == "" {
		c.LogLevel = "error"
	}
	if c.Logger == nil {
		c.Logger = logger.DefaultLogger(c.LogLevel)
	}
	if c.Endpoint == "" {
		return nil, fmt.Errorf("observability: endpoint is required")
	}
	if c.Name == "" {
		return nil, fmt.Errorf("observability: name is required")
	}
	if c.Protocol == "" {
		c.Protocol = types.OTLPProtocolGRPC
	}
	if c.ExportTimeout <= 0 {
		c.ExportTimeout = types.DefaultOTLPExportTimeout
	}
	if c.BatchTimeout <= 0 {
		c.BatchTimeout = types.DefaultOTLPBatchTimeout
	}
	if c.MetricsInterval <= 0 {
		c.MetricsInterval = types.DefaultOTLPMetricsInterval
	}
	return c, nil
}
