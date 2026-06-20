package pgvector

import "github.com/agenticenv/agent-sdk-go/pkg/memory"

// Default table and column names for [Memory].
const (
	DefaultTable        = "agent_memories"
	DefaultTextCol      = "text"
	DefaultEmbeddingCol = "embedding"

	ColID        = "id"
	ColKind      = "kind"
	ColMetadata  = "metadata"
	ColScopeTags = "scope_tags"
	ColExpiresAt = "expires_at"
	ColCreatedAt = "created_at"
	ColUpdatedAt = "updated_at"
	ColUserID    = memory.ScopeKeyUserID
	ColTenantID  = memory.ScopeKeyTenantID
	ColAgentID   = memory.ScopeKeyAgentID
)

// DefaultLoadLimit is the maximum memories returned when [interfaces.WithLoadLimit] is zero or negative.
const DefaultLoadLimit = 10

// DefaultMinScore is the default cosine similarity when [interfaces.WithMinScore] is zero.
const DefaultMinScore float32 = 0.35
