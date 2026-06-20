package memory

import (
	"time"

	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

const defaultRecallLimit = 10

// defaultRecallMinScore is the default semantic similarity floor for recall.
// Run-summary memories often score below 0.75 against follow-up questions; 0.35 matches retriever example tuning.
const defaultRecallMinScore float32 = 0.35

// defaultDedupMinScore is the default semantic similarity floor for upserting an existing memory on store.
const defaultDedupMinScore float32 = 0.85

// Optional default kinds for general-purpose agents. Custom agents may define and use their own kind strings.
const (
	KindPreference  interfaces.MemoryKind = "preference"
	KindFact        interfaces.MemoryKind = "fact"
	KindDecision    interfaces.MemoryKind = "decision"
	KindInstruction interfaces.MemoryKind = "instruction"
	KindNote        interfaces.MemoryKind = "note"
)

// Default scope metadata keys for [DefaultScopeConfig] and [ScopeMetadata].
const (
	ScopeKeyTenantID = "tenant_id"
	ScopeKeyUserID   = "user_id"
	ScopeKeyAgentID  = "agent_id"
)

// Default TTL values for [DefaultTTLPolicy]. Zero duration means no expiry.
const (
	TTLDecision    = 7 * 24 * time.Hour
	TTLFact        = 0
	TTLPreference  = 0
	TTLInstruction = 0
	TTLNote        = 48 * time.Hour
)

// Context keys used by [DefaultScopeConfig] resolvers.
var (
	ctxKeyTenantID = ctxKey{"memory:tenant_id"}
	ctxKeyUserID   = ctxKey{"memory:user_id"}
	ctxKeyAgentID  = ctxKey{"memory:agent_id"}
)

// DefaultConfig returns a [Config] with SDK defaults for scope, TTL, store, and recall policies.
func DefaultConfig(store interfaces.Memory) Config {
	return Config{
		Memory:      store,
		ScopeConfig: DefaultScopeConfig(),
		TTLPolicy:   DefaultTTLPolicy(),
		Store:       DefaultStoreConfig(),
		Recall:      DefaultRecallConfig(),
	}
}

// DefaultStoreConfig returns SDK defaults for store behavior.
func DefaultStoreConfig() StoreConfig {
	return StoreConfig{
		Mode:          DefaultStoreMode(),
		DedupMinScore: defaultDedupMinScore,
		DefaultKind:   KindNote,
	}
}

// DefaultStoreMode returns the default store mode ([StoreModeOnDemand]).
func DefaultStoreMode() StoreMode {
	return StoreModeOnDemand
}

// DefaultScopeConfig returns SDK defaults: tenant_id, user_id, and agent_id from context.
func DefaultScopeConfig() ScopeConfig {
	return ScopeConfig{
		TenantIDResolver: contextStringResolver(ctxKeyTenantID),
		UserIDResolver:   contextStringResolver(ctxKeyUserID),
		AgentIDResolver:  contextStringResolver(ctxKeyAgentID),
	}
}

// DefaultTTLPolicy returns a convenience TTL map for general-purpose agents.
func DefaultTTLPolicy() TTLPolicy {
	return TTLPolicy{
		KindDecision:    TTLDecision,
		KindFact:        TTLFact,
		KindPreference:  TTLPreference,
		KindInstruction: TTLInstruction,
		KindNote:        TTLNote,
	}
}

func DefaultRecallConfig() RecallConfig {
	return RecallConfig{Enabled: true, Limit: defaultRecallLimit, MinScore: defaultRecallMinScore}
}
