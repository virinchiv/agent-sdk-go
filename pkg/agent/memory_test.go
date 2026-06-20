package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

func TestNewMemoryTool_nil(t *testing.T) {
	if NewMemoryTool(nil) != nil {
		t.Fatal("expected nil")
	}
}

func TestMemoryTool_metadata(t *testing.T) {
	tool := NewMemoryTool(func(context.Context, []interfaces.MemoryRecord) error { return nil })
	if tool.Name() != types.SaveMemoryToolName {
		t.Fatalf("Name = %q", tool.Name())
	}
	if tool.(*MemoryTool).ToolKind() != types.ToolKindMemory {
		t.Fatalf("ToolKind = %q", tool.(*MemoryTool).ToolKind())
	}
}

func TestMemoryTool_Execute_storesRecord(t *testing.T) {
	var stored []interfaces.MemoryRecord
	tool := NewMemoryTool(func(_ context.Context, records []interfaces.MemoryRecord) error {
		stored = records
		return nil
	})

	out, err := tool.Execute(context.Background(), map[string]any{
		types.MemoryToolParamText: "favorite color is blue",
		types.MemoryToolParamKind: "preference",
	})
	if err != nil {
		t.Fatal(err)
	}
	if out != "memory saved" {
		t.Fatalf("out = %v", out)
	}
	if len(stored) != 1 || stored[0].Text != "favorite color is blue" || stored[0].Kind != "preference" {
		t.Fatalf("stored = %+v", stored)
	}
}

func TestMemoryTool_Execute_emptyText(t *testing.T) {
	tool := NewMemoryTool(func(context.Context, []interfaces.MemoryRecord) error { return nil })
	_, err := tool.Execute(context.Background(), map[string]any{
		types.MemoryToolParamText: "   ",
	})
	if err == nil {
		t.Fatal("expected error for empty text")
	}
}

func TestMemoryTool_Execute_missingText(t *testing.T) {
	tool := NewMemoryTool(func(context.Context, []interfaces.MemoryRecord) error { return nil })
	_, err := tool.Execute(context.Background(), map[string]any{})
	if err == nil {
		t.Fatal("expected error for missing text")
	}
}

func TestMemoryTool_Execute_storeError(t *testing.T) {
	tool := NewMemoryTool(func(context.Context, []interfaces.MemoryRecord) error {
		return errors.New("backend down")
	})
	_, err := tool.Execute(context.Background(), map[string]any{
		types.MemoryToolParamText: "remember this",
	})
	if err == nil || err.Error() != "backend down" {
		t.Fatalf("err = %v", err)
	}
}

func TestMemoryTool_Execute_withoutKind(t *testing.T) {
	var stored []interfaces.MemoryRecord
	tool := NewMemoryTool(func(_ context.Context, records []interfaces.MemoryRecord) error {
		stored = records
		return nil
	})

	_, err := tool.Execute(context.Background(), map[string]any{
		types.MemoryToolParamText: "remember this",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(stored) != 1 || stored[0].Kind != "" {
		t.Fatalf("stored = %+v", stored)
	}
}

func TestMemoryTool_Parameters(t *testing.T) {
	tool := NewMemoryTool(func(context.Context, []interfaces.MemoryRecord) error { return nil })
	schema := tool.(*MemoryTool).Parameters()
	required, ok := schema["required"].([]string)
	if !ok || len(required) != 1 || required[0] != types.MemoryToolParamText {
		t.Fatalf("required = %v", schema["required"])
	}
	props, ok := schema["properties"].(map[string]interfaces.JSONSchema)
	if !ok || props[types.MemoryToolParamText] == nil || props[types.MemoryToolParamKind] == nil {
		t.Fatalf("properties = %v", schema["properties"])
	}
}

func TestMemoryTool_Execute_nilStore(t *testing.T) {
	tool := &MemoryTool{}
	_, err := tool.Execute(context.Background(), map[string]any{
		types.MemoryToolParamText: "x",
	})
	if !errors.Is(err, ErrMemoryToolNotExecutable) {
		t.Fatalf("err = %v, want ErrMemoryToolNotExecutable", err)
	}
}

func TestNewRegisteredMemoryTool(t *testing.T) {
	tool := NewRegisteredMemoryTool()
	if tool == nil || tool.Name() != types.SaveMemoryToolName {
		t.Fatalf("tool = %+v", tool)
	}
}
