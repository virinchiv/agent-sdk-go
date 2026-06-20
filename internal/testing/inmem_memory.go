// Package testutil provides test helpers for the agent SDK.
package testutil

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/memory"
	"github.com/google/uuid"
)

var _ interfaces.Memory = (*InmemMemory)(nil)

type record struct {
	entry interfaces.MemoryEntry
}

// InmemMemory is a mutex-protected in-memory [interfaces.Memory] for tests.
type InmemMemory struct {
	mu      sync.RWMutex
	records map[string]record
}

// NewInmemMemory returns an empty in-memory store.
func NewInmemMemory() *InmemMemory {
	return &InmemMemory{records: make(map[string]record)}
}

// Store persists a memory in scope and returns its ID.
func (m *InmemMemory) Store(ctx context.Context, scope interfaces.MemoryScope, rec interfaces.MemoryRecord, opts ...interfaces.StoreMemoryOption) (string, error) {
	_ = ctx
	storeOpts := interfaces.StoreMemoryOptions{}
	for _, opt := range opts {
		opt(&storeOpts)
	}

	id := strings.TrimSpace(storeOpts.ID)
	if id == "" {
		id = uuid.NewString()
	}

	now := time.Now().UTC()
	m.mu.Lock()
	defer m.mu.Unlock()

	existing, ok := m.records[id]
	createdAt := now
	if ok {
		createdAt = existing.entry.CreatedAt
	}

	entry := interfaces.MemoryEntry{
		ID:        id,
		Text:      rec.Text,
		Kind:      rec.Kind,
		Scope:     cloneScope(scope),
		Metadata:  cloneMetadata(rec.Metadata),
		ExpiresAt: rec.ExpiresAt.UTC(),
		CreatedAt: createdAt,
		UpdatedAt: now,
	}
	m.records[id] = record{entry: entry}
	return id, nil
}

// Load retrieves memories within scope.
func (m *InmemMemory) Load(ctx context.Context, scope interfaces.MemoryScope, query string, opts ...interfaces.LoadMemoryOption) ([]interfaces.MemoryEntry, error) {
	_ = ctx
	loadOpts := interfaces.LoadMemoryOptions{}
	for _, opt := range opts {
		opt(&loadOpts)
	}
	limit := loadOpts.Limit
	if limit <= 0 {
		limit = 10
	}

	m.mu.RLock()
	defer m.mu.RUnlock()

	var matches []interfaces.MemoryEntry
	query = strings.ToLower(strings.TrimSpace(query))
	recencyOnly := query == ""
	for _, rec := range m.records {
		entry := rec.entry
		if entry.Expired() {
			continue
		}
		if !scopeMatches(entry.Scope, scope) {
			continue
		}
		if !kindMatches(entry.Kind, loadOpts.Kinds) {
			continue
		}
		matched := entry
		if !recencyOnly {
			if !strings.Contains(strings.ToLower(entry.Text), query) {
				continue
			}
			matched.Score = 1.0
		}
		if !recencyOnly && loadOpts.MinScore > 0 && matched.Score < loadOpts.MinScore {
			continue
		}
		matches = append(matches, matched)
	}

	sort.Slice(matches, func(i, j int) bool {
		return matches[i].UpdatedAt.After(matches[j].UpdatedAt)
	})
	if len(matches) > limit {
		matches = matches[:limit]
	}
	return cloneEntries(matches), nil
}

// Clear removes all memories matching scope.
func (m *InmemMemory) Clear(ctx context.Context, scope interfaces.MemoryScope) error {
	_ = ctx
	if scopeIsEmpty(scope) {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, rec := range m.records {
		if scopeMatches(rec.entry.Scope, scope) {
			delete(m.records, id)
		}
	}
	return nil
}

func scopeMatches(stored, filter interfaces.MemoryScope) bool {
	metaStored := memory.ScopeMetadata(stored)
	metaFilter := memory.ScopeMetadata(filter)
	for key, want := range metaFilter {
		if got, ok := metaStored[key]; !ok || got != want {
			return false
		}
	}
	return true
}

func kindMatches(kind interfaces.MemoryKind, kinds []interfaces.MemoryKind) bool {
	if len(kinds) == 0 {
		return true
	}
	for _, allowed := range kinds {
		if kind == allowed {
			return true
		}
	}
	return false
}

func scopeIsEmpty(scope interfaces.MemoryScope) bool {
	return scope.UserID == "" && scope.TenantID == "" && scope.AgentID == "" && len(scope.Tags) == 0
}

func cloneScope(scope interfaces.MemoryScope) interfaces.MemoryScope {
	out := interfaces.MemoryScope{
		UserID:   scope.UserID,
		TenantID: scope.TenantID,
		AgentID:  scope.AgentID,
	}
	if len(scope.Tags) > 0 {
		out.Tags = make(map[string]string, len(scope.Tags))
		for k, v := range scope.Tags {
			out.Tags[k] = v
		}
	}
	return out
}

func cloneMetadata(metadata map[string]string) map[string]string {
	if len(metadata) == 0 {
		return nil
	}
	raw, _ := json.Marshal(metadata)
	var out map[string]string
	_ = json.Unmarshal(raw, &out)
	return out
}

func cloneEntries(entries []interfaces.MemoryEntry) []interfaces.MemoryEntry {
	out := make([]interfaces.MemoryEntry, len(entries))
	for i, entry := range entries {
		out[i] = interfaces.MemoryEntry{
			ID:        entry.ID,
			Text:      entry.Text,
			Kind:      entry.Kind,
			Scope:     cloneScope(entry.Scope),
			Metadata:  cloneMetadata(entry.Metadata),
			ExpiresAt: entry.ExpiresAt,
			Score:     entry.Score,
			CreatedAt: entry.CreatedAt,
			UpdatedAt: entry.UpdatedAt,
		}
	}
	return out
}
