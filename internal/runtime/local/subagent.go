package local

import sdkruntime "github.com/agenticenv/agent-sdk-go/internal/runtime"

// subAgentRoute is the local runtime's internal representation of a delegatable sub-agent.
// Built from ExecuteRequest.SubAgents by buildSubAgentRoutes; not shared with any other package.
type subAgentRoute struct {
	name     string
	runtime  *LocalRuntime
	children map[string]subAgentRoute
}

// buildSubAgentRoutes converts the runtime-agnostic SubAgentSpec tree (from ExecuteRequest)
// into local-specific routes. Each spec's Runtime is type-asserted to *LocalRuntime;
// specs with a non-local runtime are skipped (mixed-runtime delegation not supported).
func buildSubAgentRoutes(specs []*sdkruntime.SubAgentSpec) map[string]subAgentRoute {
	if len(specs) == 0 {
		return nil
	}
	out := make(map[string]subAgentRoute, len(specs))
	for _, spec := range specs {
		if spec == nil {
			continue
		}
		lr, ok := spec.Runtime.(*LocalRuntime)
		if !ok {
			continue
		}
		out[spec.ToolName] = subAgentRoute{
			name:     spec.Name,
			runtime:  lr,
			children: buildSubAgentRoutes(spec.Children),
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
