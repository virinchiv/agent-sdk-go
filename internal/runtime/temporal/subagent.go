package temporal

import sdkruntime "github.com/agenticenv/agent-sdk-go/internal/runtime"

// SubAgentRoute tells the Temporal runtime how to delegate to a sub-agent child workflow.
// It is serialised into AgentWorkflowInput and propagated to child workflows unchanged.
type SubAgentRoute struct {
	Name             string                   `json:"name"`
	TaskQueue        string                   `json:"task_queue,omitempty"`
	ChildRoutes      map[string]SubAgentRoute `json:"child_routes,omitempty"`
	AgentFingerprint string                   `json:"agent_fingerprint,omitempty"`
}

// buildSubAgentRoutes converts the runtime-agnostic SubAgentSpec tree (from ExecuteRequest)
// into a Temporal-specific SubAgentRoute map. Each spec's Runtime is type-asserted to
// *TemporalRuntime to extract the task queue and per-run agent fingerprint (static runtime
// digests + resolved spec.Tools).
func buildSubAgentRoutes(specs []*sdkruntime.SubAgentSpec) map[string]SubAgentRoute {
	if len(specs) == 0 {
		return nil
	}
	out := make(map[string]SubAgentRoute, len(specs))
	for _, spec := range specs {
		if spec == nil {
			continue
		}
		route := SubAgentRoute{Name: spec.Name}
		if tr, ok := spec.Runtime.(*TemporalRuntime); ok {
			route.TaskQueue = tr.taskQueue
			route.AgentFingerprint = computeAgentFingerprintFromRuntime(tr, spec.Tools)
		}
		route.ChildRoutes = buildSubAgentRoutes(spec.Children)
		out[spec.ToolName] = route
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
