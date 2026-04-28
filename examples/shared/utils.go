package shared

import (
	"fmt"
	"strings"

	"github.com/agenticenv/agent-sdk-go/pkg/agent"
)

// RunResultFromFinishedEvent returns the typed result from a RUN_FINISHED event, or nil.
func RunResultFromFinishedEvent(ev agent.AgentEvent) *agent.AgentRunResult {
	if ev == nil || ev.Type() != agent.AgentEventTypeRunFinished {
		return nil
	}
	fin, ok := ev.(*agent.AgentRunFinishedEvent)
	if !ok || fin == nil {
		return nil
	}
	res, _ := fin.Result.(*agent.AgentRunResult)
	return res
}

// ToolApprovalValueFromEvent returns the CUSTOM tool-approval payload when ev is that stream event.
func ToolApprovalValueFromEvent(ev agent.AgentEvent) (agent.AgentCustomEventApprovalValue, bool) {
	ce, ok := ev.(*agent.AgentCustomEvent)
	if !ok || ce == nil || ce.Name != string(agent.AgentCustomEventNameToolApproval) {
		return agent.AgentCustomEventApprovalValue{}, false
	}
	v, err := agent.ParseCustomEventApproval(ce)
	if err != nil || v.ApprovalToken == "" {
		return agent.AgentCustomEventApprovalValue{}, false
	}
	return v, true
}

// DelegationApprovalValueFromEvent returns the CUSTOM sub-agent delegation payload when ev is that stream event.
func DelegationApprovalValueFromEvent(ev agent.AgentEvent) (agent.AgentCustomEventDelegationValue, bool) {
	ce, ok := ev.(*agent.AgentCustomEvent)
	if !ok || ce == nil || ce.Name != string(agent.AgentCustomEventNameSubAgentDelegation) {
		return agent.AgentCustomEventDelegationValue{}, false
	}
	v, err := agent.ParseCustomEventDelegation(ce)
	if err != nil || v.ApprovalToken == "" {
		return agent.AgentCustomEventDelegationValue{}, false
	}
	return v, true
}

// MarksStreamDelta returns true when the event carries assistant or reasoning text deltas.
func MarksStreamDelta(ev agent.AgentEvent) bool {
	if ev == nil {
		return false
	}
	switch ev.Type() {
	case agent.AgentEventTypeTextMessageContent, agent.AgentEventTypeReasoningMessageContent:
		return true
	default:
		return false
	}
}

// UsageFooter returns a non-empty line describing token usage from a finished run, or "".
func UsageFooter(res *agent.AgentRunResult) string {
	if res == nil || res.Usage == nil {
		return ""
	}
	u := res.Usage
	b := strings.Builder{}
	fmt.Fprintf(&b, "[USAGE] prompt=%d completion=%d total=%d", u.PromptTokens, u.CompletionTokens, u.TotalTokens)
	if u.CachedPromptTokens > 0 {
		fmt.Fprintf(&b, " cached_prompt=%d", u.CachedPromptTokens)
	}
	if u.ReasoningTokens > 0 {
		fmt.Fprintf(&b, " reasoning=%d", u.ReasoningTokens)
	}
	return b.String()
}
