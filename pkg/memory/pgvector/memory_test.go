package pgvector

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
	"github.com/jackc/pgx/v5/pgconn"
)

type noopLogger struct{}

func (noopLogger) Debug(_ context.Context, _ string, _ ...any) {}
func (noopLogger) Info(_ context.Context, _ string, _ ...any)  {}
func (noopLogger) Warn(_ context.Context, _ string, _ ...any)  {}
func (noopLogger) Error(_ context.Context, _ string, _ ...any) {}

var _ logger.Logger = noopLogger{}

type stubIDRows struct {
	id      string
	scanned bool
	err     error
}

func (r *stubIDRows) Close() {}
func (r *stubIDRows) Next() bool {
	if r.scanned {
		return false
	}
	r.scanned = true
	return true
}
func (r *stubIDRows) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	*dest[0].(*string) = r.id
	return nil
}
func (r *stubIDRows) Err() error { return nil }

type memoryRowData struct {
	id        string
	text      string
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
}

type stubMemoryRows struct {
	data    []memoryRowData
	pos     int
	scanErr error
	iterErr error
}

func (r *stubMemoryRows) Close() {}
func (r *stubMemoryRows) Next() bool {
	r.pos++
	return r.pos <= len(r.data)
}
func (r *stubMemoryRows) Scan(dest ...any) error {
	if r.scanErr != nil {
		return r.scanErr
	}
	row := r.data[r.pos-1]
	*dest[0].(*string) = row.id
	*dest[1].(*string) = row.text
	*dest[2].(*string) = row.kind
	*dest[3].(**string) = row.userID
	*dest[4].(**string) = row.tenantID
	*dest[5].(**string) = row.agentID
	*dest[6].(*[]string) = row.scopeTags
	*dest[7].(*[]byte) = row.metadata
	*dest[8].(**time.Time) = row.expiresAt
	*dest[9].(*time.Time) = row.createdAt
	*dest[10].(*time.Time) = row.updatedAt
	*dest[11].(*float64) = row.score
	return nil
}
func (r *stubMemoryRows) Err() error { return r.iterErr }

type stubDB struct {
	queryRows pgRows
	queryErr  error
	execTag   pgconn.CommandTag
	execErr   error

	lastQuerySQL  string
	lastQueryArgs []any
	lastExecSQL   string
	lastExecArgs  []any
}

func (s *stubDB) Query(_ context.Context, sql string, args ...any) (pgRows, error) {
	s.lastQuerySQL = sql
	s.lastQueryArgs = args
	return s.queryRows, s.queryErr
}

func (s *stubDB) Exec(_ context.Context, sql string, args ...any) (pgconn.CommandTag, error) {
	s.lastExecSQL = sql
	s.lastExecArgs = args
	return s.execTag, s.execErr
}

func stubEmbed(_ context.Context, _ string) ([]float32, error) {
	return []float32{0.1, 0.2, 0.3}, nil
}

func errEmbed(_ context.Context, _ string) ([]float32, error) {
	return nil, errors.New("embed error")
}

func newTestMemory(t *testing.T, db pgDB, opts ...Option) *Memory {
	t.Helper()
	m := &Memory{
		embed:           stubEmbed,
		db:              db,
		table:           DefaultTable,
		textCol:         DefaultTextCol,
		embeddingCol:    DefaultEmbeddingCol,
		defaultLimit:    DefaultLoadLimit,
		defaultMinScore: DefaultMinScore,
		logger:          noopLogger{},
	}
	for _, opt := range opts {
		opt(m)
	}
	return m
}

func TestNewMemory_MissingEmbed(t *testing.T) {
	_, err := NewMemory(nil, WithTable(DefaultTable))
	if err == nil || !contains(err.Error(), "embed") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewMemory_MissingDSNAndPool(t *testing.T) {
	_, err := NewMemory(stubEmbed, WithLogger(noopLogger{}))
	if err == nil || !contains(err.Error(), "DSN") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewMemory_InvalidDSN(t *testing.T) {
	_, err := NewMemory(stubEmbed, WithDSN("not-a-valid-dsn"), WithLogger(noopLogger{}))
	if err == nil {
		t.Fatal("expected parse DSN error")
	}
}

func TestNewMemory_Defaults(t *testing.T) {
	m := newTestMemory(t, &stubDB{})
	if m.table != DefaultTable {
		t.Fatalf("table = %q", m.table)
	}
	if m.textCol != DefaultTextCol || m.embeddingCol != DefaultEmbeddingCol {
		t.Fatalf("cols = %q %q", m.textCol, m.embeddingCol)
	}
	if m.defaultLimit != DefaultLoadLimit || m.defaultMinScore != DefaultMinScore {
		t.Fatalf("defaults = %d %v", m.defaultLimit, m.defaultMinScore)
	}
}

func TestStore_NoDB(t *testing.T) {
	m := &Memory{embed: stubEmbed, db: nil}
	_, err := m.Store(context.Background(), interfaces.MemoryScope{}, interfaces.MemoryRecord{Text: "x"})
	if err == nil || !contains(err.Error(), "database is not set") {
		t.Fatalf("err = %v", err)
	}
}

func TestStore_EmbedError(t *testing.T) {
	m := newTestMemory(t, &stubDB{})
	m.embed = errEmbed
	_, err := m.Store(context.Background(), interfaces.MemoryScope{UserID: "u1"}, interfaces.MemoryRecord{Text: "x"})
	if err == nil || !contains(err.Error(), "embed") {
		t.Fatalf("err = %v", err)
	}
}

func TestStore_Create(t *testing.T) {
	db := &stubDB{
		queryRows: &stubIDRows{id: "mem-1"},
	}
	m := newTestMemory(t, db)

	id, err := m.Store(context.Background(),
		interfaces.MemoryScope{UserID: "u1", TenantID: "t1", Tags: map[string]string{"team": "a"}},
		interfaces.MemoryRecord{
			Text:     "likes dark mode",
			Kind:     interfaces.MemoryKind("preference"),
			Metadata: map[string]string{"source": "run-1"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if id != "mem-1" {
		t.Fatalf("id = %q", id)
	}
	if !contains(db.lastQuerySQL, "INSERT INTO "+DefaultTable) {
		t.Fatalf("sql = %s", db.lastQuerySQL)
	}
	if !contains(db.lastQuerySQL, "ON CONFLICT") {
		t.Fatalf("sql missing upsert: %s", db.lastQuerySQL)
	}
	if contains(db.lastQuerySQL, "updated_at = updated_at.") {
		t.Fatalf("sql must not reference updated_at as table: %s", db.lastQuerySQL)
	}
	if !contains(db.lastQuerySQL, "updated_at = EXCLUDED.updated_at") {
		t.Fatalf("sql missing updated_at upsert: %s", db.lastQuerySQL)
	}
	if !contains(db.lastQuerySQL, "created_at = "+DefaultTable+".created_at") {
		t.Fatalf("sql must preserve created_at on conflict: %s", db.lastQuerySQL)
	}
	if len(db.lastQueryArgs) != 12 {
		t.Fatalf("args len = %d", len(db.lastQueryArgs))
	}
}

func TestStore_UpsertWithID(t *testing.T) {
	db := &stubDB{
		queryRows: &stubIDRows{id: "fixed-id"},
	}
	m := newTestMemory(t, db)

	id, err := m.Store(context.Background(),
		interfaces.MemoryScope{UserID: "u1"},
		interfaces.MemoryRecord{Text: "updated"},
		interfaces.WithMemoryID("fixed-id"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if id != "fixed-id" {
		t.Fatalf("id = %q", id)
	}
	if db.lastQueryArgs[0] != "fixed-id" {
		t.Fatalf("first arg = %v", db.lastQueryArgs[0])
	}
}

func TestLoad_Semantic(t *testing.T) {
	now := time.Now().UTC()
	user := "u1"
	db := &stubDB{
		queryRows: &stubMemoryRows{data: []memoryRowData{{
			id:        "abc",
			text:      "likes dark mode",
			kind:      "preference",
			userID:    &user,
			metadata:  []byte(`{"source":"run-1"}`),
			createdAt: now,
			updatedAt: now,
			score:     0.91,
		}}},
	}
	m := newTestMemory(t, db)

	entries, err := m.Load(context.Background(),
		interfaces.MemoryScope{UserID: "u1"},
		"theme preference",
		interfaces.WithLoadLimit(5),
		interfaces.WithMinScore(0.8),
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("len = %d", len(entries))
	}
	if entries[0].ID != "abc" || entries[0].Text != "likes dark mode" || entries[0].Score != 0.91 {
		t.Fatalf("entry = %#v", entries[0])
	}
	if entries[0].Metadata["source"] != "run-1" {
		t.Fatalf("metadata = %#v", entries[0].Metadata)
	}
	if !contains(db.lastQuerySQL, "<=>") {
		t.Fatalf("sql missing vector search: %s", db.lastQuerySQL)
	}
	if !contains(db.lastQuerySQL, "user_id = $") {
		t.Fatalf("sql missing scope filter: %s", db.lastQuerySQL)
	}
}

func TestLoad_Recency(t *testing.T) {
	now := time.Now().UTC()
	db := &stubDB{
		queryRows: &stubMemoryRows{data: []memoryRowData{{
			id:        "note-1",
			text:      "recent note",
			kind:      "note",
			createdAt: now,
			updatedAt: now,
		}}},
	}
	m := newTestMemory(t, db)

	entries, err := m.Load(context.Background(), interfaces.MemoryScope{UserID: "u1"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Text != "recent note" {
		t.Fatalf("entries = %#v", entries)
	}
	if contains(db.lastQuerySQL, "<=>") {
		t.Fatalf("unexpected vector search for empty query: %s", db.lastQuerySQL)
	}
	if !contains(db.lastQuerySQL, "ORDER BY updated_at DESC") {
		t.Fatalf("sql missing recency sort: %s", db.lastQuerySQL)
	}
}

func TestLoad_EmbedError(t *testing.T) {
	m := newTestMemory(t, &stubDB{})
	m.embed = errEmbed
	_, err := m.Load(context.Background(), interfaces.MemoryScope{UserID: "u1"}, "query")
	if err == nil || !contains(err.Error(), "embed") {
		t.Fatalf("err = %v", err)
	}
}

func TestLoad_SkipsExpired(t *testing.T) {
	past := time.Now().UTC().Add(-time.Hour)
	db := &stubDB{
		queryRows: &stubMemoryRows{data: []memoryRowData{{
			id:        "expired",
			text:      "old",
			expiresAt: &past,
			createdAt: past,
			updatedAt: past,
		}}},
	}
	m := newTestMemory(t, db)

	entries, err := m.Load(context.Background(), interfaces.MemoryScope{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected expired entry to be omitted, got %#v", entries)
	}
}

func TestLoad_KindsFilter(t *testing.T) {
	db := &stubDB{queryRows: &stubMemoryRows{}}
	m := newTestMemory(t, db)

	_, err := m.Load(context.Background(), interfaces.MemoryScope{UserID: "u1"}, "",
		interfaces.WithLoadKinds("fact", "note"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if !contains(db.lastQuerySQL, "kind = ANY") {
		t.Fatalf("sql = %s", db.lastQuerySQL)
	}
}

func TestClear(t *testing.T) {
	db := &stubDB{}
	m := newTestMemory(t, db)

	if err := m.Clear(context.Background(), interfaces.MemoryScope{TenantID: "t1"}); err != nil {
		t.Fatal(err)
	}
	if !contains(db.lastExecSQL, "DELETE FROM "+DefaultTable) {
		t.Fatalf("sql = %s", db.lastExecSQL)
	}
	if !contains(db.lastExecSQL, "tenant_id = $1") {
		t.Fatalf("sql = %s", db.lastExecSQL)
	}
}

func TestClear_EmptyScope(t *testing.T) {
	m := newTestMemory(t, &stubDB{})
	err := m.Clear(context.Background(), interfaces.MemoryScope{})
	if err == nil || !contains(err.Error(), "scope must include") {
		t.Fatalf("err = %v", err)
	}
}

func TestEncodeDecodeScopeTags(t *testing.T) {
	meta := map[string]string{
		"user_id":    "u1",
		"project_id": "p1",
		"env":        "prod",
	}
	encoded := encodeScopeTags(meta)
	if len(encoded) != 2 {
		t.Fatalf("encoded = %#v", encoded)
	}
	decoded := decodeScopeTags([]string{"project_id=p1", "env=prod"})
	if decoded["project_id"] != "p1" || decoded["env"] != "prod" {
		t.Fatalf("decoded = %#v", decoded)
	}
}

func TestBuildScopeArgs_CustomTag(t *testing.T) {
	sql, args := buildScopeArgs(interfaces.MemoryScope{
		Tags: map[string]string{"team": "alpha"},
	}, nil, 1)
	if !contains(sql, "scope_tags @> $1::text[]") {
		t.Fatalf("sql = %q", sql)
	}
	tags, ok := args[0].([]string)
	if !ok || len(tags) != 1 || tags[0] != "team=alpha" {
		t.Fatalf("args = %#v", args)
	}
}

func TestScanMemoryRows_MetadataError(t *testing.T) {
	now := time.Now().UTC()
	rows := &stubMemoryRows{data: []memoryRowData{{
		id:        "x",
		text:      "t",
		metadata:  []byte("not-json"),
		createdAt: now,
		updatedAt: now,
	}}}
	_, err := scanMemoryRows(rows)
	if err == nil || !contains(err.Error(), "unmarshal metadata") {
		t.Fatalf("err = %v", err)
	}
}

func TestMarshalMetadata(t *testing.T) {
	raw, err := marshalMetadata(nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != "{}" {
		t.Fatalf("raw = %s", raw)
	}
}

func contains(s, sub string) bool {
	return len(sub) > 0 && len(s) >= len(sub) && (s == sub || containsStr(s, sub))
}

func containsStr(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
