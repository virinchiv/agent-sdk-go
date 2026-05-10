// Package a2a defines skill-filter shapes for A2A agent connections.
//
// Use these types with [github.com/agenticenv/agent-sdk-go/pkg/agent.WithA2AConfig] ([github.com/agenticenv/agent-sdk-go/pkg/agent.A2AConfig].SkillFilter),
// [github.com/agenticenv/agent-sdk-go/pkg/a2a/client.NewClient], and related APIs. Definitions live in internal/types/a2a.go.
package a2a

import "github.com/agenticenv/agent-sdk-go/internal/types"

// Type aliases for A2A skill filtering ([github.com/agenticenv/agent-sdk-go/pkg/agent.A2AConfig], client constructors).

type (
	A2ASkillSpec   = types.A2ASkillSpec
	A2ASkillFilter = types.A2ASkillFilter
)
