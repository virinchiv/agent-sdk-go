package weaviate

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	weaviateclient "github.com/weaviate/weaviate-go-client/v5/weaviate"
	"github.com/weaviate/weaviate/entities/models"
)

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

func graphQLData(entries map[string]interface{}) map[string]models.JSONObject {
	out := make(map[string]models.JSONObject, len(entries))
	for k, v := range entries {
		out[k] = v
	}
	return out
}

func TestNew_MissingHost(t *testing.T) {
	_, err := NewMemory()
	if err == nil || !strings.Contains(err.Error(), "host") {
		t.Fatalf("err = %v", err)
	}
}

func TestNew_Defaults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	mem, err := NewMemory(WithHost(testWeaviateHost(t, srv)))
	if err != nil {
		t.Fatal(err)
	}
	if mem.className != DefaultClassName {
		t.Fatalf("className = %q", mem.className)
	}
	if mem.textField != DefaultTextField {
		t.Fatalf("textField = %q", mem.textField)
	}
	if mem.defaultLimit != DefaultLoadLimit {
		t.Fatalf("defaultLimit = %d", mem.defaultLimit)
	}
	if mem.scheme != types.DefaultScheme {
		t.Fatalf("scheme = %q", mem.scheme)
	}
}

func TestNew_WithClient(t *testing.T) {
	wc, err := weaviateclient.NewClient(weaviateclient.Config{Scheme: "http", Host: "unused:0"})
	if err != nil {
		t.Fatal(err)
	}
	mem, err := NewMemory(
		WithClient(wc),
		WithClassName("CustomMemory"),
		WithTextField("body"),
		WithDefaultLimit(3),
		WithDefaultMinScore(0.5),
	)
	if err != nil {
		t.Fatal(err)
	}
	if mem.className != "CustomMemory" || mem.textField != "body" {
		t.Fatalf("class/text = %q %q", mem.className, mem.textField)
	}
	if mem.defaultLimit != 3 || mem.defaultMinScore != 0.5 {
		t.Fatalf("defaults = %d %v", mem.defaultLimit, mem.defaultMinScore)
	}
}

func TestStore_NoClient(t *testing.T) {
	mem := &Memory{className: DefaultClassName, client: nil}
	_, err := mem.Store(context.Background(), interfaces.MemoryScope{}, interfaces.MemoryRecord{Text: "x"})
	if err == nil || !strings.Contains(err.Error(), "client is not set") {
		t.Fatalf("err = %v", err)
	}
}

func TestStore_Create(t *testing.T) {
	const className = DefaultClassName
	var gotBody string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/meta", "/v1/.well-known/ready":
			w.WriteHeader(http.StatusOK)
			return
		case "/v1/objects":
			if r.Method != http.MethodPost {
				t.Errorf("method = %s", r.Method)
			}
			body, _ := io.ReadAll(r.Body)
			gotBody = string(body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `{"class":"AgentMemory","id":"11111111-1111-1111-1111-111111111111"}`)
			return
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	mem, err := NewMemory(WithHost(testWeaviateHost(t, srv)), WithLogger(noopLogger{}))
	if err != nil {
		t.Fatal(err)
	}

	id, err := mem.Store(context.Background(),
		interfaces.MemoryScope{UserID: "u1", TenantID: "t1"},
		interfaces.MemoryRecord{
			Text:     "likes dark mode",
			Kind:     interfaces.MemoryKind("preference"),
			Metadata: map[string]string{"source": "run-1"},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	if id != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("id = %q", id)
	}
	if !strings.Contains(gotBody, className) {
		t.Fatalf("body missing class: %s", gotBody)
	}
	if !strings.Contains(gotBody, "likes dark mode") || !strings.Contains(gotBody, "u1") {
		t.Fatalf("body = %s", gotBody)
	}
}

func TestStore_UpsertUpdate(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/meta", "/v1/.well-known/ready":
			w.WriteHeader(http.StatusOK)
			return
		case "/v1/objects/22222222-2222-2222-2222-222222222222":
			if r.Method != http.MethodPatch {
				t.Errorf("method = %s", r.Method)
			}
			w.WriteHeader(http.StatusNoContent)
			return
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	mem, err := NewMemory(WithHost(testWeaviateHost(t, srv)), WithLogger(noopLogger{}))
	if err != nil {
		t.Fatal(err)
	}

	id, err := mem.Store(context.Background(),
		interfaces.MemoryScope{UserID: "u1"},
		interfaces.MemoryRecord{Text: "updated"},
		interfaces.WithMemoryID("22222222-2222-2222-2222-222222222222"),
	)
	if err != nil {
		t.Fatal(err)
	}
	if id != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("id = %q", id)
	}
}

func TestLoad_Semantic(t *testing.T) {
	mockBody := `{
		"data": {
			"Get": {
				"AgentMemory": [
					{
						"text": "likes dark mode",
						"kind": "preference",
						"user_id": "u1",
						"metadata": "{\"source\":\"run-1\"}",
						"_additional": { "id": "abc", "certainty": 0.91 }
					}
				]
			}
		}
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/meta", "/v1/.well-known/ready":
			w.WriteHeader(http.StatusOK)
			return
		case "/v1/graphql":
			body, _ := io.ReadAll(r.Body)
			if !strings.Contains(string(body), "nearText") {
				t.Errorf("body missing nearText: %s", body)
			}
			_, _ = io.WriteString(w, mockBody)
			return
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	mem, err := NewMemory(WithHost(testWeaviateHost(t, srv)), WithLogger(noopLogger{}))
	if err != nil {
		t.Fatal(err)
	}

	entries, err := mem.Load(context.Background(),
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
}

func TestLoad_Recency(t *testing.T) {
	mockBody := `{
		"data": {
			"Get": {
				"AgentMemory": [
					{
						"text": "recent note",
						"kind": "note",
						"user_id": "u1",
						"_additional": { "id": "note-1" }
					}
				]
			}
		}
	}`

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/meta", "/v1/.well-known/ready":
			w.WriteHeader(http.StatusOK)
			return
		case "/v1/graphql":
			body, _ := io.ReadAll(r.Body)
			bodyStr := string(body)
			if strings.Contains(bodyStr, "nearText") {
				t.Errorf("unexpected nearText for empty query: %s", body)
			}
			if !strings.Contains(bodyStr, "sort") {
				t.Errorf("body missing sort: %s", body)
			}
			_, _ = io.WriteString(w, mockBody)
			return
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	mem, err := NewMemory(WithHost(testWeaviateHost(t, srv)), WithLogger(noopLogger{}))
	if err != nil {
		t.Fatal(err)
	}

	entries, err := mem.Load(context.Background(), interfaces.MemoryScope{UserID: "u1"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 || entries[0].Text != "recent note" {
		t.Fatalf("entries = %#v", entries)
	}
}

func TestClear(t *testing.T) {
	var gotDelete bool

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/v1/meta", "/v1/.well-known/ready":
			w.WriteHeader(http.StatusOK)
			return
		case "/v1/batch/objects":
			if r.Method != http.MethodDelete {
				t.Errorf("method = %s", r.Method)
			}
			gotDelete = true
			w.WriteHeader(http.StatusOK)
			_, _ = io.WriteString(w, `{"results":{"matches":1,"successful":1}}`)
			return
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	mem, err := NewMemory(WithHost(testWeaviateHost(t, srv)), WithLogger(noopLogger{}))
	if err != nil {
		t.Fatal(err)
	}

	if err := mem.Clear(context.Background(), interfaces.MemoryScope{TenantID: "t1"}); err != nil {
		t.Fatal(err)
	}
	if !gotDelete {
		t.Fatal("expected batch delete")
	}
}

func TestClear_EmptyScope(t *testing.T) {
	wc, _ := weaviateclient.NewClient(weaviateclient.Config{Scheme: "http", Host: "unused:0"})
	mem, err := NewMemory(WithClient(wc))
	if err != nil {
		t.Fatal(err)
	}
	err = mem.Clear(context.Background(), interfaces.MemoryScope{})
	if err == nil || !strings.Contains(err.Error(), "scope must include") {
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

	decoded := decodeScopeTags([]interface{}{"project_id=p1", "env=prod"})
	if decoded["project_id"] != "p1" || decoded["env"] != "prod" {
		t.Fatalf("decoded = %#v", decoded)
	}
}

func TestParseEntry_MetadataError(t *testing.T) {
	_, err := parseEntry(map[string]interface{}{
		"text":     "x",
		"metadata": "not-json",
	}, DefaultTextField)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestBuildProperties_Metadata(t *testing.T) {
	props, err := buildProperties(DefaultTextField,
		interfaces.MemoryScope{Tags: map[string]string{"team": "a"}},
		interfaces.MemoryRecord{
			Text:     "hello",
			Metadata: map[string]string{"k": "v"},
		},
		mustParseTime(t, "2026-01-01T00:00:00Z"),
		true,
	)
	if err != nil {
		t.Fatal(err)
	}
	raw, ok := props[PropMetadata].(string)
	if !ok {
		t.Fatalf("metadata type = %T", props[PropMetadata])
	}
	var decoded map[string]string
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["k"] != "v" {
		t.Fatalf("decoded = %#v", decoded)
	}
	tags, ok := props[PropScopeTags].([]string)
	if !ok || len(tags) != 1 || tags[0] != "team=a" {
		t.Fatalf("tags = %#v", props[PropScopeTags])
	}
}

func TestParseEntries_InvalidResponse(t *testing.T) {
	mem := &Memory{className: DefaultClassName, textField: DefaultTextField, logger: noopLogger{}}
	_, err := mem.parseEntries(&models.GraphQLResponse{
		Data: graphQLData(map[string]interface{}{"Get": "bad"}),
	})
	if err == nil || !strings.Contains(err.Error(), "missing Get") {
		t.Fatalf("err = %v", err)
	}

	entries, err := mem.parseEntries(&models.GraphQLResponse{
		Data: graphQLData(map[string]interface{}{
			"Get": map[string]interface{}{DefaultClassName: nil},
		}),
	})
	if err != nil {
		t.Fatalf("null class should be empty result: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("entries = %#v", entries)
	}
}

func mustParseTime(t *testing.T, raw string) time.Time {
	t.Helper()
	ts, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		t.Fatal(err)
	}
	return ts.UTC()
}
