package agent

import (
	"errors"

	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

//go:generate mockgen -destination=./mocks/mock_registry.go -package=mocks github.com/agenticenv/agent-sdk-go/pkg/agent ToolRegistry,MCPRegistry,A2ARegistry,SubAgentRegistry

var (
	// ErrRegistryNotFound is returned when Get or Unregister cannot find the name.
	ErrRegistryNotFound = errors.New("agent: registry entry not found")
	// ErrRegistryDuplicate is returned when Register would overwrite an existing name.
	ErrRegistryDuplicate = errors.New("agent: registry entry already exists")
	// ErrRegistryInvalidName is returned when name is empty after trim.
	ErrRegistryInvalidName = errors.New("agent: registry name must not be empty")
	// ErrRegistryNilEntry is returned when Register is called with a nil tool, client, or sub-agent.
	ErrRegistryNilEntry = errors.New("agent: registry entry must not be nil")
)

// ToolRegistry stores tools for an agent.
type ToolRegistry interface {
	Register(tool interfaces.Tool) error
	Get(name string) (interfaces.Tool, error)
	List() []interfaces.Tool
	Unregister(name string) error
}

// MCPRegistry stores MCP clients for an agent.
type MCPRegistry interface {
	Register(name string, config MCPConfig) error
	RegisterClient(client interfaces.MCPClient) error
	Get(name string) (interfaces.MCPClient, error)
	List() []interfaces.MCPClient
	Unregister(name string) error
}

// A2ARegistry stores A2A clients for an agent.
type A2ARegistry interface {
	Register(name string, config A2AConfig) error
	RegisterClient(client interfaces.A2AClient) error
	Get(name string) (interfaces.A2AClient, error)
	List() []interfaces.A2AClient
	Unregister(name string) error
}

// SubAgentRegistry stores sub-agents for a parent agent.
type SubAgentRegistry interface {
	Register(sub *Agent) error
	Get(name string) (*Agent, error)
	List() []*Agent
	Unregister(name string) error
}
