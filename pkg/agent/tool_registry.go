package agent

import (
	"strings"
	"sync"

	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

var _ ToolRegistry = (*toolRegistryImpl)(nil)

type toolRegistryImpl struct {
	mu    sync.RWMutex
	tools map[string]interfaces.Tool
	order []string
}

// NewToolRegistry returns an empty in-process tool registry for use with [WithToolRegistry].
func NewToolRegistry() ToolRegistry {
	return &toolRegistryImpl{
		tools: make(map[string]interfaces.Tool),
	}
}

func (r *toolRegistryImpl) Register(tool interfaces.Tool) error {
	if tool == nil {
		return ErrRegistryNilEntry
	}
	name := strings.TrimSpace(tool.Name())
	if name == "" {
		return ErrRegistryInvalidName
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.tools[name]; exists {
		return ErrRegistryDuplicate
	}
	r.order = append(r.order, name)
	r.tools[name] = tool
	return nil
}

func (r *toolRegistryImpl) Unregister(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return ErrRegistryInvalidName
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[name]; !ok {
		return ErrRegistryNotFound
	}
	delete(r.tools, name)
	r.order = removeFromOrder(r.order, name)
	return nil
}

func (r *toolRegistryImpl) Get(name string) (interfaces.Tool, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, ErrRegistryInvalidName
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	if !ok {
		return nil, ErrRegistryNotFound
	}
	return t, nil
}

func (r *toolRegistryImpl) List() []interfaces.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]interfaces.Tool, 0, len(r.order))
	for _, name := range r.order {
		if t, ok := r.tools[name]; ok {
			result = append(result, t)
		}
	}
	return result
}

// removeFromOrder removes the first occurrence of name from a string slice and returns the result.
func removeFromOrder(order []string, name string) []string {
	for i, n := range order {
		if n == name {
			return append(order[:i], order[i+1:]...)
		}
	}
	return order
}

// RegisterTools registers each tool on reg, returning the first error.
func RegisterTools(reg ToolRegistry, tools ...interfaces.Tool) error {
	for _, t := range tools {
		if err := reg.Register(t); err != nil {
			return err
		}
	}
	return nil
}
