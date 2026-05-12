package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

func TestMetrics_HTTP_incrementAndHistogram(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := u.Host

	m, err := NewMetrics(
		WithEndpoint(endpoint),
		WithName("test-metrics"),
		WithProtocol(ProtocolHTTP),
		WithInsecure(true),
		WithMetricsInterval(40*time.Millisecond),
	)
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	m.IncrementCounter(ctx, "runs_total", interfaces.Attribute{Key: "agent", Value: "a"})
	m.RecordHistogram(ctx, "latency_seconds", 0.42, interfaces.Attribute{Key: "step", Value: "llm"})

	var wg sync.WaitGroup
	wg.Add(10)
	for i := 0; i < 10; i++ {
		go func() {
			defer wg.Done()
			m.IncrementCounter(ctx, "runs_total")
		}()
	}
	wg.Wait()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := m.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}
