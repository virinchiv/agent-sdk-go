package observability

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestNewLogs_emptyEndpoint(t *testing.T) {
	_, err := NewLogs(
		WithName("svc"),
		WithEndpoint(""),
	)
	if err == nil {
		t.Fatal("expected error for empty endpoint")
	}
}

func TestNewLogs_emptyName(t *testing.T) {
	_, err := NewLogs(
		WithName(""),
		WithEndpoint("localhost:4317"),
		WithInsecure(true),
	)
	if err == nil {
		t.Fatal("expected error for empty name")
	}
}

func TestNewLogs_HTTP_shutdown(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	endpoint := u.Host

	lp, err := NewLogs(
		WithEndpoint(endpoint),
		WithName("test-logs"),
		WithProtocol(ProtocolHTTP),
		WithInsecure(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	if lp == nil || lp.Provider() == nil {
		t.Fatal("expected non-nil Logs and Provider")
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := lp.Shutdown(shutdownCtx); err != nil {
		t.Fatalf("Shutdown: %v", err)
	}
}

func TestNewLogs_GRPC_insecure_shutdown(t *testing.T) {
	lp, err := NewLogs(
		WithEndpoint("127.0.0.1:1"),
		WithName("grpc-logs-smoke"),
		WithProtocol(ProtocolGRPC),
		WithInsecure(true),
	)
	if err != nil {
		t.Fatal(err)
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = lp.Shutdown(shutdownCtx)
}
