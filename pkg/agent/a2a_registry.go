package agent

import (
	"fmt"
	"strings"
	"sync"

	a2aclient "github.com/agenticenv/agent-sdk-go/pkg/a2a/client"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
)

var _ A2ARegistry = (*a2aRegistryImpl)(nil)

type a2aRegistryImpl struct {
	mu      sync.RWMutex
	logger  logger.Logger
	clients map[string]interfaces.A2AClient
	order   []string
}

// NewA2ARegistry returns an empty A2A client registry for use with [WithA2ARegistry].
// logger is used when [Register] builds a client from [A2AConfig].
func NewA2ARegistry(l logger.Logger) A2ARegistry {
	if l == nil {
		l = NoopLogger()
	}
	return &a2aRegistryImpl{
		logger:  l,
		clients: make(map[string]interfaces.A2AClient),
	}
}

func (r *a2aRegistryImpl) Register(name string, config A2AConfig) error {
	cl, err := newA2AClient(name, config, r.logger)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.registerClientLocked(cl)
}

func (r *a2aRegistryImpl) RegisterClient(client interfaces.A2AClient) error {
	if client == nil {
		return ErrRegistryNilEntry
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.registerClientLocked(client)
}

func (r *a2aRegistryImpl) registerClientLocked(client interfaces.A2AClient) error {
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

func (r *a2aRegistryImpl) Unregister(name string) error {
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

func (r *a2aRegistryImpl) Get(name string) (interfaces.A2AClient, error) {
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

func (r *a2aRegistryImpl) List() []interfaces.A2AClient {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]interfaces.A2AClient, 0, len(r.order))
	for _, name := range r.order {
		if c, ok := r.clients[name]; ok {
			out = append(out, c)
		}
	}
	return out
}

func newA2AClient(name string, cfg A2AConfig, log logger.Logger) (interfaces.A2AClient, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrRegistryInvalidName
	}
	if strings.TrimSpace(cfg.URL) == "" {
		return nil, fmt.Errorf("a2a %q: URL is required", name)
	}
	if log == nil {
		log = NoopLogger()
	}
	a2aOpts := []a2aclient.Option{
		a2aclient.WithLogger(log),
		a2aclient.WithTimeout(cfg.Timeout),
		a2aclient.WithToken(cfg.Token),
		a2aclient.WithHeaders(cfg.Headers),
		a2aclient.WithSkillFilter(cfg.SkillFilter),
	}
	if cfg.SkipTLSVerify {
		a2aOpts = append(a2aOpts, a2aclient.WithSkipTLSVerify(true))
	}
	cl, err := a2aclient.NewClient(name, cfg.URL, a2aOpts...)
	if err != nil {
		return nil, fmt.Errorf("a2a %q: new client: %w", name, err)
	}
	return cl, nil
}
