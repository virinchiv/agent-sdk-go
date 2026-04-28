package agent

import (
	"context"

	"github.com/agenticenv/agent-sdk-go/internal/types"
)

type ApprovalStatus = types.ApprovalStatus

const (
	ApprovalStatusNone        ApprovalStatus = types.ApprovalStatusNone
	ApprovalStatusPending     ApprovalStatus = types.ApprovalStatusPending
	ApprovalStatusApproved    ApprovalStatus = types.ApprovalStatusApproved
	ApprovalStatusRejected    ApprovalStatus = types.ApprovalStatusRejected
	ApprovalStatusUnavailable ApprovalStatus = types.ApprovalStatusUnavailable
)

// ApprovalSender sends an approval result for the current run. Call once per request. Safe for concurrent use—
// multiple approvals may be pending when tools run in parallel.
type ApprovalSender = types.ApprovalSender

// ApprovalHandler is called for pending tool approval during [Agent.Run] (register with [WithApprovalHandler]).
// req.Respond is always set: call req.Respond(ApprovalStatusApproved) or Rejected when ready.
// The handler may return immediately after starting async work. Multiple invocations may run
// concurrently when tools are invoked in parallel.
type ApprovalHandler = types.ApprovalHandler

// ApprovalRequestName classifies approval callbacks (aligned with CUSTOM event roles).
type ApprovalRequestName = types.ApprovalRequestName

const (
	ApprovalRequestNameTool     = types.ApprovalRequestNameTool
	ApprovalRequestNameSubAgent = types.ApprovalRequestNameSubAgent
)

// ApprovalRequest describes a pending tool approval for [Agent.Run] and [Agent.RunAsync].
// Name + Value mirror CUSTOM stream events; use [ParseToolApproval] / [ParseDelegationApproval].
// Respond is always set; call it once with ApprovalStatusApproved or ApprovalStatusRejected.
// For streaming approvals, use [Agent.OnApproval] with the approval token from the CUSTOM event Value.
type ApprovalRequest = types.ApprovalRequest

// ToolApprovalRequestValue is the decoded Value for tool approvals (matches CUSTOM approval payload).
type ToolApprovalRequestValue = types.ToolApprovalRequestValue

// SubAgentDelegationApprovalRequestValue is the decoded Value for delegation approvals.
type SubAgentDelegationApprovalRequestValue = types.SubAgentDelegationApprovalRequestValue

// ParseToolApproval decodes Value when Name is [ApprovalRequestNameTool] (handles map[string]any from JSON).
func ParseToolApproval(req *ApprovalRequest) (ToolApprovalRequestValue, error) {
	return types.ParseToolApproval(req)
}

// ParseDelegationApproval decodes Value when Name is [ApprovalRequestNameSubAgent].
func ParseDelegationApproval(req *ApprovalRequest) (SubAgentDelegationApprovalRequestValue, error) {
	return types.ParseDelegationApproval(req)
}

// OnApproval completes a tool approval when using [Agent.Stream]. Pass the token from the approval
// event and the chosen status (see streaming examples).
func (a *Agent) OnApproval(ctx context.Context, approvalToken string, status ApprovalStatus) error {
	return a.runtime.Approve(ctx, approvalToken, status)
}
