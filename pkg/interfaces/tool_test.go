package interfaces

import (
	"context"
	"testing"
)

type stubKindTool struct{ kind string }

func (s stubKindTool) ToolKind() string                                       { return s.kind }
func (stubKindTool) Name() string                                             { return "x" }
func (stubKindTool) DisplayName() string                                      { return "x" }
func (stubKindTool) Description() string                                      { return "" }
func (stubKindTool) Parameters() JSONSchema                                   { return JSONSchema{"type": "object"} }
func (stubKindTool) Execute(_ context.Context, _ map[string]any) (any, error) { return nil, nil }

type stubNativeTool struct{}

func (stubNativeTool) Name() string           { return "n" }
func (stubNativeTool) DisplayName() string    { return "n" }
func (stubNativeTool) Description() string    { return "" }
func (stubNativeTool) Parameters() JSONSchema { return JSONSchema{"type": "object"} }
func (stubNativeTool) Execute(_ context.Context, _ map[string]any) (any, error) {
	return nil, nil
}

func TestKindOf(t *testing.T) {
	if KindOf(nil) != "native" {
		t.Fatalf("nil = %q", KindOf(nil))
	}
	if KindOf(stubNativeTool{}) != "native" {
		t.Fatal("native tool without provider")
	}
	if KindOf(stubKindTool{kind: "mcp"}) != "mcp" {
		t.Fatal("mcp kind")
	}
	if KindOf(stubKindTool{kind: ""}) != "native" {
		t.Fatal("empty kind falls back to native")
	}
}
