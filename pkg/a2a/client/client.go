package client

import (
	"context"
	"errors"
	"fmt"
	"iter"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2aclient"
	"github.com/a2aproject/a2a-go/v2/a2aclient/agentcard"
	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
)

// Client implements [interfaces.A2AClient], [interfaces.A2AStreamingClient], and
// [interfaces.A2ATaskClient] backed by [a2aclient.Client] from the a2aproject/a2a-go/v2 SDK.
//
// The inner [a2aclient.Client] is created lazily on the first call that needs it (SendMessage,
// GetTask, CancelTask, SendStreamingMessage). [ResolveCard] and [ListSkills] use only the
// lightweight card resolver HTTP call. [Ping] also uses only the resolver.
//
// All exported methods are safe for concurrent use. A single inner client is shared across calls.
type Client struct {
	mu          sync.Mutex
	name        string
	url         string
	resolver    *agentcard.Resolver
	log         logger.Logger
	timeout     time.Duration
	skillFilter types.A2ASkillFilter

	// lazily initialised; protected by mu
	closed bool
	card   *a2a.AgentCard
	inner  *a2aclient.Client
}

// Compile-time interface checks.
var (
	_ interfaces.A2AClient          = (*Client)(nil)
	_ interfaces.A2AStreamingClient = (*Client)(nil)
	_ interfaces.A2ATaskClient      = (*Client)(nil)
)

// NewClient creates an A2A client connected to the agent server at url.
//
// url is the base URL used for card resolution (e.g. "https://agent.example.com").
// name identifies this connection for logging (equivalent role to the key in agent.A2AServers).
// opts are optional [Option]s; see [BuildConfig] for defaults.
//
// NewClient does not open a network connection. The inner [a2aclient.Client] is created
// lazily on the first SendMessage / GetTask / CancelTask / SendStreamingMessage call.
func NewClient(name string, url string, opts ...Option) (*Client, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("a2a client: name is empty")
	}
	if strings.TrimSpace(url) == "" {
		return nil, errors.New("a2a client: url is empty")
	}
	cfg, err := BuildConfig(opts...)
	if err != nil {
		return nil, err
	}
	resolver := &agentcard.Resolver{Client: buildHTTPClient(cfg)}
	cfg.Logger.Debug(context.Background(), "a2a client: created",
		slog.String("client", name),
		slog.String("url", url),
		slog.Duration("timeout", cfg.Timeout),
	)
	return &Client{
		name:        name,
		url:         url,
		resolver:    resolver,
		log:         cfg.Logger,
		timeout:     cfg.Timeout,
		skillFilter: cfg.SkillFilter,
	}, nil
}

// Name implements [interfaces.A2AClient].
func (c *Client) Name() string {
	if c == nil {
		return ""
	}
	return c.name
}

// Ping implements [interfaces.A2AClient] by resolving the agent card at the configured URL.
// A successful card resolution proves the server is reachable and well-formed.
func (c *Client) Ping(ctx context.Context) error {
	if c == nil {
		return errors.New("a2a client: nil")
	}
	if err := c.checkClosed(); err != nil {
		return err
	}
	start := time.Now()
	c.log.Debug(ctx, "a2a client: ping start", slog.String("client", c.name))
	rCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	if _, err := c.resolver.Resolve(rCtx, c.url); err != nil {
		c.log.Warn(ctx, "a2a client: ping failed",
			slog.String("client", c.name),
			slog.Duration("elapsed", time.Since(start)),
			slog.Any("err", err),
		)
		return fmt.Errorf("a2a client ping: %w", err)
	}
	c.log.Debug(ctx, "a2a client: ping ok",
		slog.String("client", c.name),
		slog.Duration("elapsed", time.Since(start)),
	)
	return nil
}

// ResolveCard implements [interfaces.A2AClient]: fetches the AgentCard from the server's
// well-known endpoint. The resolved card is cached so that subsequent [ListSkills] calls
// (and the lazy inner-client creation) do not incur an additional round-trip.
func (c *Client) ResolveCard(ctx context.Context) (interfaces.A2AAgentCard, error) {
	if c == nil {
		return interfaces.A2AAgentCard{}, errors.New("a2a client: nil")
	}
	if err := c.checkClosed(); err != nil {
		return interfaces.A2AAgentCard{}, err
	}
	start := time.Now()
	c.log.Debug(ctx, "a2a client: resolve_card start", slog.String("client", c.name))
	rCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	sdkCard, err := c.resolver.Resolve(rCtx, c.url)
	if err != nil {
		c.log.Warn(ctx, "a2a client: resolve_card failed",
			slog.String("client", c.name),
			slog.Duration("elapsed", time.Since(start)),
			slog.Any("err", err),
		)
		return interfaces.A2AAgentCard{}, fmt.Errorf("a2a client resolve card: %w", err)
	}
	if normalizeResolvedCard(sdkCard) {
		c.log.Warn(ctx, "a2a client: agent card missing protocolVersion; defaulted to a2a spec version",
			slog.String("client", c.name),
			slog.String("agent", sdkCard.Name),
			slog.String("version", string(a2a.Version)),
		)
	}
	c.mu.Lock()
	c.card = sdkCard
	c.mu.Unlock()
	card := fromSDKAgentCard(sdkCard, c.url)
	c.log.Debug(ctx, "a2a client: resolve_card ok",
		slog.String("client", c.name),
		slog.String("agent", card.Name),
		slog.Int("skills", len(card.Skills)),
		slog.Duration("elapsed", time.Since(start)),
	)
	return card, nil
}

// ListSkills implements [interfaces.A2AClient]: resolves the card and returns the skill specs.
// Skills are the A2A equivalent of tools and are used to expose the remote agent's capabilities
// to the LLM as Tool definitions. When [WithSkillFilter] is configured, only skills matching
// the allow/block lists are returned ([types.A2ASkillFilter.Apply] runs here).
func (c *Client) ListSkills(ctx context.Context) ([]interfaces.A2ASkillSpec, error) {
	if c == nil {
		return nil, errors.New("a2a client: nil")
	}
	card, err := c.ResolveCard(ctx)
	if err != nil {
		return nil, err
	}
	return c.skillFilter.Apply(card.Skills), nil
}

// SendMessage implements [interfaces.A2AClient]: sends req to the agent and returns the result.
// The result contains either a completed [interfaces.A2AMessage] (synchronous response) or an
// [interfaces.A2ATask] (asynchronous; poll with [GetTask] or subscribe via [SendStreamingMessage]).
func (c *Client) SendMessage(ctx context.Context, req interfaces.A2ASendMessageRequest) (interfaces.A2ASendMessageResult, error) {
	if c == nil {
		return interfaces.A2ASendMessageResult{}, errors.New("a2a client: nil")
	}
	start := time.Now()
	c.log.Debug(ctx, "a2a client: send_message start", slog.String("client", c.name))
	inner, err := c.getInner(ctx)
	if err != nil {
		return interfaces.A2ASendMessageResult{}, err
	}
	opCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	sdkResult, err := inner.SendMessage(opCtx, toSDKSendMessageRequest(req))
	if err != nil {
		c.log.Warn(ctx, "a2a client: send_message failed",
			slog.String("client", c.name),
			slog.Duration("elapsed", time.Since(start)),
			slog.Any("err", err),
		)
		return interfaces.A2ASendMessageResult{}, fmt.Errorf("a2a client send message: %w", err)
	}
	result := fromSDKSendMessageResult(sdkResult)
	c.log.Debug(ctx, "a2a client: send_message ok",
		slog.String("client", c.name),
		slog.Bool("hasMessage", result.Message != nil),
		slog.Bool("hasTask", result.Task != nil),
		slog.Duration("elapsed", time.Since(start)),
	)
	return result, nil
}

// SendStreamingMessage implements [interfaces.A2AStreamingClient]: sends req and returns an
// iterator over events streamed back by the agent. Each event is one of: message delta,
// task status update, or artifact update.
//
// The caller must either consume all events or break the iteration to release resources.
// The iterator is driven by ctx; cancel ctx to abort the stream.
//
// When the server does not support streaming, the SDK falls back to a single non-streaming
// SendMessage call and wraps the result as a single event.
func (c *Client) SendStreamingMessage(ctx context.Context, req interfaces.A2ASendMessageRequest) (iter.Seq2[interfaces.A2AStreamEvent, error], error) {
	if c == nil {
		return nil, errors.New("a2a client: nil")
	}
	inner, err := c.getInner(ctx)
	if err != nil {
		return nil, err
	}
	sdkReq := toSDKSendMessageRequest(req)
	// Use a child context so the caller can cancel the stream via ctx without a hard timeout.
	streamCtx, cancel := context.WithCancel(ctx)
	sdkSeq := inner.SendStreamingMessage(streamCtx, sdkReq)
	seq := func(yield func(interfaces.A2AStreamEvent, error) bool) {
		defer cancel()
		for event, err := range sdkSeq {
			if err != nil {
				yield(interfaces.A2AStreamEvent{}, err)
				return
			}
			if !yield(fromSDKEvent(event), nil) {
				return
			}
		}
	}
	return seq, nil
}

// GetTask implements [interfaces.A2ATaskClient]: retrieves the current state of an async task.
func (c *Client) GetTask(ctx context.Context, taskID string) (interfaces.A2ATask, error) {
	if c == nil {
		return interfaces.A2ATask{}, errors.New("a2a client: nil")
	}
	if strings.TrimSpace(taskID) == "" {
		return interfaces.A2ATask{}, errors.New("a2a client get task: taskID is empty")
	}
	start := time.Now()
	c.log.Debug(ctx, "a2a client: get_task start",
		slog.String("client", c.name),
		slog.String("taskID", taskID),
	)
	inner, err := c.getInner(ctx)
	if err != nil {
		return interfaces.A2ATask{}, err
	}
	opCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	task, err := inner.GetTask(opCtx, &a2a.GetTaskRequest{ID: a2a.TaskID(taskID)})
	if err != nil {
		c.log.Warn(ctx, "a2a client: get_task failed",
			slog.String("client", c.name),
			slog.String("taskID", taskID),
			slog.Duration("elapsed", time.Since(start)),
			slog.Any("err", err),
		)
		return interfaces.A2ATask{}, fmt.Errorf("a2a client get task: %w", err)
	}
	result := fromSDKTask(task)
	c.log.Debug(ctx, "a2a client: get_task ok",
		slog.String("client", c.name),
		slog.String("taskID", taskID),
		slog.String("status", string(result.Status)),
		slog.Duration("elapsed", time.Since(start)),
	)
	return result, nil
}

// CancelTask implements [interfaces.A2ATaskClient]: requests cancellation of an in-progress task.
// The returned [interfaces.A2ATask] reflects the server-acknowledged state after cancellation.
func (c *Client) CancelTask(ctx context.Context, taskID string) (interfaces.A2ATask, error) {
	if c == nil {
		return interfaces.A2ATask{}, errors.New("a2a client: nil")
	}
	if strings.TrimSpace(taskID) == "" {
		return interfaces.A2ATask{}, errors.New("a2a client cancel task: taskID is empty")
	}
	start := time.Now()
	c.log.Debug(ctx, "a2a client: cancel_task start",
		slog.String("client", c.name),
		slog.String("taskID", taskID),
	)
	inner, err := c.getInner(ctx)
	if err != nil {
		return interfaces.A2ATask{}, err
	}
	opCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()
	task, err := inner.CancelTask(opCtx, &a2a.CancelTaskRequest{ID: a2a.TaskID(taskID)})
	if err != nil {
		c.log.Warn(ctx, "a2a client: cancel_task failed",
			slog.String("client", c.name),
			slog.String("taskID", taskID),
			slog.Duration("elapsed", time.Since(start)),
			slog.Any("err", err),
		)
		return interfaces.A2ATask{}, fmt.Errorf("a2a client cancel task: %w", err)
	}
	result := fromSDKTask(task)
	c.log.Debug(ctx, "a2a client: cancel_task ok",
		slog.String("client", c.name),
		slog.String("taskID", taskID),
		slog.String("status", string(result.Status)),
		slog.Duration("elapsed", time.Since(start)),
	)
	return result, nil
}

// Close implements [interfaces.A2AClient]: destroys the inner client and marks this client as closed.
// Subsequent calls on a closed client return an error. Close is idempotent.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return nil
	}
	c.closed = true
	inner := c.inner
	c.inner = nil
	c.card = nil
	if inner != nil {
		if err := inner.Destroy(); err != nil {
			c.log.Warn(context.Background(), "a2a client: destroy error",
				slog.String("client", c.name),
				slog.Any("err", err),
			)
			return fmt.Errorf("a2a client close: %w", err)
		}
	}
	c.log.Info(context.Background(), "a2a client: closed", slog.String("client", c.name))
	return nil
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// normalizeResolvedCard fills empty ProtocolVersion on each supported interface.
// The JSON agent card from some servers omits protocolVersion (or uses names the std
// decoder does not map). a2aclient.Factory then reports
// "no compatible transports found: available transports - [JSONRPC_]" because matching
// keys include version. Default to [a2a.Version] like [a2a.NewAgentInterface].
func normalizeResolvedCard(card *a2a.AgentCard) bool {
	if card == nil {
		return false
	}
	patched := false
	for _, iface := range card.SupportedInterfaces {
		if iface == nil {
			continue
		}
		if iface.ProtocolVersion == "" {
			iface.ProtocolVersion = a2a.Version
			patched = true
		}
	}
	return patched
}

// checkClosed returns an error if the client has been closed.
func (c *Client) checkClosed() error {
	c.mu.Lock()
	closed := c.closed
	c.mu.Unlock()
	if closed {
		return fmt.Errorf("a2a client %q: closed", c.name)
	}
	return nil
}

// getInner lazily creates and caches the inner [a2aclient.Client].
//
// The inner client is created by resolving the agent card (one HTTP GET) and then calling
// [a2aclient.NewFromCard], which selects the best supported transport (JSON-RPC first, then REST
// per the SDK's default factory ordering) and opens the connection.
//
// If two goroutines race to create the client, one will win; the other discards its client via
// [a2aclient.Client.Destroy] and returns the winner's instance.
func (c *Client) getInner(ctx context.Context) (*a2aclient.Client, error) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return nil, fmt.Errorf("a2a client %q: closed", c.name)
	}
	if c.inner != nil {
		inner := c.inner
		c.mu.Unlock()
		return inner, nil
	}
	c.mu.Unlock()

	// Resolve card and open connection outside the lock to avoid blocking concurrent readers
	// for the full duration of two network round-trips.
	opCtx, cancel := context.WithTimeout(ctx, c.timeout)
	defer cancel()

	sdkCard, err := c.resolver.Resolve(opCtx, c.url)
	if err != nil {
		return nil, fmt.Errorf("a2a client %q: resolve card for connect: %w", c.name, err)
	}
	if normalizeResolvedCard(sdkCard) {
		c.log.Warn(ctx, "a2a client: agent card missing protocolVersion; defaulted to a2a spec version",
			slog.String("client", c.name),
			slog.String("agent", sdkCard.Name),
			slog.String("version", string(a2a.Version)),
		)
	}

	inner, err := a2aclient.NewFromCard(opCtx, sdkCard)
	if err != nil {
		return nil, fmt.Errorf("a2a client %q: connect: %w", c.name, err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		_ = inner.Destroy()
		return nil, fmt.Errorf("a2a client %q: closed", c.name)
	}
	if c.inner != nil {
		// Another goroutine won the race; discard our extra client.
		_ = inner.Destroy()
		return c.inner, nil
	}
	c.card = sdkCard
	c.inner = inner
	c.log.Info(ctx, "a2a client: connected",
		slog.String("client", c.name),
		slog.String("agent", sdkCard.Name),
	)
	return inner, nil
}
