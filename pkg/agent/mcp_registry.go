package agent

import (
	"fmt"
	"strings"
	"sync"

	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
	mcpclient "github.com/agenticenv/agent-sdk-go/pkg/mcp/client"
)

var _ MCPRegistry = (*mcpRegistryImpl)(nil)

type mcpRegistryImpl struct {
	mu      sync.RWMutex
	logger  logger.Logger
	clients map[string]interfaces.MCPClient
	order   []string
}

// NewMCPRegistry returns an empty MCP client registry for use with [WithMCPRegistry].
// logger is used when [Register] builds a client from [MCPConfig].
func NewMCPRegistry(l logger.Logger) MCPRegistry {
	if l == nil {
		l = NoopLogger()
	}
	return &mcpRegistryImpl{
		logger:  l,
		clients: make(map[string]interfaces.MCPClient),
	}
}

func (r *mcpRegistryImpl) Register(name string, config MCPConfig) error {
	cl, err := newMCPClient(name, config, r.logger)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.registerClientLocked(cl)
}

func (r *mcpRegistryImpl) RegisterClient(client interfaces.MCPClient) error {
	if client == nil {
		return ErrRegistryNilEntry
	}
	name := strings.TrimSpace(client.Name())
	if name == "" {
		return ErrRegistryInvalidName
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.registerClientLocked(client)
}

func (r *mcpRegistryImpl) registerClientLocked(client interfaces.MCPClient) error {
	name := strings.TrimSpace(client.Name())
	if name == "" {
		return ErrRegistryInvalidName
	}
	if _, exists := r.clients[name]; exists {
		return ErrRegistryDuplicate
	}
	r.order = append(r.order, name)
	r.clients[name] = client
	return nil
}

func (r *mcpRegistryImpl) Unregister(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrRegistryInvalidName
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.clients[name]; !ok {
		return ErrRegistryNotFound
	}
	delete(r.clients, name)
	r.order = removeFromOrder(r.order, name)
	return nil
}

func (r *mcpRegistryImpl) Get(name string) (interfaces.MCPClient, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrRegistryInvalidName
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.clients[name]
	if !ok {
		return nil, ErrRegistryNotFound
	}
	return c, nil
}

func (r *mcpRegistryImpl) List() []interfaces.MCPClient {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]interfaces.MCPClient, 0, len(r.order))
	for _, name := range r.order {
		if c, ok := r.clients[name]; ok {
			out = append(out, c)
		}
	}
	return out
}

func newMCPClient(name string, cfg MCPConfig, log logger.Logger) (interfaces.MCPClient, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrRegistryInvalidName
	}
	if cfg.Transport == nil {
		return nil, fmt.Errorf("mcp %q: Transport is required", name)
	}
	if log == nil {
		log = NoopLogger()
	}
	mcpOpts := []mcpclient.Option{
		mcpclient.WithLogger(log),
		mcpclient.WithTimeout(cfg.Timeout),
		mcpclient.WithRetryAttempts(cfg.RetryAttempts),
		mcpclient.WithToolFilter(cfg.ToolFilter),
	}
	cl, err := mcpclient.NewClient(name, cfg.Transport, mcpOpts...)
	if err != nil {
		return nil, fmt.Errorf("mcp %q: new client: %w", name, err)
	}
	return cl, nil
}
