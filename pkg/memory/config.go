package memory

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

// StoreMode selects when the SDK persists long-term memories.
type StoreMode string

const (
	// StoreModeOnDemand registers the save_memory tool; the LLM stores via tool calls (default).
	StoreModeOnDemand StoreMode = "ondemand"
	// StoreModeAlways extracts memories at run end and stores them automatically.
	StoreModeAlways StoreMode = "always"
)

// ExtractFunc extracts long-term memories from a completed run.
// Used only when [StoreMode] is [StoreModeAlways]. Nil uses the SDK default LLM extractor.
type ExtractFunc func(ctx context.Context, messages []interfaces.Message) ([]interfaces.MemoryRecord, error)

// Config wires long-term memory for the agent SDK.
type Config struct {
	// Memory is the memory backend implementation. Required.
	// Shipped backends: pkg/memory/weaviate and pkg/memory/pgvector.
	Memory interfaces.Memory

	// ScopeConfig resolves [interfaces.MemoryScope] from request context.
	ScopeConfig ScopeConfig

	// TTLPolicy sets ExpiresAt at Store time from record kind.
	TTLPolicy TTLPolicy

	// Store controls when and how memories are written.
	Store StoreConfig

	// Recall controls Load behavior when the SDK recalls memories for a run.
	Recall RecallConfig
}

// StoreConfig controls store timing, extraction, deduplication, and kind policy.
type StoreConfig struct {
	// Mode controls when memories are written. Default [StoreModeOnDemand].
	Mode StoreMode

	// DedupMinScore is the semantic similarity floor for upserting an existing memory instead of appending.
	// Zero defaults to [defaultDedupMinScore].
	DedupMinScore float32

	// Extract overrides run-end memory extraction when [Mode] is [StoreModeAlways].
	// Must be nil when [Mode] is [StoreModeOnDemand].
	Extract ExtractFunc

	// DefaultKind is used when the record kind is empty. Zero defaults to [KindNote].
	DefaultKind interfaces.MemoryKind

	// AllowedKinds restricts stored kinds when non-empty.
	AllowedKinds []interfaces.MemoryKind
}

// ScopeResolver extracts one scope field value from the request context.
type ScopeResolver func(ctx context.Context) string

// ScopeConfig controls how the agent SDK builds [interfaces.MemoryScope] from context.
// Configured via [Config.ScopeConfig] and agent [WithMemory].
//
// Defaults resolve tenant_id, user_id, and agent_id from context values set by
// [WithContextTenantID], [WithContextUserID], and [WithContextAgentID].
// Override any field with a custom resolver when your auth or tenancy model differs.
type ScopeConfig struct {
	TenantIDResolver ScopeResolver
	UserIDResolver   ScopeResolver
	AgentIDResolver  ScopeResolver

	// ExtraKeys lists tag keys always resolved on Store/Load/Clear (e.g. "project_id", "env").
	// Each key must have a matching entry in TagResolvers.
	ExtraKeys []string

	// TagResolvers supplies values for [ExtraKeys].
	TagResolvers map[string]ScopeResolver
}

// TTLPolicy maps memory kind strings to time-to-live.
// Zero duration means no expiry for that kind. Opt-in via agent config; not enforced by [interfaces.Memory].
type TTLPolicy map[interfaces.MemoryKind]time.Duration

// RecallConfig controls SDK-initiated memory Load before or during a run.
type RecallConfig struct {
	// Enabled recalls memories automatically for each run when true (default).
	// When false the SDK still stores memories after each run but skips Load.
	Enabled bool

	// Limit is the maximum number of memories to load. Zero or negative defaults to [defaultRecallLimit].
	Limit int

	// MinScore filters out entries below this relevance score when Score is applicable.
	MinScore float32

	// Kinds restricts recall to these kinds. Empty means all kinds.
	Kinds []interfaces.MemoryKind
}

type ctxKey struct{ name string }

// WithContextTenantID attaches tenant ID for default scope resolution.
func WithContextTenantID(ctx context.Context, tenantID string) context.Context {
	return context.WithValue(ctx, ctxKeyTenantID, tenantID)
}

// WithContextUserID attaches user ID for default scope resolution.
func WithContextUserID(ctx context.Context, userID string) context.Context {
	return context.WithValue(ctx, ctxKeyUserID, userID)
}

// WithContextAgentID attaches agent ID for default scope resolution.
func WithContextAgentID(ctx context.Context, agentID string) context.Context {
	return context.WithValue(ctx, ctxKeyAgentID, agentID)
}

// Validate checks that every [ScopeConfig.ExtraKeys] entry has a TagResolver.
func (c ScopeConfig) Validate() error {
	for _, key := range c.ExtraKeys {
		if c.TagResolvers == nil || c.TagResolvers[key] == nil {
			return fmt.Errorf("memory scope: ExtraKeys %q requires a TagResolver", key)
		}
	}
	return nil
}

// Resolve builds a [interfaces.MemoryScope] from context using configured resolvers.
func (c ScopeConfig) Resolve(ctx context.Context) (interfaces.MemoryScope, error) {
	if err := c.Validate(); err != nil {
		return interfaces.MemoryScope{}, err
	}

	scope := interfaces.MemoryScope{}
	if c.TenantIDResolver != nil {
		scope.TenantID = c.TenantIDResolver(ctx)
	}
	if c.UserIDResolver != nil {
		scope.UserID = c.UserIDResolver(ctx)
	}
	if c.AgentIDResolver != nil {
		scope.AgentID = c.AgentIDResolver(ctx)
	}

	if len(c.ExtraKeys) > 0 {
		tags := make(map[string]string, len(c.ExtraKeys))
		for _, key := range c.ExtraKeys {
			if v := c.TagResolvers[key](ctx); v != "" {
				tags[key] = v
			}
		}
		if len(tags) > 0 {
			scope.Tags = tags
		}
	}

	return scope, nil
}

// ScopeMetadata returns a flat map of scope fields for vector-store or metadata filters.
// SDK default keys are defined in [ScopeKeyTenantID], [ScopeKeyUserID], and [ScopeKeyAgentID].
// Struct fields take precedence over conflicting tag keys.
func ScopeMetadata(s interfaces.MemoryScope) map[string]string {
	out := make(map[string]string, len(s.Tags)+3)
	for k, v := range s.Tags {
		if k == ScopeKeyUserID || k == ScopeKeyTenantID || k == ScopeKeyAgentID {
			continue
		}
		out[k] = v
	}
	if s.UserID != "" {
		out[ScopeKeyUserID] = s.UserID
	}
	if s.TenantID != "" {
		out[ScopeKeyTenantID] = s.TenantID
	}
	if s.AgentID != "" {
		out[ScopeKeyAgentID] = s.AgentID
	}
	return out
}

// ExpiresAt returns the expiry timestamp for kind at now.
// Empty or unknown kinds return zero time (no expiry) unless present in the policy map.
func (p TTLPolicy) ExpiresAt(kind interfaces.MemoryKind, now time.Time) time.Time {
	if len(p) == 0 {
		return time.Time{}
	}
	ttl, ok := p[kind]
	if !ok {
		return time.Time{}
	}
	if ttl <= 0 {
		return time.Time{}
	}
	return now.Add(ttl)
}

// ResolveKind returns the kind to store, applying default and allowlist policy.
func (s StoreConfig) ResolveKind(kind interfaces.MemoryKind) (interfaces.MemoryKind, error) {
	if kind == "" {
		kind = s.DefaultKind
	}
	if kind == "" {
		kind = KindNote
	}
	if len(s.AllowedKinds) == 0 {
		return kind, nil
	}
	for _, allowed := range s.AllowedKinds {
		if kind == allowed {
			return kind, nil
		}
	}
	return "", fmt.Errorf("memory store: kind %q is not allowed", kind)
}

// WithDefaults fills zero store fields with SDK defaults.
func (s StoreConfig) WithDefaults() StoreConfig {
	if s.Mode == "" {
		s.Mode = DefaultStoreMode()
	}
	if s.DedupMinScore <= 0 {
		s.DedupMinScore = defaultDedupMinScore
	}
	if s.DefaultKind == "" && len(s.AllowedKinds) == 0 {
		s.DefaultKind = KindNote
	}
	return s
}

// Validate checks store configuration.
func (s StoreConfig) Validate() error {
	if _, err := s.ResolveKind(""); err != nil {
		return err
	}
	switch s.Mode {
	case StoreModeOnDemand, StoreModeAlways:
	default:
		return fmt.Errorf("memory config: invalid StoreMode %q", s.Mode)
	}
	if s.Mode == StoreModeOnDemand && s.Extract != nil {
		return errors.New("memory config: Extract is only valid with StoreMode always")
	}
	if s.DedupMinScore < 0 || s.DedupMinScore > 1 {
		return fmt.Errorf("memory config: DedupMinScore must be between 0 and 1, got %v", s.DedupMinScore)
	}
	return nil
}

// WithDefaults fills zero policy fields with SDK defaults. Memory must be set separately.
func (c Config) WithDefaults() Config {
	if c.ScopeConfig.TenantIDResolver == nil &&
		c.ScopeConfig.UserIDResolver == nil &&
		c.ScopeConfig.AgentIDResolver == nil &&
		len(c.ScopeConfig.ExtraKeys) == 0 {
		c.ScopeConfig = DefaultScopeConfig()
	}
	if len(c.TTLPolicy) == 0 {
		c.TTLPolicy = DefaultTTLPolicy()
	}
	c.Store = c.Store.WithDefaults()
	if c.Recall.Limit <= 0 {
		c.Recall.Limit = defaultRecallLimit
	}
	return c
}

// Validate checks the config. Call [WithDefaults] first.
func (c Config) Validate() error {
	if c.Memory == nil {
		return errors.New("memory config: Memory is required")
	}
	if err := c.ScopeConfig.Validate(); err != nil {
		return err
	}
	return c.Store.Validate()
}

// ExpiresAtForKind returns expiry for kind at now using the config TTL policy.
func (c Config) ExpiresAtForKind(kind interfaces.MemoryKind, now time.Time) time.Time {
	resolved, err := c.Store.ResolveKind(kind)
	if err != nil {
		return time.Time{}
	}
	return c.TTLPolicy.ExpiresAt(resolved, now)
}

// LoadOptions builds [interfaces.LoadMemoryOption] values from recall settings.
func (r RecallConfig) LoadOptions() []interfaces.LoadMemoryOption {
	return r.loadOptions(true)
}

// RecencyLoadOptions builds load options for scoped recency listing (no semantic min score).
func (r RecallConfig) RecencyLoadOptions() []interfaces.LoadMemoryOption {
	return r.loadOptions(false)
}

func (r RecallConfig) loadOptions(withMinScore bool) []interfaces.LoadMemoryOption {
	opts := []interfaces.LoadMemoryOption{
		interfaces.WithLoadLimit(r.Limit),
	}
	if withMinScore && r.MinScore > 0 {
		opts = append(opts, interfaces.WithMinScore(r.MinScore))
	}
	if len(r.Kinds) > 0 {
		opts = append(opts, interfaces.WithLoadKinds(r.Kinds...))
	}
	return opts
}

func contextStringResolver(key ctxKey) ScopeResolver {
	return func(ctx context.Context) string {
		v, _ := ctx.Value(key).(string)
		return v
	}
}
