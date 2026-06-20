package interfaces

import (
	"context"
	"time"
)

//go:generate mockgen -destination=./mocks/mock_memory.go -package=mocks github.com/agenticenv/agent-sdk-go/pkg/interfaces Memory

// MemoryKind is an arbitrary label for categorizing memories (e.g. "preference", "fact", "bug_report").
// Different agents define their own taxonomies; the interface does not prescribe fixed kinds.
type MemoryKind string

// MemoryScope identifies the namespace a memory belongs to.
// Non-empty fields are AND-ed when storing, loading, or clearing.
//
// UserID, TenantID, and AgentID are common isolation dimensions; Tags hold custom keys
// (e.g. project_id, env). The agent SDK may populate scope via pkg/memory ScopeConfig;
// implementations only see the resolved [MemoryScope] values.
type MemoryScope struct {
	UserID   string
	TenantID string
	AgentID  string

	// Tags holds additional scope dimensions (e.g. project, team, environment).
	// Do not duplicate UserID, TenantID, or AgentID as tag keys.
	Tags map[string]string
}

// MemoryRecord is the content written to long-term storage.
type MemoryRecord struct {
	// Text is the distilled fact, preference, or instruction to remember.
	Text string

	// Kind is an optional category label for filtered recall. Empty is allowed.
	Kind MemoryKind

	// Metadata holds optional attributes (source run, confidence, custom tags).
	// Scope fields from [MemoryScope] are stored separately and should not be duplicated here.
	Metadata map[string]string

	// ExpiresAt is when the entry should no longer be recalled. Zero means no expiry.
	// The agent SDK sets this from [memory.Config] TTL policy on Store; direct callers may leave it zero.
	ExpiresAt time.Time
}

// MemoryEntry is a memory returned from [Memory.Load].
type MemoryEntry struct {
	// ID is the stable record identifier assigned by the backend.
	// Always non-empty; required for upserts via [WithMemoryID].
	ID string

	Text      string
	Kind      MemoryKind
	Scope     MemoryScope
	Metadata  map[string]string
	ExpiresAt time.Time

	// Score is query relevance when the backend supports ranked retrieval (e.g. vector search).
	// Zero means not applicable (e.g. key-value or recency-only backends).
	Score float32

	CreatedAt time.Time
	UpdatedAt time.Time
}

// Expired reports whether the entry has passed its expiry time.
// Entries with a zero ExpiresAt never expire.
func (e MemoryEntry) Expired() bool {
	return !e.ExpiresAt.IsZero() && time.Now().After(e.ExpiresAt)
}

// Memory stores and retrieves long-term agent context across runs.
//
// Store/Load/Clear with scope and query work with vector, relational, or key-value backends.
// Scope drives isolation; query semantics are backend-specific (semantic search, filter, etc.).
//
// Agent SDK usage (runtime):
//  1. Store — persist extracted context after a run.
//  2. Load  — recall memories for the current query before or during a run.
//
// Application usage (optional, not invoked by agent runtime):
//  3. Clear — remove all memories in a scope (e.g. tenant offboarding, forget-me).
type Memory interface {
	// Store persists a new memory in the given scope and returns its assigned ID.
	// The returned ID is always non-empty; implementations must assign one if the
	// backend does not (e.g. a UUID).
	//
	// Expiry (ExpiresAt on [MemoryRecord]) is set by the agent SDK from TTL policy.
	// Use [WithMemoryID] to upsert an existing record.
	Store(ctx context.Context, scope MemoryScope, record MemoryRecord, opts ...StoreMemoryOption) (id string, err error)

	// Load retrieves memories within scope.
	// Query drives ranking or filtering; pass empty query to list by recency when supported.
	// Implementations must omit expired entries (ExpiresAt non-zero and in the past).
	//
	// Non-empty scope fields filter results (AND). Use [WithLoadLimit], [WithMinScore],
	// and [WithLoadKinds] to narrow recall.
	Load(ctx context.Context, scope MemoryScope, query string, opts ...LoadMemoryOption) ([]MemoryEntry, error)

	// Clear removes all memories matching the scope. Called by the application when
	// required (e.g. user offboarding). Not invoked by the agent runtime.
	// Warning: a TenantID-only scope deletes all memories for that tenant across every user and agent.
	Clear(ctx context.Context, scope MemoryScope) error
}

// --- Store options ---

// StoreMemoryOptions configures a [Memory.Store] call.
type StoreMemoryOptions struct {
	// ID upserts the record when non-empty. Use the ID returned by a prior Store call.
	ID string
}

type StoreMemoryOption func(*StoreMemoryOptions)

// WithMemoryID upserts the record with the given ID when the backend supports
// stable identifiers. Use the ID returned by a prior [Memory.Store] call.
func WithMemoryID(id string) StoreMemoryOption {
	return func(o *StoreMemoryOptions) {
		o.ID = id
	}
}

// --- Load options ---

// LoadMemoryOptions configures a [Memory.Load] call.
type LoadMemoryOptions struct {
	// Limit is the maximum number of memories to return. Zero or negative means backend default.
	Limit int

	// MinScore filters out entries below the given relevance score when Score is applicable.
	MinScore float32

	// Kinds restricts recall to the given memory kinds. Empty means all kinds.
	Kinds []MemoryKind
}

type LoadMemoryOption func(*LoadMemoryOptions)

// WithLoadLimit sets the maximum number of memories to return.
// Zero or negative means backend default.
func WithLoadLimit(limit int) LoadMemoryOption {
	return func(o *LoadMemoryOptions) {
		o.Limit = limit
	}
}

// WithMinScore filters out entries below the given relevance score.
func WithMinScore(minScore float32) LoadMemoryOption {
	return func(o *LoadMemoryOptions) {
		o.MinScore = minScore
	}
}

// WithLoadKinds restricts recall to the given memory kinds.
func WithLoadKinds(kinds ...MemoryKind) LoadMemoryOption {
	return func(o *LoadMemoryOptions) {
		o.Kinds = kinds
	}
}
