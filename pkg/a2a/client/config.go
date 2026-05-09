// Package client implements [interfaces.A2AClient], [interfaces.A2AStreamingClient], and
// [interfaces.A2ATaskClient] using the github.com/a2aproject/a2a-go/v2 SDK.
// Application code creates a Client via [NewClient] and registers it with the agent via
// agent.WithA2AConfig. All operations share a single [a2aclient.Client] that is
// created lazily on first use from the resolved AgentCard.
package client

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/agenticenv/agent-sdk-go/internal/types"
	a2apkg "github.com/agenticenv/agent-sdk-go/pkg/a2a"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
)

// ClientConfig holds optional settings for [NewClient].
// Logger and LogLevel mirror pkg/llm and pkg/mcp/client patterns.
// Token, Headers, and SkipTLSVerify configure HTTP auth for both card resolution
// and A2A protocol calls.
// SkillFilter aligns with [github.com/agenticenv/agent-sdk-go/pkg/agent.A2AConfig.SkillFilter]
// (same field semantics as [a2apkg.A2ASkillFilter]).
type ClientConfig struct {
	// Logger receives structured diagnostic output. When nil, [BuildConfig] creates a
	// stderr text logger at [LogLevel].
	Logger logger.Logger

	// LogLevel sets the level used when Logger is not provided ("debug", "info", "warn", "error").
	// Empty defaults to "error" in [BuildConfig].
	LogLevel string

	// Timeout is the deadline applied to each operation (card resolve, send message, get/cancel task).
	// Zero defaults to [types.DefaultA2ATimeout] in [BuildConfig].
	Timeout time.Duration

	// Token is a static bearer token. When non-empty, [BuildConfig] injects it as the
	// Authorization: Bearer <token> header (only if Authorization is not already in Headers).
	Token string

	// Headers are additional HTTP headers sent on every request (card resolution and A2A protocol calls).
	// Use this for API keys, correlation IDs, or any custom header your server requires.
	Headers map[string]string

	// SkipTLSVerify disables TLS certificate verification. Use only in development/testing.
	SkipTLSVerify bool

	// SkillFilter restricts which skills from [ListSkills] are returned (exact skill-ID match).
	// [BuildConfig] validates the filter; [Client.ListSkills] runs [a2apkg.A2ASkillFilter.Apply]
	// to restrict returned specs.
	SkillFilter a2apkg.A2ASkillFilter
}

// Option mutates [ClientConfig] when passed to [BuildConfig] or [NewClient].
type Option func(*ClientConfig)

// WithLogger sets the diagnostic logger for this client. When l is [*logger.SlogLogger] the
// underlying *slog.Logger is also forwarded where the SDK accepts one.
func WithLogger(l logger.Logger) Option {
	return func(c *ClientConfig) { c.Logger = l }
}

// WithLogLevel sets the level used when [WithLogger] is not set
// (same level strings as [logger.DefaultLogger]: debug, info, warn, error).
// Empty defaults to "error" in [BuildConfig].
func WithLogLevel(level string) Option {
	return func(c *ClientConfig) { c.LogLevel = level }
}

// WithTimeout sets the per-operation deadline (card resolve, SendMessage, GetTask, CancelTask).
// Zero means use [defaultA2ATimeout] in [BuildConfig].
func WithTimeout(d time.Duration) Option {
	return func(c *ClientConfig) { c.Timeout = d }
}

// WithToken sets a static bearer token. [BuildConfig] injects it as
// "Authorization: Bearer <token>" unless the caller already set that header via [WithHeaders].
func WithToken(tok string) Option {
	return func(c *ClientConfig) { c.Token = tok }
}

// WithHeaders sets additional HTTP headers sent on every request (merged with any token header).
// Call multiple times; each call replaces the header map.
func WithHeaders(h map[string]string) Option {
	return func(c *ClientConfig) { c.Headers = h }
}

// WithSkipTLSVerify disables TLS certificate verification. Only use in development or testing.
func WithSkipTLSVerify(skip bool) Option {
	return func(c *ClientConfig) { c.SkipTLSVerify = skip }
}

// WithSkillFilter sets allow/block lists (same semantics as [github.com/agenticenv/agent-sdk-go/pkg/agent.A2AConfig.SkillFilter]).
// [BuildConfig] validates the filter; [Client.ListSkills] runs [a2apkg.A2ASkillFilter.Apply] to restrict returned specs.
func WithSkillFilter(f a2apkg.A2ASkillFilter) Option {
	return func(c *ClientConfig) { c.SkillFilter = f }
}

// BuildConfig builds a [ClientConfig] from opts. Defaults when not set:
//   - LogLevel: "error"
//   - Logger: stderr text logger at LogLevel
//   - Timeout: [types.DefaultA2ATimeout]
//   - Token injected into Headers["Authorization"] if Token is set and key not already present
//
// It returns an error if [ClientConfig.SkillFilter] fails [a2apkg.A2ASkillFilter.Validate].
func BuildConfig(opts ...Option) (*ClientConfig, error) {
	c := &ClientConfig{}
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
	if c.Timeout <= 0 {
		c.Timeout = types.DefaultA2ATimeout
	}
	if tok := strings.TrimSpace(c.Token); tok != "" {
		if c.Headers == nil {
			c.Headers = make(map[string]string)
		}
		if _, exists := c.Headers["Authorization"]; !exists {
			c.Headers["Authorization"] = "Bearer " + tok
		}
	}
	if err := c.SkillFilter.Validate(); err != nil {
		return nil, fmt.Errorf("a2a client config: %w", err)
	}
	return c, nil
}

// buildHTTPClient constructs an [http.Client] from cfg for card resolution and protocol transport.
// It applies optional TLS skip and injects static headers via a round-tripper wrapper.
func buildHTTPClient(cfg *ClientConfig) *http.Client {
	var base http.RoundTripper
	if cfg.SkipTLSVerify {
		base = &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // opt-in
		}
	} else {
		base = http.DefaultTransport
	}
	if len(cfg.Headers) > 0 {
		return &http.Client{Transport: &headerRoundTripper{base: base, headers: cfg.Headers}}
	}
	return &http.Client{Transport: base}
}

// headerRoundTripper injects a fixed set of headers into every outgoing request.
type headerRoundTripper struct {
	base    http.RoundTripper
	headers map[string]string
}

func (h *headerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	base := h.base
	if base == nil {
		base = http.DefaultTransport
	}
	r := req.Clone(req.Context())
	for k, v := range h.headers {
		if k = strings.TrimSpace(k); k != "" {
			r.Header.Set(k, v)
		}
	}
	return base.RoundTrip(r)
}
