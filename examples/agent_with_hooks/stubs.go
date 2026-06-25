package main

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/google/uuid"
)

// demoRetriever returns fixed knowledge-base documents for prefetch / agentic retrieval demos.
type demoRetriever struct{}

func (demoRetriever) Name() string { return "demo-kb" }

func (demoRetriever) Search(_ context.Context, query string) ([]interfaces.Document, error) {
	_ = query
	return []interfaces.Document{
		{
			Content:  "Return policy: full refund within 30 days. Customer record includes SSN 123-45-6789.",
			Source:   "kb/returns",
			Score:    0.92,
			Metadata: map[string]any{"section": "returns"},
		},
		{
			Content:  "Shipping is free on orders over $50.",
			Source:   "kb/shipping",
			Score:    0.81,
			Metadata: map[string]any{"section": "shipping"},
		},
	}, nil
}

// demoMemory is a minimal in-process [interfaces.Memory] for examples (no external DB).
type demoMemory struct {
	mu      sync.RWMutex
	records map[string]demoMemRecord
}

type demoMemRecord struct {
	entry interfaces.MemoryEntry
}

func newDemoMemory() *demoMemory {
	return &demoMemory{records: make(map[string]demoMemRecord)}
}

func (m *demoMemory) Store(_ context.Context, scope interfaces.MemoryScope, rec interfaces.MemoryRecord, opts ...interfaces.StoreMemoryOption) (string, error) {
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

	createdAt := now
	if existing, ok := m.records[id]; ok {
		createdAt = existing.entry.CreatedAt
	}
	m.records[id] = demoMemRecord{entry: interfaces.MemoryEntry{
		ID:        id,
		Text:      rec.Text,
		Kind:      rec.Kind,
		Scope:     scope,
		Metadata:  rec.Metadata,
		ExpiresAt: rec.ExpiresAt,
		CreatedAt: createdAt,
		UpdatedAt: now,
	}}
	return id, nil
}

func (m *demoMemory) Load(_ context.Context, scope interfaces.MemoryScope, query string, opts ...interfaces.LoadMemoryOption) ([]interfaces.MemoryEntry, error) {
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

	query = strings.ToLower(strings.TrimSpace(query))
	recencyOnly := query == ""
	var out []interfaces.MemoryEntry
	for _, rec := range m.records {
		entry := rec.entry
		if entry.Expired() {
			continue
		}
		if !demoScopeMatches(entry.Scope, scope) {
			continue
		}
		if query != "" && !strings.Contains(strings.ToLower(entry.Text), query) {
			continue
		}
		if !recencyOnly {
			entry.Score = 1.0
		}
		out = append(out, entry)
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (m *demoMemory) Clear(_ context.Context, scope interfaces.MemoryScope) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id, rec := range m.records {
		if demoScopeMatches(rec.entry.Scope, scope) {
			delete(m.records, id)
		}
	}
	return nil
}

func demoScopeMatches(stored, filter interfaces.MemoryScope) bool {
	if filter.UserID != "" && stored.UserID != filter.UserID {
		return false
	}
	if filter.TenantID != "" && stored.TenantID != filter.TenantID {
		return false
	}
	if filter.AgentID != "" && stored.AgentID != filter.AgentID {
		return false
	}
	return true
}

// demoMemoryExtract returns a deterministic memory for run-end store (no extra LLM call).
func demoMemoryExtract(_ context.Context, _ []interfaces.Message) ([]interfaces.MemoryRecord, error) {
	return []interfaces.MemoryRecord{{
		Text:     "User prefers bullet-point answers. Contact email on file: alice@example.com",
		Kind:     "preference",
		Metadata: map[string]string{"source": "extract"},
	}}, nil
}
