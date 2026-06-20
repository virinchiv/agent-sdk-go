// Package weaviate provides a [interfaces.Memory] implementation backed by Weaviate.
//
// Expected class schema (vectorizer should target the text field):
//
//	{
//	  "class": "AgentMemory",
//	  "vectorizer": "text2vec-...",
//	  "properties": [
//	    {"name": "text", "dataType": ["text"]},
//	    {"name": "kind", "dataType": ["text"]},
//	    {"name": "user_id", "dataType": ["text"]},
//	    {"name": "tenant_id", "dataType": ["text"]},
//	    {"name": "agent_id", "dataType": ["text"]},
//	    {"name": "scope_tags", "dataType": ["text[]"]},
//	    {"name": "metadata", "dataType": ["text"]},
//	    {"name": "expires_at", "dataType": ["date"]},
//	    {"name": "created_at", "dataType": ["date"]},
//	    {"name": "updated_at", "dataType": ["date"]}
//	  ]
//	}
package weaviate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
	"github.com/agenticenv/agent-sdk-go/pkg/memory"
	"github.com/google/uuid"
	client "github.com/weaviate/weaviate-go-client/v5/weaviate"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/data"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/fault"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/filters"
	"github.com/weaviate/weaviate-go-client/v5/weaviate/graphql"
	"github.com/weaviate/weaviate/entities/models"
)

var _ interfaces.Memory = (*Memory)(nil)

// Memory stores and recalls agent memories in a Weaviate class.
type Memory struct {
	className string
	textField string
	tenant    string

	defaultLimit    int
	defaultMinScore float32

	logger logger.Logger
	client *client.Client

	host     string
	scheme   string
	logLevel string
}

// Option configures [Memory].
type Option func(*Memory)

// WithClient sets the Weaviate client.
func WithClient(c *client.Client) Option {
	return func(m *Memory) { m.client = c }
}

// WithHost sets the Weaviate host (required when [WithClient] is omitted).
func WithHost(host string) Option {
	return func(m *Memory) { m.host = host }
}

// WithScheme sets the Weaviate scheme. Defaults to [types.DefaultScheme].
func WithScheme(scheme string) Option {
	return func(m *Memory) { m.scheme = scheme }
}

// WithClassName sets the Weaviate class name. Defaults to [DefaultClassName].
func WithClassName(className string) Option {
	return func(m *Memory) { m.className = className }
}

// WithTextField sets the property used for memory text and vectorization. Defaults to [DefaultTextField].
func WithTextField(textField string) Option {
	return func(m *Memory) { m.textField = textField }
}

// WithTenant sets the Weaviate multi-tenancy tenant for all operations.
func WithTenant(tenant string) Option {
	return func(m *Memory) { m.tenant = tenant }
}

// WithDefaultLimit sets the load limit when callers omit [interfaces.WithLoadLimit].
func WithDefaultLimit(limit int) Option {
	return func(m *Memory) { m.defaultLimit = limit }
}

// WithDefaultMinScore sets the nearText certainty when callers omit [interfaces.WithMinScore].
func WithDefaultMinScore(minScore float32) Option {
	return func(m *Memory) { m.defaultMinScore = minScore }
}

// WithLogger sets the logger.
func WithLogger(l logger.Logger) Option {
	return func(m *Memory) { m.logger = l }
}

// WithLogLevel sets the log level when no logger is provided.
func WithLogLevel(logLevel string) Option {
	return func(m *Memory) { m.logLevel = logLevel }
}

// NewMemory builds a Weaviate-backed [interfaces.Memory]. When [WithClient] is omitted, host is required.
func NewMemory(opts ...Option) (*Memory, error) {
	m := &Memory{}
	for _, opt := range opts {
		opt(m)
	}
	if m.className == "" {
		m.className = DefaultClassName
	}
	if m.textField == "" {
		m.textField = DefaultTextField
	}
	if m.defaultLimit <= 0 {
		m.defaultLimit = DefaultLoadLimit
	}
	if m.defaultMinScore == 0 {
		m.defaultMinScore = DefaultMinScore
	}
	if m.logLevel == "" {
		m.logLevel = "error"
	}
	if m.logger == nil {
		m.logger = logger.DefaultLogger(m.logLevel)
	}
	if m.client == nil {
		if m.host == "" {
			return nil, errors.New("host is required when not using WithClient")
		}
		if m.scheme == "" {
			m.scheme = types.DefaultScheme
		}
		wc, err := client.NewClient(client.Config{
			Scheme: m.scheme,
			Host:   m.host,
		})
		if err != nil {
			return nil, fmt.Errorf("create weaviate client: %w", err)
		}
		m.client = wc
	}
	m.logger.Info(context.Background(), "weaviate memory built",
		slog.String("scope", "weaviate-memory"),
		slog.String("class", m.className),
		slog.String("textField", m.textField),
		slog.Int("defaultLimit", m.defaultLimit),
		slog.Float64("defaultMinScore", float64(m.defaultMinScore)),
	)
	return m, nil
}

// Store persists a memory in scope and returns its ID.
func (m *Memory) Store(ctx context.Context, scope interfaces.MemoryScope, record interfaces.MemoryRecord, opts ...interfaces.StoreMemoryOption) (string, error) {
	if m.client == nil {
		return "", errors.New("client is not set")
	}

	storeOpts := interfaces.StoreMemoryOptions{}
	for _, opt := range opts {
		opt(&storeOpts)
	}

	now := time.Now().UTC()
	props, err := buildProperties(m.textField, scope, record, now, storeOpts.ID == "")
	if err != nil {
		return "", err
	}

	id := strings.TrimSpace(storeOpts.ID)
	if id != "" {
		updater := m.client.Data().Updater().
			WithClassName(m.className).
			WithID(id).
			WithProperties(props).
			WithMerge()
		if m.tenant != "" {
			updater = updater.WithTenant(m.tenant)
		}
		if err := updater.Do(ctx); err != nil {
			if !isNotFound(err) {
				return "", fmt.Errorf("weaviate update memory: %w", err)
			}
			creator := m.client.Data().Creator().
				WithClassName(m.className).
				WithID(id).
				WithProperties(props)
			if m.tenant != "" {
				creator = creator.WithTenant(m.tenant)
			}
			wrapper, createErr := creator.Do(ctx)
			if createErr != nil {
				return "", fmt.Errorf("weaviate create memory: %w", createErr)
			}
			return objectID(wrapper), nil
		}
		return id, nil
	}

	creator := m.client.Data().Creator().
		WithClassName(m.className).
		WithProperties(props)
	if m.tenant != "" {
		creator = creator.WithTenant(m.tenant)
	}
	wrapper, err := creator.Do(ctx)
	if err != nil {
		return "", fmt.Errorf("weaviate create memory: %w", err)
	}
	if got := objectID(wrapper); got != "" {
		return got, nil
	}
	return uuid.NewString(), nil
}

// Load recalls memories within scope. Non-empty query uses nearText; empty query lists by updated_at.
func (m *Memory) Load(ctx context.Context, scope interfaces.MemoryScope, query string, opts ...interfaces.LoadMemoryOption) ([]interfaces.MemoryEntry, error) {
	if m.client == nil {
		return nil, errors.New("client is not set")
	}

	loadOpts := interfaces.LoadMemoryOptions{}
	for _, opt := range opts {
		opt(&loadOpts)
	}
	limit := loadOpts.Limit
	if limit <= 0 {
		limit = m.defaultLimit
	}
	minScore := loadOpts.MinScore
	if minScore == 0 {
		minScore = m.defaultMinScore
	}

	where := combineWhere(
		scopeWhere(scope),
		kindsWhere(loadOpts.Kinds),
	)

	fields := []graphql.Field{
		{Name: m.textField},
		{Name: PropKind},
		{Name: PropUserID},
		{Name: PropTenantID},
		{Name: PropAgentID},
		{Name: PropScopeTags},
		{Name: PropMetadata},
		{Name: PropExpiresAt},
		{Name: PropCreatedAt},
		{Name: PropUpdatedAt},
		{Name: "_additional { id certainty }"},
	}

	builder := m.client.GraphQL().Get().
		WithClassName(m.className).
		WithLimit(limit).
		WithFields(fields...).
		WithSort(graphql.Sort{Path: []string{PropUpdatedAt}, Order: graphql.Desc})

	if where != nil {
		builder = builder.WithWhere(where)
	}

	query = strings.TrimSpace(query)
	if query != "" {
		nearText := m.client.GraphQL().
			NearTextArgBuilder().
			WithConcepts([]string{query}).
			WithCertainty(minScore)
		builder = builder.WithNearText(nearText)
	}

	result, err := builder.Do(ctx)
	if err != nil {
		return nil, fmt.Errorf("weaviate load memories: %w", err)
	}
	if err := graphqlErrors(result); err != nil {
		return nil, fmt.Errorf("weaviate load memories: %w", err)
	}

	entries, err := m.parseEntries(result)
	if err != nil {
		return nil, err
	}

	if minScore > 0 && query != "" {
		filtered := entries[:0]
		for _, e := range entries {
			if e.Score >= minScore {
				filtered = append(filtered, e)
			}
		}
		entries = filtered
	}

	return entries, nil
}

// Clear removes all memories matching scope.
func (m *Memory) Clear(ctx context.Context, scope interfaces.MemoryScope) error {
	if m.client == nil {
		return errors.New("client is not set")
	}

	where := scopeWhere(scope)
	if where == nil {
		return errors.New("scope must include at least one non-empty field")
	}

	deleter := m.client.Batch().ObjectsBatchDeleter().
		WithClassName(m.className).
		WithWhere(where)
	if m.tenant != "" {
		deleter = deleter.WithTenant(m.tenant)
	}
	if _, err := deleter.Do(ctx); err != nil {
		return fmt.Errorf("weaviate clear memories: %w", err)
	}
	return nil
}

func (m *Memory) parseEntries(result *models.GraphQLResponse) ([]interfaces.MemoryEntry, error) {
	if result == nil || result.Data == nil {
		return nil, nil
	}

	get, ok := result.Data["Get"].(map[string]interface{})
	if !ok {
		return nil, errors.New("invalid response: missing Get")
	}

	itemsRaw, ok := get[m.className]
	if !ok || itemsRaw == nil {
		return nil, nil
	}
	items, ok := itemsRaw.([]interface{})
	if !ok {
		return nil, errors.New("invalid response: missing class data")
	}

	entries := make([]interfaces.MemoryEntry, 0, len(items))
	for _, item := range items {
		obj, ok := item.(map[string]interface{})
		if !ok {
			m.logger.Warn(context.Background(), "weaviate memory: skipping non-object item",
				slog.String("scope", "weaviate-memory"),
				slog.String("class", m.className),
			)
			continue
		}
		entry, err := parseEntry(obj, m.textField)
		if err != nil {
			return nil, err
		}
		if entry.Expired() {
			continue
		}
		entries = append(entries, entry)
	}
	return entries, nil
}

func buildProperties(textField string, scope interfaces.MemoryScope, record interfaces.MemoryRecord, now time.Time, setCreated bool) (map[string]interface{}, error) {
	meta := memory.ScopeMetadata(scope)
	props := map[string]interface{}{
		textField:     record.Text,
		PropKind:      string(record.Kind),
		PropUpdatedAt: now,
	}
	if setCreated {
		props[PropCreatedAt] = now
	}

	if v := meta[memory.ScopeKeyUserID]; v != "" {
		props[PropUserID] = v
	}
	if v := meta[memory.ScopeKeyTenantID]; v != "" {
		props[PropTenantID] = v
	}
	if v := meta[memory.ScopeKeyAgentID]; v != "" {
		props[PropAgentID] = v
	}
	if tags := encodeScopeTags(meta); len(tags) > 0 {
		props[PropScopeTags] = tags
	}
	if len(record.Metadata) > 0 {
		raw, err := json.Marshal(record.Metadata)
		if err != nil {
			return nil, fmt.Errorf("marshal metadata: %w", err)
		}
		props[PropMetadata] = string(raw)
	}
	if !record.ExpiresAt.IsZero() {
		props[PropExpiresAt] = record.ExpiresAt.UTC()
	}
	return props, nil
}

func parseEntry(obj map[string]interface{}, textField string) (interfaces.MemoryEntry, error) {
	entry := interfaces.MemoryEntry{
		ID:   additionalID(obj),
		Text: getString(obj, textField),
		Kind: interfaces.MemoryKind(getString(obj, PropKind)),
		Scope: interfaces.MemoryScope{
			UserID:   getString(obj, PropUserID),
			TenantID: getString(obj, PropTenantID),
			AgentID:  getString(obj, PropAgentID),
			Tags:     decodeScopeTags(obj[PropScopeTags]),
		},
		ExpiresAt: getTime(obj, PropExpiresAt),
		CreatedAt: getTime(obj, PropCreatedAt),
		UpdatedAt: getTime(obj, PropUpdatedAt),
	}
	if raw := getString(obj, PropMetadata); raw != "" {
		if err := json.Unmarshal([]byte(raw), &entry.Metadata); err != nil {
			return interfaces.MemoryEntry{}, fmt.Errorf("unmarshal metadata: %w", err)
		}
	}
	if additional, ok := obj["_additional"].(map[string]interface{}); ok {
		if certainty, ok := additional["certainty"].(float64); ok {
			entry.Score = float32(certainty)
		}
	}
	return entry, nil
}

func scopeWhere(scope interfaces.MemoryScope) *filters.WhereBuilder {
	meta := memory.ScopeMetadata(scope)
	if len(meta) == 0 {
		return nil
	}

	operands := make([]*filters.WhereBuilder, 0, len(meta))
	for key, value := range meta {
		switch key {
		case memory.ScopeKeyUserID, memory.ScopeKeyTenantID, memory.ScopeKeyAgentID:
			operands = append(operands, filters.Where().
				WithPath([]string{key}).
				WithOperator(filters.Equal).
				WithValueString(value))
		default:
			operands = append(operands, filters.Where().
				WithPath([]string{PropScopeTags}).
				WithOperator(filters.ContainsAll).
				WithValueText(scopeTagToken(key, value)))
		}
	}
	return combineWhereOperands(operands)
}

func kindsWhere(kinds []interfaces.MemoryKind) *filters.WhereBuilder {
	if len(kinds) == 0 {
		return nil
	}
	if len(kinds) == 1 {
		return filters.Where().
			WithPath([]string{PropKind}).
			WithOperator(filters.Equal).
			WithValueString(string(kinds[0]))
	}
	values := make([]string, len(kinds))
	for i, kind := range kinds {
		values[i] = string(kind)
	}
	return filters.Where().
		WithPath([]string{PropKind}).
		WithOperator(filters.ContainsAny).
		WithValueText(values...)
}

func combineWhere(parts ...*filters.WhereBuilder) *filters.WhereBuilder {
	operands := make([]*filters.WhereBuilder, 0, len(parts))
	for _, part := range parts {
		if part != nil {
			operands = append(operands, part)
		}
	}
	return combineWhereOperands(operands)
}

func combineWhereOperands(operands []*filters.WhereBuilder) *filters.WhereBuilder {
	if len(operands) == 0 {
		return nil
	}
	if len(operands) == 1 {
		return operands[0]
	}
	return filters.Where().WithOperator(filters.And).WithOperands(operands)
}

func encodeScopeTags(meta map[string]string) []string {
	if len(meta) == 0 {
		return nil
	}
	tags := make([]string, 0, len(meta))
	for key, value := range meta {
		switch key {
		case memory.ScopeKeyUserID, memory.ScopeKeyTenantID, memory.ScopeKeyAgentID:
			continue
		default:
			tags = append(tags, scopeTagToken(key, value))
		}
	}
	return tags
}

func graphqlErrors(result *models.GraphQLResponse) error {
	if result == nil || len(result.Errors) == 0 {
		return nil
	}
	msgs := make([]string, len(result.Errors))
	for i, e := range result.Errors {
		if e != nil && e.Message != "" {
			msgs[i] = e.Message
		}
	}
	return fmt.Errorf("graphql: %s", strings.Join(msgs, "; "))
}

func decodeScopeTags(raw any) map[string]string {
	values, ok := raw.([]interface{})
	if !ok || len(values) == 0 {
		return nil
	}
	out := make(map[string]string, len(values))
	for _, item := range values {
		token, ok := item.(string)
		if !ok {
			continue
		}
		key, value, ok := strings.Cut(token, "=")
		if !ok || key == "" {
			continue
		}
		out[key] = value
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func scopeTagToken(key, value string) string {
	return key + "=" + value
}

func objectID(wrapper *data.ObjectWrapper) string {
	if wrapper == nil || wrapper.Object == nil || wrapper.Object.ID == "" {
		return ""
	}
	return wrapper.Object.ID.String()
}

func isNotFound(err error) bool {
	var wcErr *fault.WeaviateClientError
	if errors.As(err, &wcErr) {
		return wcErr.StatusCode == 404
	}
	return false
}

func getString(obj map[string]interface{}, key string) string {
	if v, ok := obj[key].(string); ok {
		return v
	}
	return ""
}

func getTime(obj map[string]interface{}, key string) time.Time {
	raw := getString(obj, key)
	if raw == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, raw)
	if err != nil {
		t, err = time.Parse(time.RFC3339, raw)
		if err != nil {
			return time.Time{}
		}
	}
	return t.UTC()
}

func additionalID(obj map[string]interface{}) string {
	additional, ok := obj["_additional"].(map[string]interface{})
	if !ok {
		return ""
	}
	if id, ok := additional["id"].(string); ok {
		return id
	}
	return ""
}
