package types

import "testing"

type stubKindTool struct{ kind ToolKind }

func (s stubKindTool) ToolKind() ToolKind { return s.kind }

type stubNativeTool struct{}

func TestKindOf(t *testing.T) {
	if KindOf(nil) != ToolKindNative {
		t.Fatalf("nil = %q", KindOf(nil))
	}
	if KindOf(stubNativeTool{}) != ToolKindNative {
		t.Fatal("native tool without provider")
	}
	if KindOf(stubKindTool{kind: ToolKindMCP}) != ToolKindMCP {
		t.Fatal("mcp kind")
	}
	if KindOf(stubKindTool{kind: ToolKindMemory}) != ToolKindMemory {
		t.Fatal("memory kind")
	}
	if KindOf(stubKindTool{kind: ""}) != ToolKindNative {
		t.Fatal("empty kind falls back to native")
	}
}

func TestToolKind_CountsTowardToolTelemetry(t *testing.T) {
	if !ToolKindNative.CountsTowardToolTelemetry() || !ToolKindMCP.CountsTowardToolTelemetry() {
		t.Fatal("native and mcp count toward tool telemetry")
	}
	for _, k := range []ToolKind{ToolKindSubAgent, ToolKindA2A, ToolKindRetriever} {
		if k.CountsTowardToolTelemetry() {
			t.Fatalf("%q should not count toward tool telemetry", k)
		}
	}
	if !ToolKindMemory.CountsTowardToolTelemetry() {
		t.Fatal("memory tool should count toward tool telemetry")
	}
}

func TestToolKind_HooksEligible(t *testing.T) {
	if !ToolKindNative.HooksEligible() || !ToolKindMCP.HooksEligible() {
		t.Fatal("native and mcp should be hook eligible")
	}
	for _, k := range []ToolKind{ToolKindA2A, ToolKindSubAgent, ToolKindRetriever, ToolKindMemory} {
		if k.HooksEligible() {
			t.Fatalf("%q should not be hook eligible", k)
		}
	}
}
