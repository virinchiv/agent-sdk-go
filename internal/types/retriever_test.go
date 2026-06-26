package types

import (
	"strings"
	"testing"
)

func TestRetrieverToolName(t *testing.T) {
	if got := RetrieverToolName("  wiki  "); got != "retriever_wiki" {
		t.Fatalf("got %q", got)
	}
	if RetrieverToolName("") != "" || RetrieverToolName("  ") != "" {
		t.Fatal("expected empty for missing key")
	}
}

func TestRetrieverNameFromToolName(t *testing.T) {
	name, ok := RetrieverNameFromToolName("retriever_kb")
	if !ok || name != "kb" {
		t.Fatalf("got (%q, %v)", name, ok)
	}
	if _, ok := RetrieverNameFromToolName("retriever_"); ok {
		t.Fatal("expected false for prefix only")
	}
	if _, ok := RetrieverNameFromToolName("other_kb"); ok {
		t.Fatal("expected false for wrong prefix")
	}
	if _, ok := RetrieverNameFromToolName(""); ok {
		t.Fatal("expected false for empty")
	}
}

func TestRetrieverToolName_roundTrip(t *testing.T) {
	key := "my_kb"
	toolName := RetrieverToolName(key)
	got, ok := RetrieverNameFromToolName(toolName)
	if !ok || got != key {
		t.Fatalf("round trip = (%q, %v)", got, ok)
	}
}

func TestRetrieverToolDisplayName(t *testing.T) {
	if got := RetrieverToolDisplayName("wiki"); got != "wiki Retriever Tool" {
		t.Fatalf("got %q", got)
	}
	if RetrieverToolDisplayName("  ") != "" {
		t.Fatal("expected empty")
	}
}

func TestRetrieverToolParamQueryValue(t *testing.T) {
	got, err := RetrieverToolParamQueryValue(map[string]any{RetrieverToolParamQuery: "  golang  "})
	if err != nil {
		t.Fatal(err)
	}
	if got != "golang" {
		t.Fatalf("got %q", got)
	}

	_, err = RetrieverToolParamQueryValue(nil)
	if err == nil {
		t.Fatal("expected error for nil args")
	}

	_, err = RetrieverToolParamQueryValue(map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing query")
	}

	_, err = RetrieverToolParamQueryValue(map[string]any{RetrieverToolParamQuery: 42})
	if err == nil {
		t.Fatal("expected error for non-string query")
	}

	_, err = RetrieverToolParamQueryValue(map[string]any{RetrieverToolParamQuery: "   "})
	if err == nil || !strings.Contains(err.Error(), "non-empty") {
		t.Fatalf("got %v", err)
	}
}
