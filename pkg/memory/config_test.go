package memory_test

import (
	"context"
	"testing"
	"time"

	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/memory"
)

type stubMemory struct{}

func (stubMemory) Store(context.Context, interfaces.MemoryScope, interfaces.MemoryRecord, ...interfaces.StoreMemoryOption) (string, error) {
	return "id-1", nil
}
func (stubMemory) Load(context.Context, interfaces.MemoryScope, string, ...interfaces.LoadMemoryOption) ([]interfaces.MemoryEntry, error) {
	return nil, nil
}
func (stubMemory) Clear(context.Context, interfaces.MemoryScope) error { return nil }

func TestDefaultConfig(t *testing.T) {
	cfg := memory.DefaultConfig(stubMemory{})
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if cfg.Recall.Limit != 10 {
		t.Fatalf("recall limit = %d", cfg.Recall.Limit)
	}
	if !cfg.Recall.Enabled {
		t.Fatal("expected recall enabled by default")
	}
	if cfg.Store.Mode != memory.StoreModeOnDemand {
		t.Fatalf("store mode = %q", cfg.Store.Mode)
	}
	if cfg.Store.DedupMinScore != 0.85 {
		t.Fatalf("dedup min score = %v", cfg.Store.DedupMinScore)
	}
}

func TestConfig_Validate_extractWithOnDemand(t *testing.T) {
	cfg := memory.DefaultConfig(stubMemory{})
	cfg.Store.Extract = func(context.Context, []interfaces.Message) ([]interfaces.MemoryRecord, error) {
		return nil, nil
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error when Extract is set with OnDemand")
	}
}

func TestConfig_Validate_invalidStoreMode(t *testing.T) {
	cfg := memory.DefaultConfig(stubMemory{})
	cfg.Store.Mode = "invalid"
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for invalid StoreMode")
	}
}

func TestConfig_Validate_invalidDedupMinScore(t *testing.T) {
	cfg := memory.DefaultConfig(stubMemory{})
	cfg.Store.DedupMinScore = 1.5
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected error for DedupMinScore > 1")
	}
}

func TestConfig_Validate_alwaysWithExtract(t *testing.T) {
	cfg := memory.DefaultConfig(stubMemory{})
	cfg.Store.Mode = memory.StoreModeAlways
	cfg.Store.Extract = func(context.Context, []interfaces.Message) ([]interfaces.MemoryRecord, error) {
		return []interfaces.MemoryRecord{{Text: "fact", Kind: memory.KindFact}}, nil
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestStoreConfig_WithDefaults(t *testing.T) {
	got := (memory.StoreConfig{}).WithDefaults()
	if got.Mode != memory.StoreModeOnDemand {
		t.Fatalf("mode = %q", got.Mode)
	}
	if got.DedupMinScore != 0.85 {
		t.Fatalf("dedup = %v", got.DedupMinScore)
	}
	if got.DefaultKind != memory.KindNote {
		t.Fatalf("default kind = %q", got.DefaultKind)
	}
}

func TestStoreConfig_ResolveKind(t *testing.T) {
	s := memory.StoreConfig{DefaultKind: memory.KindFact}
	got, err := s.ResolveKind("")
	if err != nil || got != memory.KindFact {
		t.Fatalf("got %q err=%v", got, err)
	}

	s = memory.StoreConfig{AllowedKinds: []interfaces.MemoryKind{memory.KindFact}}
	if _, err := s.ResolveKind(memory.KindNote); err == nil {
		t.Fatal("expected allowlist error")
	}
}

func TestDefaultStoreConfig(t *testing.T) {
	got := memory.DefaultStoreConfig()
	if got.Mode != memory.StoreModeOnDemand || got.DedupMinScore != 0.85 || got.DefaultKind != memory.KindNote {
		t.Fatalf("got = %+v", got)
	}
}

func TestConfig_WithDefaults_appliesStore(t *testing.T) {
	cfg := (memory.Config{Memory: stubMemory{}}).WithDefaults()
	if cfg.Store.Mode != memory.StoreModeOnDemand {
		t.Fatalf("store mode = %q", cfg.Store.Mode)
	}
	if cfg.Store.DefaultKind != memory.KindNote {
		t.Fatalf("default kind = %q", cfg.Store.DefaultKind)
	}
}

func TestConfig_WithDefaults(t *testing.T) {
	cfg := (memory.Config{Memory: stubMemory{}}).WithDefaults()
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
}

func TestConfig_Validate_missingMemory(t *testing.T) {
	if err := (memory.Config{}).Validate(); err == nil {
		t.Fatal("expected error for missing store")
	}
}

func TestConfig_ExpiresAtForKind(t *testing.T) {
	cfg := memory.DefaultConfig(stubMemory{})
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	exp := cfg.ExpiresAtForKind(memory.KindDecision, now)
	if !exp.Equal(now.Add(memory.TTLDecision)) {
		t.Fatalf("expires = %v", exp)
	}
}

func TestRecallConfig_LoadOptions(t *testing.T) {
	opts := (memory.RecallConfig{Limit: 5, MinScore: 0.5, Kinds: []interfaces.MemoryKind{memory.KindFact}}).LoadOptions()
	if len(opts) != 3 {
		t.Fatalf("opts len = %d", len(opts))
	}
}

func TestDefaultScopeConfig_Resolve(t *testing.T) {
	ctx := context.Background()
	ctx = memory.WithContextTenantID(ctx, "tenant-1")
	ctx = memory.WithContextUserID(ctx, "user-1")
	ctx = memory.WithContextAgentID(ctx, "agent-1")

	scope, err := memory.DefaultScopeConfig().Resolve(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if scope.TenantID != "tenant-1" || scope.UserID != "user-1" || scope.AgentID != "agent-1" {
		t.Fatalf("scope = %+v", scope)
	}
}

func TestScopeConfig_Resolve_extraKeys(t *testing.T) {
	cfg := memory.ScopeConfig{
		TenantIDResolver: func(ctx context.Context) string { return "t1" },
		ExtraKeys:        []string{"project_id", "env"},
		TagResolvers: map[string]memory.ScopeResolver{
			"project_id": func(ctx context.Context) string { return "proj-a" },
			"env":        func(ctx context.Context) string { return "prod" },
		},
	}
	scope, err := cfg.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if scope.Tags["project_id"] != "proj-a" || scope.Tags["env"] != "prod" {
		t.Fatalf("tags = %+v", scope.Tags)
	}
}

func TestScopeConfig_Validate_missingTagResolver(t *testing.T) {
	cfg := memory.ScopeConfig{
		ExtraKeys: []string{"project_id"},
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected validation error")
	}
}

func TestScopeConfig_Resolve_customResolvers(t *testing.T) {
	cfg := memory.ScopeConfig{
		TenantIDResolver: func(ctx context.Context) string { return "custom-tenant" },
		UserIDResolver:   func(ctx context.Context) string { return "custom-user" },
	}
	scope, err := cfg.Resolve(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if scope.TenantID != "custom-tenant" || scope.UserID != "custom-user" {
		t.Fatalf("scope = %+v", scope)
	}
}

func TestScopeMetadata(t *testing.T) {
	meta := memory.ScopeMetadata(interfaces.MemoryScope{
		TenantID: "t1",
		UserID:   "u1",
		Tags: map[string]string{
			"project_id":          "p1",
			memory.ScopeKeyUserID: "ignored",
		},
	})
	if meta[memory.ScopeKeyTenantID] != "t1" || meta[memory.ScopeKeyUserID] != "u1" {
		t.Fatalf("meta = %+v", meta)
	}
	if meta["project_id"] != "p1" {
		t.Fatalf("tags = %+v", meta)
	}
	if _, ok := meta["ignored"]; ok {
		t.Fatal("conflicting tag key should be skipped")
	}
}

func TestTTLPolicy_ExpiresAt(t *testing.T) {
	now := time.Date(2026, 6, 18, 12, 0, 0, 0, time.UTC)
	p := memory.DefaultTTLPolicy()

	if got := p.ExpiresAt(memory.KindDecision, now); !got.Equal(now.Add(memory.TTLDecision)) {
		t.Fatalf("decision expiry = %v, want %v", got, now.Add(memory.TTLDecision))
	}
	if !p.ExpiresAt(memory.KindFact, now).IsZero() {
		t.Fatal("fact should not expire")
	}
	if !p.ExpiresAt(interfaces.MemoryKind("custom"), now).IsZero() {
		t.Fatal("unknown kind should not expire")
	}
	if !p.ExpiresAt("", now).IsZero() {
		t.Fatal("empty kind should not expire")
	}
}

func TestTTLPolicy_ExpiresAt_nilPolicy(t *testing.T) {
	var p memory.TTLPolicy
	if !p.ExpiresAt(memory.KindNote, time.Now()).IsZero() {
		t.Fatal("nil policy should not expire")
	}
}
