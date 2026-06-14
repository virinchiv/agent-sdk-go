package agent

import (
	"strings"
	"sync"
)

var _ SubAgentRegistry = (*subAgentRegistryImpl)(nil)

type subAgentRegistryImpl struct {
	mu     sync.RWMutex
	agents map[string]*Agent
	order  []string
}

// NewSubAgentRegistry returns an empty sub-agent registry for use with [WithSubAgentRegistry].
func NewSubAgentRegistry() SubAgentRegistry {
	return &subAgentRegistryImpl{
		agents: make(map[string]*Agent),
	}
}

func (r *subAgentRegistryImpl) Register(sub *Agent) error {
	if sub == nil {
		return ErrRegistryNilEntry
	}
	name := strings.TrimSpace(sub.Name)
	if name == "" {
		return ErrRegistryInvalidName
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.agents[name]; exists {
		return ErrRegistryDuplicate
	}
	r.order = append(r.order, name)
	r.agents[name] = sub
	return nil
}

func (r *subAgentRegistryImpl) Unregister(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrRegistryInvalidName
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.agents[name]; !ok {
		return ErrRegistryNotFound
	}
	delete(r.agents, name)
	r.order = removeFromOrder(r.order, name)
	return nil
}

func (r *subAgentRegistryImpl) Get(name string) (*Agent, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrRegistryInvalidName
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	a, ok := r.agents[name]
	if !ok {
		return nil, ErrRegistryNotFound
	}
	return a, nil
}

func (r *subAgentRegistryImpl) List() []*Agent {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Agent, 0, len(r.order))
	for _, name := range r.order {
		if a, ok := r.agents[name]; ok {
			out = append(out, a)
		}
	}
	return out
}
