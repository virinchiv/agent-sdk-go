package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

func TestConvertAttr_types(t *testing.T) {
	tests := []struct {
		key string
		val any
	}{
		{"s", "hello"},
		{"b", true},
		{"i", int(42)},
		{"i32", int32(3)},
		{"i64", int64(9)},
		{"f32", float32(1.5)},
		{"f64", 2.5},
		{"other", struct{ X int }{7}},
	}
	for _, tt := range tests {
		kv := convertAttr(tt.key, tt.val)
		if string(kv.Key) != tt.key {
			t.Fatalf("key %s: got %q", tt.key, kv.Key)
		}
	}
}

func TestAttrsToOtel_empty(t *testing.T) {
	if attrsToOtel(nil) != nil {
		t.Fatal("nil attrs should return nil slice")
	}
	if len(attrsToOtel([]interfaces.Attribute{})) != 0 {
		t.Fatal("empty attrs")
	}
}

func TestBuildSampler_ratioBounds(t *testing.T) {
	// Smoke: all ratios produce a non-nil sampler (ratio sampling vs parent-based always).
	_ = buildSampler(0.5)
	_ = buildSampler(0)
	_ = buildSampler(-1)
	_ = buildSampler(1.5)
}

func TestTracer_HTTP_StartSpanShutdown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := u.Host

	tr, err := NewTracer(
		WithEndpoint(endpoint),
		WithName("test-tracer"),
		WithProtocol(ProtocolHTTP),
		WithInsecure(true),
		WithBatchTimeout(20*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := tr.Shutdown(shutdownCtx); err != nil {
			t.Errorf("Shutdown: %v", err)
		}
	}()

	ctx := context.Background()
	ctx2, sp := tr.StartSpan(ctx, "op", interfaces.Attribute{Key: "k", Value: "v"})
	if sp == nil {
		t.Fatal("nil span")
	}
	sp.SetAttribute("n", 1)
	sp.RecordError(nil)
	sp.End()

	_ = ctx2 // span context carries trace

	time.Sleep(50 * time.Millisecond)
}

func TestBuildResource_optionalFields(t *testing.T) {
	cfg := &Config{
		Name:                  "svc",
		ServiceVersion:        "1.2.3",
		DeploymentEnvironment: "test",
	}
	res, err := buildResource(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if res == nil {
		t.Fatal("nil resource")
	}
}
