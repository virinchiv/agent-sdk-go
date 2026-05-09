package client

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
)

// ---------------------------------------------------------------------------
// BuildConfig
// ---------------------------------------------------------------------------

func TestBuildConfig_Defaults(t *testing.T) {
	c, err := BuildConfig()
	if err != nil {
		t.Fatal(err)
	}
	if c.LogLevel != "error" {
		t.Errorf("LogLevel = %q, want error", c.LogLevel)
	}
	if c.Logger == nil {
		t.Fatal("expected default Logger")
	}
	if c.Timeout != types.DefaultA2ATimeout {
		t.Errorf("Timeout = %v, want %v", c.Timeout, types.DefaultA2ATimeout)
	}
	if c.Token != "" {
		t.Errorf("Token should be empty, got %q", c.Token)
	}
	if len(c.Headers) != 0 {
		t.Errorf("Headers should be empty, got %v", c.Headers)
	}
	if c.SkipTLSVerify {
		t.Error("SkipTLSVerify should default to false")
	}
}

func TestBuildConfig_WithLogger(t *testing.T) {
	l := logger.NoopLogger()
	c, err := BuildConfig(WithLogger(l))
	if err != nil {
		t.Fatal(err)
	}
	if c.Logger != l {
		t.Error("Logger not set from WithLogger")
	}
}

func TestBuildConfig_WithLogLevel(t *testing.T) {
	c, err := BuildConfig(WithLogLevel("debug"))
	if err != nil {
		t.Fatal(err)
	}
	if c.LogLevel != "debug" {
		t.Errorf("LogLevel = %q, want debug", c.LogLevel)
	}
}

func TestBuildConfig_WithTimeout(t *testing.T) {
	c, err := BuildConfig(WithTimeout(5 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if c.Timeout != 5*time.Second {
		t.Errorf("Timeout = %v, want 5s", c.Timeout)
	}
}

func TestBuildConfig_ZeroTimeout_UsesDefault(t *testing.T) {
	c, err := BuildConfig(WithTimeout(0))
	if err != nil {
		t.Fatal(err)
	}
	if c.Timeout != types.DefaultA2ATimeout {
		t.Errorf("Timeout = %v, want %v", c.Timeout, types.DefaultA2ATimeout)
	}
}

func TestBuildConfig_NegativeTimeout_UsesDefault(t *testing.T) {
	c, err := BuildConfig(WithTimeout(-1))
	if err != nil {
		t.Fatal(err)
	}
	if c.Timeout != types.DefaultA2ATimeout {
		t.Errorf("Timeout = %v, want %v", c.Timeout, types.DefaultA2ATimeout)
	}
}

func TestBuildConfig_WithToken_InjectsAuthorizationHeader(t *testing.T) {
	c, err := BuildConfig(WithToken("mytoken"))
	if err != nil {
		t.Fatal(err)
	}
	got := c.Headers["Authorization"]
	if got != "Bearer mytoken" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer mytoken")
	}
}

func TestBuildConfig_WithToken_RespectsExistingAuthorizationHeader(t *testing.T) {
	existing := "Bearer existing"
	c, err := BuildConfig(
		WithHeaders(map[string]string{"Authorization": existing}),
		WithToken("newtoken"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if c.Headers["Authorization"] != existing {
		t.Errorf("Authorization overwritten; got %q, want %q", c.Headers["Authorization"], existing)
	}
}

func TestBuildConfig_WithToken_WhitespaceOnly_NotInjected(t *testing.T) {
	c, err := BuildConfig(WithToken("   "))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Headers["Authorization"]; ok {
		t.Error("Authorization header should not be set for whitespace-only token")
	}
}

func TestBuildConfig_WithHeaders(t *testing.T) {
	h := map[string]string{"X-Api-Key": "secret", "X-Tenant": "acme"}
	c, err := BuildConfig(WithHeaders(h))
	if err != nil {
		t.Fatal(err)
	}
	if c.Headers["X-Api-Key"] != "secret" {
		t.Errorf("X-Api-Key = %q", c.Headers["X-Api-Key"])
	}
	if c.Headers["X-Tenant"] != "acme" {
		t.Errorf("X-Tenant = %q", c.Headers["X-Tenant"])
	}
}

func TestBuildConfig_WithSkipTLSVerify(t *testing.T) {
	c, err := BuildConfig(WithSkipTLSVerify(true))
	if err != nil {
		t.Fatal(err)
	}
	if !c.SkipTLSVerify {
		t.Error("SkipTLSVerify should be true")
	}
}

func TestBuildConfig_NilOptionSkipped(t *testing.T) {
	var nilOpt Option
	c, err := BuildConfig(nilOpt, WithLogLevel("warn"))
	if err != nil {
		t.Fatal(err)
	}
	if c.LogLevel != "warn" {
		t.Errorf("LogLevel = %q, want warn", c.LogLevel)
	}
}

func TestBuildConfig_WithLogger_WinsOverLogLevel(t *testing.T) {
	l := logger.NoopLogger()
	c, err := BuildConfig(WithLogLevel("debug"), WithLogger(l))
	if err != nil {
		t.Fatal(err)
	}
	if c.Logger != l {
		t.Error("explicit Logger should take priority")
	}
}

// ---------------------------------------------------------------------------
// buildHTTPClient
// ---------------------------------------------------------------------------

func TestBuildHTTPClient_DefaultTransport(t *testing.T) {
	cfg, _ := BuildConfig()
	hc := buildHTTPClient(cfg)
	if hc == nil {
		t.Fatal("expected non-nil http.Client")
	}
}

func TestBuildHTTPClient_WithHeaders_InjectsOnRequest(t *testing.T) {
	var gotKey, gotVal string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = "X-Test"
		gotVal = r.Header.Get("X-Test")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg, _ := BuildConfig(WithHeaders(map[string]string{"X-Test": "hello"}))
	hc := buildHTTPClient(cfg)
	resp, err := hc.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if gotKey == "" || gotVal != "hello" {
		t.Errorf("expected X-Test: hello, got %q: %q", gotKey, gotVal)
	}
}

func TestBuildHTTPClient_SkipTLSVerify_UsesCustomTLS(t *testing.T) {
	cfg, _ := BuildConfig(WithSkipTLSVerify(true))
	hc := buildHTTPClient(cfg)
	if hc == nil {
		t.Fatal("nil client")
	}
	// The transport must be customised (not http.DefaultTransport) to skip TLS.
	if hc.Transport == http.DefaultTransport {
		t.Error("expected custom transport with SkipTLSVerify, got http.DefaultTransport")
	}
}

// ---------------------------------------------------------------------------
// headerRoundTripper
// ---------------------------------------------------------------------------

func TestHeaderRoundTripper_SetsHeaders(t *testing.T) {
	var seen http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rt := &headerRoundTripper{
		base:    http.DefaultTransport,
		headers: map[string]string{"X-Custom": "value1", "X-Other": "value2"},
	}
	hc := &http.Client{Transport: rt}
	resp, err := hc.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if seen.Get("X-Custom") != "value1" {
		t.Errorf("X-Custom = %q", seen.Get("X-Custom"))
	}
	if seen.Get("X-Other") != "value2" {
		t.Errorf("X-Other = %q", seen.Get("X-Other"))
	}
}

func TestHeaderRoundTripper_EmptyKeySkipped(t *testing.T) {
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHeaders = r.Header.Clone()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rt := &headerRoundTripper{
		base:    http.DefaultTransport,
		headers: map[string]string{"": "ignored", "X-Keep": "yes"},
	}
	hc := &http.Client{Transport: rt}
	resp, err := hc.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()

	if gotHeaders.Get("X-Keep") != "yes" {
		t.Errorf("X-Keep = %q", gotHeaders.Get("X-Keep"))
	}
}

func TestHeaderRoundTripper_NilBase_UsesDefaultTransport(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rt := &headerRoundTripper{base: nil, headers: map[string]string{"X-Nil": "base"}}
	hc := &http.Client{Transport: rt}
	resp, err := hc.Get(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestHeaderRoundTripper_DoesNotMutateOriginalRequest(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	rt := &headerRoundTripper{
		base:    http.DefaultTransport,
		headers: map[string]string{"X-Injected": "yes"},
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	_, _ = rt.RoundTrip(req)

	if req.Header.Get("X-Injected") != "" {
		t.Error("original request was mutated")
	}
}

// ---------------------------------------------------------------------------
// Option chaining
// ---------------------------------------------------------------------------

func TestOptions_LastWriterWins(t *testing.T) {
	c, err := BuildConfig(
		WithLogLevel("debug"),
		WithLogLevel("info"),
		WithTimeout(1*time.Second),
		WithTimeout(2*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(c.LogLevel, "info") {
		t.Errorf("LogLevel = %q, want info", c.LogLevel)
	}
	if c.Timeout != 2*time.Second {
		t.Errorf("Timeout = %v, want 2s", c.Timeout)
	}
}
