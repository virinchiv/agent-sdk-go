// Package pgvector provides a retriever backed by PostgreSQL with the pgvector extension.
// Callers provide a plain-text query; the retriever converts it to an embedding via [EmbedFunc]
// and runs a cosine-similarity nearest-neighbour search against the configured table.
package pgvector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	pgvec "github.com/pgvector/pgvector-go"
	pgxvec "github.com/pgvector/pgvector-go/pgx"
)

var _ interfaces.Retriever = (*PgvectorRetriever)(nil)

// EmbedFunc converts a plain-text query into a vector embedding.
// Callers typically wrap an LLM embedding API (e.g. OpenAI text-embedding-3-small).
type EmbedFunc func(ctx context.Context, text string) ([]float32, error)

// pgRows is the subset of [pgx.Rows] used by Search, allowing injection in tests.
type pgRows interface {
	Close()
	Next() bool
	Scan(dest ...any) error
	Err() error
}

// pgQuerier abstracts the database query call; satisfied by [pgxPoolQuerier] and test stubs.
type pgQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgRows, error)
}

// pgxPoolQuerier wraps [pgxpool.Pool] to satisfy [pgQuerier].
type pgxPoolQuerier struct{ pool *pgxpool.Pool }

func (q *pgxPoolQuerier) Query(ctx context.Context, sql string, args ...any) (pgRows, error) {
	return q.pool.Query(ctx, sql, args...)
}

// PgvectorRetriever searches a PostgreSQL table with the pgvector extension using cosine similarity.
// The query text is converted to an embedding via [EmbedFunc] before each search.
type PgvectorRetriever struct {
	// name is the stable identifier returned by [Name]; required.
	name string

	// runtime fields — used by Search.
	db           pgQuerier
	table        string
	contentCol   string
	sourceCol    string
	embeddingCol string
	topK         int
	minScore     float64
	embed        EmbedFunc
	logger       logger.Logger

	// build-time fields — consumed by NewRetriever; not used after construction.
	dsn      string
	logLevel string
}

// Option configures PgvectorRetriever.
type Option func(*PgvectorRetriever)

// WithPool sets an existing [pgxpool.Pool]. When provided, [WithDSN] is ignored.
// Callers must register pgvector types on the pool:
//
//	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
//	    return pgxvec.RegisterTypes(ctx, conn)
//	}
func WithPool(pool *pgxpool.Pool) Option {
	return func(r *PgvectorRetriever) { r.db = &pgxPoolQuerier{pool: pool} }
}

// WithDSN sets the PostgreSQL connection string used to create a new pool when [WithPool] is omitted.
// The pool is configured to register pgvector types on each new connection automatically.
func WithDSN(dsn string) Option {
	return func(r *PgvectorRetriever) { r.dsn = dsn }
}

// WithTable sets the PostgreSQL table (or view) to search. Required.
func WithTable(table string) Option {
	return func(r *PgvectorRetriever) { r.table = table }
}

// WithContentCol sets the column that holds document text. Defaults to [types.DefaultContentField].
func WithContentCol(col string) Option {
	return func(r *PgvectorRetriever) { r.contentCol = col }
}

// WithSourceCol sets the column that holds the document source identifier. Defaults to [types.DefaultSourceField].
func WithSourceCol(col string) Option {
	return func(r *PgvectorRetriever) { r.sourceCol = col }
}

// WithEmbeddingCol sets the column that holds the pgvector embedding. Defaults to "embedding".
func WithEmbeddingCol(col string) Option {
	return func(r *PgvectorRetriever) { r.embeddingCol = col }
}

// WithTopK sets the maximum number of documents returned per search. Defaults to [types.DefaultTopK].
func WithTopK(topK int) Option {
	return func(r *PgvectorRetriever) { r.topK = topK }
}

// WithMinScore sets the minimum cosine similarity (0–1) for returned documents. Defaults to [types.DefaultMinScore].
func WithMinScore(minScore float64) Option {
	return func(r *PgvectorRetriever) { r.minScore = minScore }
}

// WithLogger sets the logger. When omitted, a default logger at the configured log level is used.
func WithLogger(l logger.Logger) Option {
	return func(r *PgvectorRetriever) { r.logger = l }
}

// WithLogLevel sets the default log level when [WithLogger] is omitted. Defaults to "error".
func WithLogLevel(level string) Option {
	return func(r *PgvectorRetriever) { r.logLevel = level }
}

// NewRetriever builds a PgvectorRetriever. name must be non-empty and unique across all retrievers
// registered with the same agent. embed is required. [WithTable] is required. When [WithPool] is
// omitted, [WithDSN] must be provided. Zero-valued topK and minScore default to [types.DefaultTopK] and
// [types.DefaultMinScore].
func NewRetriever(name string, embed EmbedFunc, opts ...Option) (*PgvectorRetriever, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("name is required and must be non-empty")
	}
	if embed == nil {
		return nil, errors.New("embed func is required")
	}
	r := &PgvectorRetriever{name: strings.TrimSpace(name), embed: embed}
	for _, opt := range opts {
		opt(r)
	}
	if r.table == "" {
		return nil, errors.New("table is required; use WithTable")
	}
	if r.contentCol == "" {
		r.contentCol = types.DefaultContentField
	}
	if r.sourceCol == "" {
		r.sourceCol = types.DefaultSourceField
	}
	if r.embeddingCol == "" {
		r.embeddingCol = "embedding"
	}
	if r.topK == 0 {
		r.topK = types.DefaultTopK
	}
	if r.minScore == 0 {
		r.minScore = types.DefaultMinScore
	}
	if r.logLevel == "" {
		r.logLevel = "error"
	}
	if r.logger == nil {
		r.logger = logger.DefaultLogger(r.logLevel)
	}
	if r.db == nil {
		if r.dsn == "" {
			return nil, errors.New("DSN is required when not using WithPool; use WithDSN or WithPool")
		}
		cfg, err := pgxpool.ParseConfig(r.dsn)
		if err != nil {
			return nil, fmt.Errorf("parse DSN: %w", err)
		}
		// Register pgvector types on every new connection so the <=> operator and
		// pgvec.NewVector arguments are correctly encoded and decoded.
		cfg.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
			return pgxvec.RegisterTypes(ctx, conn)
		}
		pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
		if err != nil {
			return nil, fmt.Errorf("create pgx pool: %w", err)
		}
		r.db = &pgxPoolQuerier{pool: pool}
	}
	r.logger.Info(context.Background(), "pgvector retriever built",
		slog.String("scope", "pgvector"),
		slog.String("name", r.name),
		slog.String("table", r.table),
		slog.String("contentCol", r.contentCol),
		slog.String("sourceCol", r.sourceCol),
		slog.String("embeddingCol", r.embeddingCol),
		slog.Int("topK", r.topK),
		slog.Float64("minScore", r.minScore),
	)
	return r, nil
}

// Name implements [interfaces.Retriever].
func (r *PgvectorRetriever) Name() string {
	return r.name
}

// Search embeds the query and runs a cosine-similarity nearest-neighbour search against the
// configured PostgreSQL table. Returns at most [WithTopK] documents with similarity ≥ [WithMinScore].
//
// Table and column names are developer-controlled build-time configuration and are not
// sanitised against SQL injection because they are never derived from runtime user input.
func (r *PgvectorRetriever) Search(ctx context.Context, query string) ([]interfaces.Document, error) {
	r.logger.Debug(ctx, "pgvector search start",
		slog.String("scope", "pgvector"),
		slog.String("name", r.name),
		slog.String("table", r.table),
		slog.String("query", query),
		slog.Int("topK", r.topK),
		slog.Float64("minScore", r.minScore),
	)
	start := time.Now()

	vec, err := r.embed(ctx, query)
	if err != nil {
		r.logger.Error(ctx, "pgvector embed failed",
			slog.String("scope", "pgvector"),
			slog.String("name", r.name),
			slog.Duration("elapsed", time.Since(start)),
			slog.Any("error", err),
		)
		return nil, fmt.Errorf("embed query: %w", err)
	}

	// Cosine distance operator (<=>): 0 = identical, 2 = opposite.
	// Score = 1 − cosine_distance, giving cosine similarity in [−1, 1] (typically [0, 1] for text).
	// Table/column identifiers are build-time developer config, not runtime user input.
	//nolint:gosec
	sql := fmt.Sprintf(
		`SELECT %s, %s, 1 - (%s <=> $1) AS score
		 FROM %s
		 WHERE 1 - (%s <=> $1) >= $2
		 ORDER BY %s <=> $1
		 LIMIT $3`,
		r.contentCol, r.sourceCol, r.embeddingCol,
		r.table,
		r.embeddingCol,
		r.embeddingCol,
	)

	rows, err := r.db.Query(ctx, sql, pgvec.NewVector(vec), r.minScore, r.topK)
	if err != nil {
		r.logger.Error(ctx, "pgvector search failed",
			slog.String("scope", "pgvector"),
			slog.String("name", r.name),
			slog.String("table", r.table),
			slog.Duration("elapsed", time.Since(start)),
			slog.Any("error", err),
		)
		return nil, fmt.Errorf("pgvector query: %w", err)
	}

	docs, err := scanRows(rows)
	if err != nil {
		r.logger.Error(ctx, "pgvector scan failed",
			slog.String("scope", "pgvector"),
			slog.String("name", r.name),
			slog.String("table", r.table),
			slog.Duration("elapsed", time.Since(start)),
			slog.Any("error", err),
		)
		return nil, err
	}

	r.logger.Debug(ctx, "pgvector search done",
		slog.String("scope", "pgvector"),
		slog.String("name", r.name),
		slog.String("table", r.table),
		slog.Int("docs", len(docs)),
		slog.Duration("elapsed", time.Since(start)),
	)
	return docs, nil
}

// scanRows reads content, source, and score from each row into []interfaces.Document.
func scanRows(rows pgRows) ([]interfaces.Document, error) {
	defer rows.Close()
	var docs []interfaces.Document
	for rows.Next() {
		var doc interfaces.Document
		if err := rows.Scan(&doc.Content, &doc.Source, &doc.Score); err != nil {
			return nil, fmt.Errorf("scan row: %w", err)
		}
		docs = append(docs, doc)
	}
	return docs, rows.Err()
}
