package pgvector

import (
	"context"
	"errors"
	"testing"

	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
)

// ---------------------------------------------------------------------------
// Stubs
// ---------------------------------------------------------------------------

// noopLogger discards all log output.
type noopLogger struct{}

func (noopLogger) Debug(_ context.Context, _ string, _ ...any) {}
func (noopLogger) Info(_ context.Context, _ string, _ ...any)  {}
func (noopLogger) Warn(_ context.Context, _ string, _ ...any)  {}
func (noopLogger) Error(_ context.Context, _ string, _ ...any) {}

var _ logger.Logger = noopLogger{}

// stubRows drives scanRows without a real database connection.
type stubRows struct {
	data    []rowData
	pos     int
	scanErr error
	iterErr error
}

type rowData struct {
	content string
	source  string
	score   float64
}

func (r *stubRows) Close() {}
func (r *stubRows) Next() bool {
	r.pos++
	return r.pos <= len(r.data)
}
func (r *stubRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	row := r.data[r.pos-1]
	*dest[0].(*string) = row.content
	*dest[1].(*string) = row.source
	*dest[2].(*float64) = row.score
	return nil
}
func (r *stubRows) Err() error { return r.iterErr }

// stubQuerier returns a pre-canned pgRows (or error) for any Query call.
type stubQuerier struct {
	rows pgRows
	err  error
}

func (s *stubQuerier) Query(_ context.Context, _ string, _ ...any) (pgRows, error) {
	return s.rows, s.err
}

// stubEmbed is a fixed-vector embedding function for tests.
func stubEmbed(_ context.Context, _ string) ([]float32, error) {
	return []float32{0.1, 0.2, 0.3}, nil
}

func errEmbed(_ context.Context, _ string) ([]float32, error) {
	return nil, errors.New("embed error")
}

// newTestRetriever builds a PgvectorRetriever with a stub querier, bypassing the DSN/pool
// validation path so tests do not need a real database. Options are applied after defaults.
func newTestRetriever(t *testing.T, q pgQuerier, opts ...Option) *PgvectorRetriever {
	t.Helper()
	r := &PgvectorRetriever{
		name:         "test-kb",
		embed:        stubEmbed,
		db:           q,
		table:        "items",
		contentCol:   "content",
		sourceCol:    "source",
		embeddingCol: "embedding",
		topK:         5,
		minScore:     0.75,
		logger:       noopLogger{},
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// ---------------------------------------------------------------------------
// Constructor tests
// ---------------------------------------------------------------------------

func TestNewRetriever_MissingName(t *testing.T) {
	_, err := NewRetriever("", stubEmbed, WithTable("items"))
	if err == nil || !contains(err.Error(), "name") {
		t.Fatalf("expected name error, got %v", err)
	}
}

func TestNewRetriever_WhitespaceName(t *testing.T) {
	_, err := NewRetriever("   ", stubEmbed, WithTable("items"))
	if err == nil {
		t.Fatal("expected error for whitespace name")
	}
}

func TestNewRetriever_MissingEmbed(t *testing.T) {
	_, err := NewRetriever("kb", nil, WithTable("items"))
	if err == nil || !contains(err.Error(), "embed") {
		t.Fatalf("expected embed error, got %v", err)
	}
}

func TestNewRetriever_MissingTable(t *testing.T) {
	_, err := NewRetriever("kb", stubEmbed)
	if err == nil || !contains(err.Error(), "table") {
		t.Fatalf("expected table error, got %v", err)
	}
}

func TestNewRetriever_MissingDSNAndPool(t *testing.T) {
	_, err := NewRetriever("kb", stubEmbed, WithTable("items"), WithLogger(noopLogger{}))
	if err == nil || !contains(err.Error(), "DSN") {
		t.Fatalf("expected DSN error, got %v", err)
	}
}

func TestNewRetriever_InvalidDSN(t *testing.T) {
	_, err := NewRetriever("kb", stubEmbed,
		WithTable("items"),
		WithDSN("not-a-valid-dsn"),
		WithLogger(noopLogger{}),
	)
	if err == nil {
		t.Fatal("expected parse DSN error")
	}
}

func TestNewRetriever_Defaults(t *testing.T) {
	r := newTestRetriever(t, &stubQuerier{})
	if r.contentCol != "content" {
		t.Errorf("contentCol = %q, want %q", r.contentCol, "content")
	}
	if r.sourceCol != "source" {
		t.Errorf("sourceCol = %q, want %q", r.sourceCol, "source")
	}
	if r.embeddingCol != "embedding" {
		t.Errorf("embeddingCol = %q, want %q", r.embeddingCol, "embedding")
	}
	if r.topK != 5 {
		t.Errorf("topK = %d, want 5", r.topK)
	}
	if r.minScore != types.DefaultMinScore {
		t.Errorf("minScore = %f, want %f", r.minScore, types.DefaultMinScore)
	}
}

func TestNewRetriever_NameTrimmed(t *testing.T) {
	r := newTestRetriever(t, &stubQuerier{})
	r2 := newTestRetriever(t, &stubQuerier{}, WithTopK(3))
	_ = r2
	// The retriever in newTestRetriever uses name "test-kb" (already trimmed)
	if r.name != "test-kb" {
		t.Errorf("name = %q, want %q", r.name, "test-kb")
	}
}

func TestNewRetriever_CustomOptions(t *testing.T) {
	r := newTestRetriever(t, &stubQuerier{},
		WithContentCol("body"),
		WithSourceCol("url"),
		WithEmbeddingCol("vec"),
		WithTopK(10),
		WithMinScore(0.9),
	)
	if r.contentCol != "body" {
		t.Errorf("contentCol = %q", r.contentCol)
	}
	if r.sourceCol != "url" {
		t.Errorf("sourceCol = %q", r.sourceCol)
	}
	if r.embeddingCol != "vec" {
		t.Errorf("embeddingCol = %q", r.embeddingCol)
	}
	if r.topK != 10 {
		t.Errorf("topK = %d", r.topK)
	}
	if r.minScore != 0.9 {
		t.Errorf("minScore = %f", r.minScore)
	}
}

func TestNewRetriever_WithLogger(t *testing.T) {
	r := newTestRetriever(t, &stubQuerier{}, WithLogger(noopLogger{}))
	if _, ok := r.logger.(noopLogger); !ok {
		t.Errorf("logger type = %T, want noopLogger", r.logger)
	}
}

// ---------------------------------------------------------------------------
// Name
// ---------------------------------------------------------------------------

func TestPgvectorRetriever_Name(t *testing.T) {
	r := newTestRetriever(t, &stubQuerier{})
	if r.Name() != "test-kb" {
		t.Errorf("Name() = %q", r.Name())
	}
}

// ---------------------------------------------------------------------------
// Search tests
// ---------------------------------------------------------------------------

func TestSearch_ReturnsDocs(t *testing.T) {
	q := &stubQuerier{
		rows: &stubRows{data: []rowData{
			{content: "Go routines are lightweight", source: "go.dev", score: 0.95},
			{content: "Channels enable communication", source: "go.dev", score: 0.88},
		}},
	}
	r := newTestRetriever(t, q)

	docs, err := r.Search(context.Background(), "concurrency in Go")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("docs len = %d, want 2", len(docs))
	}
	if docs[0].Content != "Go routines are lightweight" {
		t.Errorf("docs[0].Content = %q", docs[0].Content)
	}
	if docs[0].Source != "go.dev" {
		t.Errorf("docs[0].Source = %q", docs[0].Source)
	}
	if docs[0].Score != 0.95 {
		t.Errorf("docs[0].Score = %f", docs[0].Score)
	}
	if docs[1].Content != "Channels enable communication" {
		t.Errorf("docs[1].Content = %q", docs[1].Content)
	}
}

func TestSearch_EmptyResult(t *testing.T) {
	q := &stubQuerier{rows: &stubRows{}}
	r := newTestRetriever(t, q)

	docs, err := r.Search(context.Background(), "nothing matches")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected 0 docs, got %d", len(docs))
	}
}

func TestSearch_EmbedError(t *testing.T) {
	r := newTestRetriever(t, &stubQuerier{})
	r.embed = errEmbed

	_, err := r.Search(context.Background(), "query")
	if err == nil || !contains(err.Error(), "embed") {
		t.Fatalf("expected embed error, got %v", err)
	}
}

func TestSearch_QueryError(t *testing.T) {
	q := &stubQuerier{err: errors.New("connection refused")}
	r := newTestRetriever(t, q)

	_, err := r.Search(context.Background(), "query")
	if err == nil || !contains(err.Error(), "pgvector query") {
		t.Fatalf("expected query error, got %v", err)
	}
}

func TestSearch_ScanError(t *testing.T) {
	q := &stubQuerier{
		rows: &stubRows{
			data:    []rowData{{content: "x", source: "y", score: 0.9}},
			scanErr: errors.New("scan failed"),
		},
	}
	r := newTestRetriever(t, q)

	_, err := r.Search(context.Background(), "query")
	if err == nil || !contains(err.Error(), "scan") {
		t.Fatalf("expected scan error, got %v", err)
	}
}

func TestSearch_IterError(t *testing.T) {
	q := &stubQuerier{
		rows: &stubRows{iterErr: errors.New("cursor error")},
	}
	r := newTestRetriever(t, q)

	_, err := r.Search(context.Background(), "query")
	if err == nil || !contains(err.Error(), "cursor") {
		t.Fatalf("expected iter error, got %v", err)
	}
}

func TestSearch_PassesTopKAndMinScore(t *testing.T) {
	var gotSQL string
	var gotArgs []any

	capturingQ := &capturingQuerier{rows: &stubRows{}}
	r := newTestRetriever(t, capturingQ, WithTopK(3), WithMinScore(0.8))

	_, err := r.Search(context.Background(), "q")
	if err != nil {
		t.Fatal(err)
	}
	gotSQL = capturingQ.lastSQL
	gotArgs = capturingQ.lastArgs

	if !contains(gotSQL, "LIMIT $3") {
		t.Errorf("SQL missing LIMIT: %s", gotSQL)
	}
	// args: $1=vector, $2=minScore, $3=topK
	if len(gotArgs) != 3 {
		t.Fatalf("args len = %d, want 3", len(gotArgs))
	}
	if gotArgs[1] != 0.8 {
		t.Errorf("minScore arg = %v, want 0.8", gotArgs[1])
	}
	if gotArgs[2] != 3 {
		t.Errorf("topK arg = %v, want 3", gotArgs[2])
	}
}

// ---------------------------------------------------------------------------
// scanRows tests
// ---------------------------------------------------------------------------

func TestScanRows_SingleDoc(t *testing.T) {
	rows := &stubRows{data: []rowData{
		{content: "hello", source: "src.md", score: 0.91},
	}}
	docs, err := scanRows(rows)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("len = %d", len(docs))
	}
	if docs[0].Content != "hello" || docs[0].Source != "src.md" || docs[0].Score != 0.91 {
		t.Errorf("got %+v", docs[0])
	}
}

func TestScanRows_Empty(t *testing.T) {
	docs, err := scanRows(&stubRows{})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 0 {
		t.Fatalf("expected empty, got %d docs", len(docs))
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) &&
		(s == sub || len(s) > 0 && containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// capturingQuerier records the last SQL and args for assertion.
type capturingQuerier struct {
	rows     pgRows
	lastSQL  string
	lastArgs []any
}

func (c *capturingQuerier) Query(_ context.Context, sql string, args ...any) (pgRows, error) {
	c.lastSQL = sql
	c.lastArgs = args
	return c.rows, nil
}
