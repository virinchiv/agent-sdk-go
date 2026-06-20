// Package pgvector provides a [interfaces.Memory] implementation backed by PostgreSQL with pgvector.
//
// Expected table schema (embedding dimension must match [EmbedFunc] output):
//
//	CREATE TABLE agent_memories (
//	  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
//	  text TEXT NOT NULL,
//	  kind TEXT NOT NULL DEFAULT '',
//	  user_id TEXT,
//	  tenant_id TEXT,
//	  agent_id TEXT,
//	  scope_tags TEXT[] NOT NULL DEFAULT '{}',
//	  metadata JSONB NOT NULL DEFAULT '{}',
//	  expires_at TIMESTAMPTZ,
//	  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
//	  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
//	  embedding vector(1536)
//	);
//
// Callers provide [EmbedFunc] to vectorize memory text on store and recall queries on load.
package pgvector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
	"github.com/agenticenv/agent-sdk-go/pkg/memory"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvec "github.com/pgvector/pgvector-go"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
)

var _ interfaces.Memory = (*Memory)(nil)

// EmbedFunc converts plain text into a vector embedding.
type EmbedFunc func(ctx context.Context, text string) ([]float32, error)

// pgRows is the subset of [pgx.Rows] used by Load, allowing injection in tests.
type pgRows interface {
	Close()
	Next() bool
	Scan(dest ...any) error
	Err() error
}

// pgDB abstracts database calls; satisfied by [pgxPoolDB] and test stubs.
type pgDB interface {
	Query(ctx context.Context, sql string, args ...any) (pgRows, error)
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

type pgxPoolDB struct{ pool *pgxpool.Pool }

func (d *pgxPoolDB) Query(ctx context.Context, sql string, args ...any) (pgRows, error) {
	return d.pool.Query(ctx, sql, args...)
}

func (d *pgxPoolDB) Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	return d.pool.Exec(ctx, sql, args...)
}

// Memory stores and recalls agent memories in PostgreSQL with pgvector.
type Memory struct {
	db           pgDB
	table        string
	textCol      string
	embeddingCol string
	embed        EmbedFunc

	defaultLimit    int
	defaultMinScore float32

	logger   logger.Logger
	dsn      string
	logLevel string
}

// Option configures [Memory].
type Option func(*Memory)

// WithPool sets an existing [pgxpool.Pool]. When provided, [WithDSN] is ignored.
func WithPool(pool *pgxpool.Pool) Option {
	return func(m *Memory) { m.db = &pgxPoolDB{pool: pool} }
}

// WithDSN sets the PostgreSQL connection string used to create a pool when [WithPool] is omitted.
func WithDSN(dsn string) Option {
	return func(m *Memory) { m.dsn = dsn }
}

// WithTable sets the PostgreSQL table. Defaults to [DefaultTable].
func WithTable(table string) Option {
	return func(m *Memory) { m.table = table }
}

// WithTextCol sets the column that holds memory text. Defaults to [DefaultTextCol].
func WithTextCol(col string) Option {
	return func(m *Memory) { m.textCol = col }
}

// WithEmbeddingCol sets the column that holds the pgvector embedding. Defaults to [DefaultEmbeddingCol].
func WithEmbeddingCol(col string) Option {
	return func(m *Memory) { m.embeddingCol = col }
}

// WithDefaultLimit sets the load limit when callers omit [interfaces.WithLoadLimit].
func WithDefaultLimit(limit int) Option {
	return func(m *Memory) { m.defaultLimit = limit }
}

// WithDefaultMinScore sets the cosine similarity floor when callers omit [interfaces.WithMinScore].
func WithDefaultMinScore(minScore float32) Option {
	return func(m *Memory) { m.defaultMinScore = minScore }
}

// WithLogger sets the logger.
func WithLogger(l logger.Logger) Option {
	return func(m *Memory) { m.logger = l }
}

// WithLogLevel sets the log level when no logger is provided.
func WithLogLevel(level string) Option {
	return func(m *Memory) { m.logLevel = level }
}

// NewMemory builds a pgvector-backed [interfaces.Memory]. embed is required.
// When [WithPool] is omitted, [WithDSN] must be provided.
func NewMemory(embed EmbedFunc, opts ...Option) (*Memory, error) {
	if embed == nil {
		return nil, errors.New("embed func is required")
	}
	m := &Memory{embed: embed}
	for _, opt := range opts {
		opt(m)
	}
	if m.table == "" {
		m.table = DefaultTable
	}
	if m.textCol == "" {
		m.textCol = DefaultTextCol
	}
	if m.embeddingCol == "" {
		m.embeddingCol = DefaultEmbeddingCol
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
	if m.db == nil {
		if m.dsn == "" {
			return nil, errors.New("DSN is required when not using WithPool; use WithDSN or WithPool")
		}
		cfg, err := pgxpool.ParseConfig(m.dsn)
		if err != nil {
			return nil, fmt.Errorf("parse DSN: %w", err)
		}
		cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
			return pgxvec.RegisterTypes(ctx, conn)
		}
		pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
		if err != nil {
			return nil, fmt.Errorf("create pgx pool: %w", err)
		}
		m.db = &pgxPoolDB{pool: pool}
	}
	m.logger.Info(context.Background(), "pgvector memory built",
		slog.String("scope", "pgvector-memory"),
		slog.String("table", m.table),
		slog.String("textCol", m.textCol),
		slog.String("embeddingCol", m.embeddingCol),
		slog.Int("defaultLimit", m.defaultLimit),
		slog.Float64("defaultMinScore", float64(m.defaultMinScore)),
	)
	return m, nil
}

// Store persists a memory in scope and returns its ID.
func (m *Memory) Store(ctx context.Context, scope interfaces.MemoryScope, record interfaces.MemoryRecord, opts ...interfaces.StoreMemoryOption) (string, error) {
	if m.db == nil {
		return "", errors.New("database is not set")
	}

	storeOpts := interfaces.StoreMemoryOptions{}
	for _, opt := range opts {
		opt(&storeOpts)
	}

	vec, err := m.embed(ctx, record.Text)
	if err != nil {
		return "", fmt.Errorf("embed memory text: %w", err)
	}

	now := time.Now().UTC()
	meta := memory.ScopeMetadata(scope)
	scopeTags := encodeScopeTags(meta)

	metadataJSON, err := marshalMetadata(record.Metadata)
	if err != nil {
		return "", err
	}

	id := strings.TrimSpace(storeOpts.ID)
	if id == "" {
		id = uuid.NewString()
	}

	userID := meta[memory.ScopeKeyUserID]
	tenantID := meta[memory.ScopeKeyTenantID]
	agentID := meta[memory.ScopeKeyAgentID]

	expiresArg := any(nil)
	if !record.ExpiresAt.IsZero() {
		expiresArg = record.ExpiresAt.UTC()
	}

	//nolint:gosec // table/column identifiers are build-time developer config.
	sql := fmt.Sprintf(`
		INSERT INTO %s (
			%s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12
		)
		ON CONFLICT (%s) DO UPDATE SET
			%s = EXCLUDED.%s,
			%s = EXCLUDED.%s,
			%s = EXCLUDED.%s,
			%s = EXCLUDED.%s,
			%s = EXCLUDED.%s,
			%s = EXCLUDED.%s,
			%s = EXCLUDED.%s,
			%s = EXCLUDED.%s,
			%s = EXCLUDED.%s,
			%s = %s.%s,
			%s = EXCLUDED.%s
		RETURNING %s`,
		m.table,
		ColID, m.textCol, ColKind, ColUserID, ColTenantID, ColAgentID, ColScopeTags, ColMetadata,
		ColExpiresAt, ColCreatedAt, ColUpdatedAt, m.embeddingCol,
		ColID,
		m.textCol, m.textCol,
		ColKind, ColKind,
		ColUserID, ColUserID,
		ColTenantID, ColTenantID,
		ColAgentID, ColAgentID,
		ColScopeTags, ColScopeTags,
		ColMetadata, ColMetadata,
		ColExpiresAt, ColExpiresAt,
		ColUpdatedAt, ColUpdatedAt,
		ColCreatedAt, m.table, ColCreatedAt,
		m.embeddingCol, m.embeddingCol,
		ColID,
	)

	args := []any{
		id,
		record.Text,
		string(record.Kind),
		nullIfEmpty(userID),
		nullIfEmpty(tenantID),
		nullIfEmpty(agentID),
		scopeTags,
		metadataJSON,
		expiresArg,
		now,
		now,
		pgvec.NewVector(vec),
	}

	rows, err := m.db.Query(ctx, sql, args...)
	if err != nil {
		return "", fmt.Errorf("pgvector store memory: %w", err)
	}
	defer rows.Close()

	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", fmt.Errorf("pgvector store memory: %w", err)
		}
		return "", errors.New("pgvector store memory: no id returned")
	}
	var returnedID string
	if err := rows.Scan(&returnedID); err != nil {
		return "", fmt.Errorf("pgvector store memory scan id: %w", err)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("pgvector store memory: %w", err)
	}
	return returnedID, nil
}

// Load recalls memories within scope. Non-empty query uses vector similarity; empty query lists by updated_at.
func (m *Memory) Load(ctx context.Context, scope interfaces.MemoryScope, query string, opts ...interfaces.LoadMemoryOption) ([]interfaces.MemoryEntry, error) {
	if m.db == nil {
		return nil, errors.New("database is not set")
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

	query = strings.TrimSpace(query)
	if query != "" {
		vec, err := m.embed(ctx, query)
		if err != nil {
			return nil, fmt.Errorf("embed recall query: %w", err)
		}

		whereSQL, scopeArgs := buildScopeArgs(scope, loadOpts.Kinds, 4)
		whereSQL = appendNotExpired(whereSQL)
		args := append([]any{pgvec.NewVector(vec), float64(minScore), limit}, scopeArgs...)
		scoreExpr := fmt.Sprintf("1 - (%s <=> $1)", m.embeddingCol)
		//nolint:gosec
		sql := fmt.Sprintf(`
			SELECT %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s AS score
			FROM %s
			WHERE %s AND %s >= $2
			ORDER BY %s <=> $1
			LIMIT $3`,
			ColID, m.textCol, ColKind, ColUserID, ColTenantID, ColAgentID, ColScopeTags, ColMetadata,
			ColExpiresAt, ColCreatedAt, ColUpdatedAt, scoreExpr,
			m.table,
			whereSQL, scoreExpr,
			m.embeddingCol,
		)
		rows, err := m.db.Query(ctx, sql, args...)
		if err != nil {
			return nil, fmt.Errorf("pgvector load memories: %w", err)
		}
		return scanMemoryRows(rows)
	}

	whereSQL, args := buildScopeArgs(scope, loadOpts.Kinds, 1)
	whereSQL = appendNotExpired(whereSQL)
	args = append(args, limit)
	limitArg := len(args)
	//nolint:gosec
	sql := fmt.Sprintf(`
		SELECT %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, %s, 0::float8 AS score
		FROM %s
		WHERE %s
		ORDER BY %s DESC
		LIMIT $%d`,
		ColID, m.textCol, ColKind, ColUserID, ColTenantID, ColAgentID, ColScopeTags, ColMetadata,
		ColExpiresAt, ColCreatedAt, ColUpdatedAt,
		m.table,
		whereSQL,
		ColUpdatedAt,
		limitArg,
	)
	rows, err := m.db.Query(ctx, sql, args...)
	if err != nil {
		return nil, fmt.Errorf("pgvector load memories: %w", err)
	}
	return scanMemoryRows(rows)
}

// Clear removes all memories matching scope.
func (m *Memory) Clear(ctx context.Context, scope interfaces.MemoryScope) error {
	if m.db == nil {
		return errors.New("database is not set")
	}

	meta := memory.ScopeMetadata(scope)
	if len(meta) == 0 {
		return errors.New("scope must include at least one non-empty field")
	}

	whereSQL, args := buildScopeArgs(scope, nil, 1)

	//nolint:gosec
	sql := fmt.Sprintf("DELETE FROM %s WHERE %s", m.table, whereSQL)
	if _, err := m.db.Exec(ctx, sql, args...); err != nil {
		return fmt.Errorf("pgvector clear memories: %w", err)
	}
	return nil
}

func buildScopeArgs(scope interfaces.MemoryScope, kinds []interfaces.MemoryKind, startArg int) (string, []any) {
	meta := memory.ScopeMetadata(scope)
	if len(meta) == 0 && len(kinds) == 0 {
		return "TRUE", nil
	}

	var parts []string
	var args []any
	nextArg := startArg

	for key, value := range meta {
		switch key {
		case memory.ScopeKeyUserID, memory.ScopeKeyTenantID, memory.ScopeKeyAgentID:
			args = append(args, value)
			parts = append(parts, fmt.Sprintf("%s = $%d", key, nextArg))
			nextArg++
		default:
			args = append(args, []string{scopeTagToken(key, value)})
			parts = append(parts, fmt.Sprintf("%s @> $%d::text[]", ColScopeTags, nextArg))
			nextArg++
		}
	}

	if len(kinds) == 1 {
		args = append(args, string(kinds[0]))
		parts = append(parts, fmt.Sprintf("%s = $%d", ColKind, nextArg))
	} else if len(kinds) > 1 {
		kindValues := make([]string, len(kinds))
		for i, kind := range kinds {
			kindValues[i] = string(kind)
		}
		args = append(args, kindValues)
		parts = append(parts, fmt.Sprintf("%s = ANY($%d::text[])", ColKind, nextArg))
	}

	if len(parts) == 0 {
		return "TRUE", args
	}
	return strings.Join(parts, " AND "), args
}

func appendNotExpired(whereSQL string) string {
	clause := fmt.Sprintf("(%s IS NULL OR %s > now())", ColExpiresAt, ColExpiresAt)
	if whereSQL == "" || whereSQL == "TRUE" {
		return clause
	}
	return whereSQL + " AND " + clause
}

func scanMemoryRows(rows pgRows) ([]interfaces.MemoryEntry, error) {
	defer rows.Close()

	var entries []interfaces.MemoryEntry
	for rows.Next() {
		var (
			entry     interfaces.MemoryEntry
			kind      string
			userID    *string
			tenantID  *string
			agentID   *string
			scopeTags []string
			metadata  []byte
			expiresAt *time.Time
			createdAt time.Time
			updatedAt time.Time
			score     float64
		)
		if err := rows.Scan(
			&entry.ID,
			&entry.Text,
			&kind,
			&userID,
			&tenantID,
			&agentID,
			&scopeTags,
			&metadata,
			&expiresAt,
			&createdAt,
			&updatedAt,
			&score,
		); err != nil {
			return nil, fmt.Errorf("scan memory row: %w", err)
		}

		entry.Kind = interfaces.MemoryKind(kind)
		entry.Scope = interfaces.MemoryScope{
			UserID:   derefString(userID),
			TenantID: derefString(tenantID),
			AgentID:  derefString(agentID),
			Tags:     decodeScopeTags(scopeTags),
		}
		if len(metadata) > 0 {
			if err := json.Unmarshal(metadata, &entry.Metadata); err != nil {
				return nil, fmt.Errorf("unmarshal metadata: %w", err)
			}
		}
		if expiresAt != nil {
			entry.ExpiresAt = expiresAt.UTC()
		}
		entry.CreatedAt = createdAt.UTC()
		entry.UpdatedAt = updatedAt.UTC()
		entry.Score = float32(score)

		if entry.Expired() {
			continue
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate memory rows: %w", err)
	}
	return entries, nil
}

func marshalMetadata(metadata map[string]string) ([]byte, error) {
	if len(metadata) == 0 {
		return []byte("{}"), nil
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return nil, fmt.Errorf("marshal metadata: %w", err)
	}
	return raw, nil
}

func encodeScopeTags(meta map[string]string) []string {
	if len(meta) == 0 {
		return []string{}
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

func decodeScopeTags(tags []string) map[string]string {
	if len(tags) == 0 {
		return nil
	}
	out := make(map[string]string, len(tags))
	for _, token := range tags {
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

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
