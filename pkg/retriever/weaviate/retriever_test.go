package weaviate

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	weaviateclient "github.com/weaviate/weaviate-go-client/v5/weaviate"
	"github.com/weaviate/weaviate/entities/models"
)

// noopLogger is a logger.Logger that discards all output, used in tests.
type noopLogger struct{}

func (noopLogger) Debug(ctx context.Context, msg string, _ ...any) {}
func (noopLogger) Info(ctx context.Context, msg string, _ ...any)  {}
func (noopLogger) Warn(ctx context.Context, msg string, _ ...any)  {}
func (noopLogger) Error(ctx context.Context, msg string, _ ...any) {}

func testWeaviateHost(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Host
}

func TestNewRetriever_MissingName(t *testing.T) {
	_, err := NewRetriever("", WithHost("localhost:8080"), WithClassName("Article"))
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("err = %v", err)
	}
	_, err = NewRetriever("   ", WithHost("localhost:8080"), WithClassName("Article"))
	if err == nil || !strings.Contains(err.Error(), "name is required") {
		t.Fatalf("whitespace-only name: err = %v", err)
	}
}

func TestNewRetriever_MissingClassName(t *testing.T) {
	_, err := NewRetriever("kb", WithHost("localhost:8080"))
	if err == nil || !strings.Contains(err.Error(), "className") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewRetriever_MissingHost(t *testing.T) {
	_, err := NewRetriever("kb", WithClassName("Article"))
	if err == nil || !strings.Contains(err.Error(), "host") {
		t.Fatalf("err = %v", err)
	}
}

func TestNewRetriever_Defaults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	r, err := NewRetriever("kb",
		WithHost(testWeaviateHost(t, srv)),
		WithClassName("Article"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if r.name != "kb" {
		t.Fatalf("name = %q", r.name)
	}
	if r.contentField != types.DefaultContentField {
		t.Fatalf("contentField = %q", r.contentField)
	}
	if r.sourceField != types.DefaultSourceField {
		t.Fatalf("sourceField = %q", r.sourceField)
	}
	if r.topK != types.DefaultTopK {
		t.Fatalf("topK = %d", r.topK)
	}
	if r.minScore != types.DefaultMinScore {
		t.Fatalf("minScore = %v", r.minScore)
	}
	if r.scheme != types.DefaultScheme {
		t.Fatalf("scheme = %q", r.scheme)
	}
	if r.client == nil {
		t.Fatal("client nil")
	}
}

func TestNewRetriever_WithClient(t *testing.T) {
	wc, err := weaviateclient.NewClient(weaviateclient.Config{
		Scheme: "http",
		Host:   "unused:0",
	})
	if err != nil {
		t.Fatal(err)
	}
	r, err := NewRetriever("articles",
		WithClient(wc),
		WithClassName("Article"),
		WithTopK(3),
		WithMinScore(0.5),
		WithContentField("body"),
		WithSourceField("url"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if r.topK != 3 || r.minScore != 0.5 {
		t.Fatalf("topK=%d minScore=%v", r.topK, r.minScore)
	}
	if r.contentField != "body" || r.sourceField != "url" {
		t.Fatalf("fields %q %q", r.contentField, r.sourceField)
	}
}

func TestWeaviateRetriever_Search_NoClient(t *testing.T) {
	r := &WeaviateRetriever{name: "kb", className: "Article", client: nil}
	_, err := r.Search(context.Background(), "query")
	if err == nil || !strings.Contains(err.Error(), "client is not set") {
		t.Fatalf("err = %v", err)
	}
}

func TestGetString(t *testing.T) {
	obj := map[string]interface{}{
		"content": "hello",
		"count":   42,
	}
	if got := getString(obj, "content"); got != "hello" {
		t.Fatalf("got %q", got)
	}
	if got := getString(obj, "missing"); got != "" {
		t.Fatalf("got %q", got)
	}
	if got := getString(obj, "count"); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestParseDocuments_NilAndEmpty(t *testing.T) {
	r := &WeaviateRetriever{className: "Article", contentField: "content", sourceField: "source", logger: noopLogger{}}

	docs, err := r.parseDocuments(context.Background(), nil)
	if err != nil || docs != nil {
		t.Fatalf("nil: docs=%v err=%v", docs, err)
	}

	docs, err = r.parseDocuments(context.Background(), &models.GraphQLResponse{})
	if err != nil || docs != nil {
		t.Fatalf("empty data: docs=%v err=%v", docs, err)
	}
}

func graphQLData(entries map[string]interface{}) map[string]models.JSONObject {
	out := make(map[string]models.JSONObject, len(entries))
	for k, v := range entries {
		out[k] = v
	}
	return out
}

func TestParseDocuments_InvalidResponse(t *testing.T) {
	r := &WeaviateRetriever{className: "Article", contentField: "content", sourceField: "source", logger: noopLogger{}}

	_, err := r.parseDocuments(context.Background(), &models.GraphQLResponse{
		Data: graphQLData(map[string]interface{}{"Get": "not-a-map"}),
	})
	if err == nil || !strings.Contains(err.Error(), "missing Get") {
		t.Fatalf("err = %v", err)
	}

	_, err = r.parseDocuments(context.Background(), &models.GraphQLResponse{
		Data: graphQLData(map[string]interface{}{
			"Get": map[string]interface{}{"Article": "not-a-slice"},
		}),
	})
	if err == nil || !strings.Contains(err.Error(), "missing class data") {
		t.Fatalf("err = %v", err)
	}
}

func TestParseDocuments_Success(t *testing.T) {
	r := &WeaviateRetriever{
		className:    "Article",
		contentField: "content",
		sourceField:  "source",
		logger:       noopLogger{},
	}
	result := &models.GraphQLResponse{
		Data: graphQLData(map[string]interface{}{
			"Get": map[string]interface{}{
				"Article": []interface{}{
					map[string]interface{}{
						"content": "first doc",
						"source":  "a.md",
						"tags":    []interface{}{"go"},
						"_additional": map[string]interface{}{
							"certainty": 0.91,
						},
					},
					"not-an-object",
					map[string]interface{}{
						"content": 42,
						"source":  "b.md",
					},
				},
			},
		}),
	}

	docs, err := r.parseDocuments(context.Background(), result)
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 2 {
		t.Fatalf("len(docs) = %d", len(docs))
	}
	if docs[0].Content != "first doc" || docs[0].Source != "a.md" || docs[0].Score != 0.91 {
		t.Fatalf("doc[0] = %#v", docs[0])
	}
	if docs[0].Metadata == nil {
		t.Fatal("metadata nil")
	}
	if docs[1].Content != "" || docs[1].Source != "b.md" || docs[1].Score != 0 {
		t.Fatalf("doc[1] = %#v", docs[1])
	}
}

func TestWeaviateRetriever_Search_Success(t *testing.T) {
	const className = "Article"
	mockBody := `{
		"data": {
			"Get": {
				"Article": [
					{
						"content": "Weaviate is a vector database",
						"source": "docs/weaviate.md",
						"_additional": { "certainty": 0.88 }
					}
				]
			}
		}
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/v1/meta", "/v1/.well-known/ready":
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{}`)
			return
		case "/v1/graphql":
			if r.Method != http.MethodPost {
				t.Errorf("graphql method = %s", r.Method)
			}
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "nearText") {
				t.Errorf("body missing nearText: %s", body)
			}
			if !strings.Contains(string(body), className) {
				t.Errorf("body missing class: %s", body)
			}
			_, _ = io.WriteString(w, mockBody)
			return
		default:
			t.Errorf("unexpected path = %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	r, err := NewRetriever("kb",
		WithHost(testWeaviateHost(t, srv)),
		WithClassName(className),
		WithTopK(2),
		WithMinScore(0.7),
	)
	if err != nil {
		t.Fatal(err)
	}

	docs, err := r.Search(context.Background(), "vector database")
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 {
		t.Fatalf("len(docs) = %d", len(docs))
	}
	want := interfaces.Document{
		Content: "Weaviate is a vector database",
		Source:  "docs/weaviate.md",
		Score:   0.88,
	}
	if docs[0].Content != want.Content || docs[0].Source != want.Source || docs[0].Score != want.Score {
		t.Fatalf("got %#v want content/source/score match", docs[0])
	}
	if docs[0].Metadata == nil {
		t.Fatal("metadata nil")
	}
}

func TestWeaviateRetriever_Name(t *testing.T) {
	wc, _ := weaviateclient.NewClient(weaviateclient.Config{Scheme: "http", Host: "unused:0"})
	r, err := NewRetriever("kb-articles", WithClient(wc), WithClassName("Article"))
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Name(); got != "kb-articles" {
		t.Fatalf("Name() = %q, want %q", got, "kb-articles")
	}
}

func TestNewRetriever_NameTrimmed(t *testing.T) {
	wc, _ := weaviateclient.NewClient(weaviateclient.Config{Scheme: "http", Host: "unused:0"})
	r, err := NewRetriever("  kb  ", WithClient(wc), WithClassName("Article"))
	if err != nil {
		t.Fatal(err)
	}
	if got := r.Name(); got != "kb" {
		t.Fatalf("Name() = %q, want trimmed %q", got, "kb")
	}
}

func TestNewRetriever_WithLogger(t *testing.T) {
	wc, _ := weaviateclient.NewClient(weaviateclient.Config{Scheme: "http", Host: "unused:0"})
	r, err := NewRetriever("kb", WithClient(wc), WithClassName("Article"), WithLogger(noopLogger{}))
	if err != nil {
		t.Fatal(err)
	}
	if r.logger == nil {
		t.Fatal("logger is nil after WithLogger")
	}
	if _, ok := r.logger.(noopLogger); !ok {
		t.Fatalf("logger type = %T, want noopLogger", r.logger)
	}
}

func TestWeaviateRetriever_Search_GraphQLError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = io.WriteString(w, `{"error":[{"message":"boom"}]}`)
	}))
	defer srv.Close()

	r, err := NewRetriever("kb",
		WithHost(testWeaviateHost(t, srv)),
		WithClassName("Article"),
	)
	if err != nil {
		t.Fatal(err)
	}

	_, err = r.Search(context.Background(), "query")
	if err == nil {
		t.Fatal("expected error")
	}
}
