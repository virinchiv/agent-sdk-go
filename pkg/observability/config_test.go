package observability

import (
	"strings"
	"testing"
	"time"

	"github.com/agenticenv/agent-sdk-go/internal/types"
)

func TestBuildConfig_MissingEndpoint(t *testing.T) {
	_, err := BuildConfig(WithName("svc"))
	if err == nil || !strings.Contains(err.Error(), "endpoint") {
		t.Fatalf("want endpoint error, got %v", err)
	}
}

func TestBuildConfig_MissingName(t *testing.T) {
	_, err := BuildConfig(WithEndpoint("localhost:4317"))
	if err == nil || !strings.Contains(err.Error(), "name") {
		t.Fatalf("want name error, got %v", err)
	}
}

func TestBuildConfig_Defaults(t *testing.T) {
	c, err := BuildConfig(WithEndpoint("localhost:4317"), WithName("svc"))
	if err != nil {
		t.Fatal(err)
	}
	if c.Protocol != types.OTLPProtocolGRPC {
		t.Fatalf("Protocol = %q, want grpc", c.Protocol)
	}
	if c.ExportTimeout != types.DefaultOTLPExportTimeout {
		t.Fatalf("ExportTimeout = %v, want %v", c.ExportTimeout, types.DefaultOTLPExportTimeout)
	}
	if c.BatchTimeout != types.DefaultOTLPBatchTimeout {
		t.Fatalf("BatchTimeout = %v, want %v", c.BatchTimeout, types.DefaultOTLPBatchTimeout)
	}
	if c.MetricsInterval != types.DefaultOTLPMetricsInterval {
		t.Fatalf("MetricsInterval = %v, want %v", c.MetricsInterval, types.DefaultOTLPMetricsInterval)
	}
}

func TestBuildConfig_WithProtocolHTTP(t *testing.T) {
	c, err := BuildConfig(
		WithEndpoint("localhost:4318"),
		WithName("svc"),
		WithProtocol(ProtocolHTTP),
	)
	if err != nil {
		t.Fatal(err)
	}
	if c.Protocol != ProtocolHTTP {
		t.Fatalf("Protocol = %q", c.Protocol)
	}
}

func TestBuildConfig_CustomTimeouts(t *testing.T) {
	c, err := BuildConfig(
		WithEndpoint("localhost:4317"),
		WithName("svc"),
		WithExportTimeout(7*time.Second),
		WithBatchTimeout(2*time.Second),
		WithMetricsInterval(11*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if c.ExportTimeout != 7*time.Second || c.BatchTimeout != 2*time.Second || c.MetricsInterval != 11*time.Second {
		t.Fatalf("got %+v", c)
	}
}
