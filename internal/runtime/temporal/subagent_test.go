package temporal

import (
	"testing"

	sdkruntime "github.com/agenticenv/agent-sdk-go/internal/runtime"
	"github.com/agenticenv/agent-sdk-go/internal/runtime/base"
	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

func testTemporalRuntime(name, taskQueue string) *TemporalRuntime {
	return &TemporalRuntime{
		Runtime: base.Runtime{
			AgentSpec: sdkruntime.AgentSpec{Name: name, SystemPrompt: "p"},
			AgentConfig: sdkruntime.AgentConfig{
				Limits: sdkruntime.AgentLimits{MaxIterations: 3},
			},
			ToolExecutionMode: types.AgentToolExecutionModeParallel,
		},
		taskQueue:         taskQueue,
		policyFingerprint: "policy",
	}
}

func TestBuildSubAgentRoutes_setsAgentFingerprint(t *testing.T) {
	subRT := testTemporalRuntime("sub", "sub-queue")
	subTools := []interfaces.Tool{fpTool{name: "sub_tool"}}
	want := computeAgentFingerprintFromRuntime(subRT, subTools)

	routes := buildSubAgentRoutes([]*sdkruntime.SubAgentSpec{{
		Name:     "Sub",
		ToolName: "subagent_Sub",
		Runtime:  subRT,
		Tools:    subTools,
	}})
	route, ok := routes["subagent_Sub"]
	if !ok {
		t.Fatal("missing route")
	}
	if route.TaskQueue != "sub-queue" {
		t.Fatalf("task queue: got %q", route.TaskQueue)
	}
	if route.AgentFingerprint != want {
		t.Fatalf("fingerprint: got %q want %q", route.AgentFingerprint, want)
	}
	if route.AgentFingerprint == "" {
		t.Fatal("expected non-empty sub-agent fingerprint")
	}
}

func TestBuildSubAgentRoutes_nestedChildFingerprint(t *testing.T) {
	childRT := testTemporalRuntime("child", "child-queue")
	childTools := []interfaces.Tool{fpTool{name: "child_tool"}}
	wantChild := computeAgentFingerprintFromRuntime(childRT, childTools)

	parentRT := testTemporalRuntime("parent", "parent-queue")
	routes := buildSubAgentRoutes([]*sdkruntime.SubAgentSpec{{
		Name:     "Parent",
		ToolName: "subagent_Parent",
		Runtime:  parentRT,
		Tools:    []interfaces.Tool{fpTool{name: "parent_tool"}},
		Children: []*sdkruntime.SubAgentSpec{{
			Name:     "Child",
			ToolName: "subagent_Child",
			Runtime:  childRT,
			Tools:    childTools,
		}},
	}})

	parentRoute := routes["subagent_Parent"]
	childRoute, ok := parentRoute.ChildRoutes["subagent_Child"]
	if !ok {
		t.Fatal("missing nested child route")
	}
	if childRoute.AgentFingerprint != wantChild {
		t.Fatalf("child fingerprint: got %q want %q", childRoute.AgentFingerprint, wantChild)
	}
}

func TestBuildSubAgentRoutes_parentAndSubFingerprintsDiffer(t *testing.T) {
	subRT := testTemporalRuntime("sub", "sub-queue")
	subRT.AgentSpec.SystemPrompt = "sub prompt"
	parentRT := testTemporalRuntime("parent", "parent-queue")
	parentRT.AgentSpec.SystemPrompt = "parent prompt"

	routes := buildSubAgentRoutes([]*sdkruntime.SubAgentSpec{{
		Name:     "Parent",
		ToolName: "subagent_Parent",
		Runtime:  parentRT,
		Tools:    []interfaces.Tool{fpTool{name: "parent_tool"}},
	}, {
		Name:     "Sub",
		ToolName: "subagent_Sub",
		Runtime:  subRT,
		Tools:    []interfaces.Tool{fpTool{name: "sub_tool"}},
	}})

	parentFP := routes["subagent_Parent"].AgentFingerprint
	subFP := routes["subagent_Sub"].AgentFingerprint
	if parentFP == "" || subFP == "" {
		t.Fatal("expected non-empty fingerprints")
	}
	if parentFP == subFP {
		t.Fatalf("parent and sub fingerprints must differ: %q", parentFP)
	}
}

func TestBuildSubAgentRoutes_nonTemporalSkipsFingerprint(t *testing.T) {
	routes := buildSubAgentRoutes([]*sdkruntime.SubAgentSpec{{
		Name:     "Local",
		ToolName: "subagent_Local",
		Runtime:  nil,
		Tools:    []interfaces.Tool{fpTool{name: "t"}},
	}})
	route := routes["subagent_Local"]
	if route.AgentFingerprint != "" {
		t.Fatalf("non-temporal route should not set fingerprint: %q", route.AgentFingerprint)
	}
	if route.TaskQueue != "" {
		t.Fatalf("non-temporal route should not set task queue: %q", route.TaskQueue)
	}
}
