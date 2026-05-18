package types

import "time"

// OTLPProtocol selects the wire format used by OTLP trace and metrics exporters.
// The string value is stable and used in fingerprints — do not change existing values.
type OTLPProtocol string

const (
	// OTLPProtocolGRPC exports telemetry over gRPC. This is the default and is supported
	// by virtually all OpenTelemetry collectors.
	OTLPProtocolGRPC OTLPProtocol = "grpc"

	// OTLPProtocolHTTP exports telemetry over HTTP/protobuf. Use when gRPC is blocked
	// by a firewall or proxy that only passes HTTP/1.1 traffic.
	OTLPProtocolHTTP OTLPProtocol = "http"
)

// Default OTLP timing used when the caller does not override them.
// Shared between pkg/observability (where they are the BuildConfig fallbacks)
// and pkg/agent (where they are applied silently when ObservabilityConfig is used).
const (
	// DefaultOTLPExportTimeout is the per-export call deadline.
	DefaultOTLPExportTimeout = 30 * time.Second

	// DefaultOTLPBatchTimeout is the maximum delay before a trace span batch is flushed.
	DefaultOTLPBatchTimeout = 5 * time.Second

	// DefaultOTLPMetricsInterval is how often the metrics periodic reader pushes to the collector.
	DefaultOTLPMetricsInterval = 60 * time.Second
)
