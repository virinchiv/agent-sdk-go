package types

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/agenticenv/agent-sdk-go/internal/events"
)

// maxApprovalTimeout caps how long a single approval wait may last in the run.
const MaxApprovalTimeout = 31 * 24 * time.Hour

// ErrNotApprovalCustomEvent means the CUSTOM event name is not tool or delegation approval.
var ErrNotApprovalCustomEvent = errors.New("types: custom event is not a recognized approval kind")

type ApprovalStatus string

const (
	ApprovalStatusNone     ApprovalStatus = "NONE"
	ApprovalStatusPending  ApprovalStatus = "PENDING"
	ApprovalStatusApproved ApprovalStatus = "APPROVED"
	ApprovalStatusRejected ApprovalStatus = "REJECTED"
	// ApprovalStatusUnavailable means the approval request could not be delivered (e.g. event stream down). It is not a user rejection.
	ApprovalStatusUnavailable ApprovalStatus = "UNAVAILABLE"
)

// ApprovalSender sends an approval result. Call once per request. Safe for concurrent use—
// multiple approvals may be pending when tools run in parallel.
type ApprovalSender func(status ApprovalStatus) error

// ApprovalHandler is called when a tool needs approval (Run with WithApprovalHandler).
// req.Respond is always set; call req.Respond(Approved) or Rejected when ready.
type ApprovalHandler func(ctx context.Context, req *ApprovalRequest)

// ApprovalRequestName classifies the approval payload.
type ApprovalRequestName string

const (
	ApprovalRequestNameTool     ApprovalRequestName = "tool_approval"
	ApprovalRequestNameSubAgent ApprovalRequestName = "sub_agent_delegation"
)

// ApprovalRequest is one pending approval callback. Name + Value match CUSTOM semantics;
// Value is a [ToolApprovalRequestValue] or [SubAgentDelegationApprovalRequestValue].
// Set Respond before invoking the handler (see [PrepareApprovalFromCustomEvent]).
type ApprovalRequest struct {
	Name    ApprovalRequestName `json:"name,omitempty"`
	Value   any                 `json:"value,omitempty"`
	Respond ApprovalSender      `json:"-"`
}

// ToolApprovalRequestValue is the JSON payload for tool approval (same wire shape as CUSTOM approval value).
type ToolApprovalRequestValue struct {
	AgentName       string         `json:"agentName,omitempty"`
	ToolCallID      string         `json:"toolCallId,omitempty"`
	ToolName        string         `json:"toolName"`
	ToolDisplayName string         `json:"toolDisplayName,omitempty"`
	Args            map[string]any `json:"args,omitempty"`
	ApprovalToken   string         `json:"approvalToken,omitempty"`
}

// SubAgentDelegationApprovalRequestValue is the JSON payload for sub-agent delegation approval.
type SubAgentDelegationApprovalRequestValue struct {
	AgentName     string         `json:"agentName,omitempty"`
	SubAgentName  string         `json:"subAgentName,omitempty"`
	Args          map[string]any `json:"args,omitempty"`
	ApprovalToken string         `json:"approvalToken,omitempty"`
}

// ToolApprovalFromEventValue copies the CUSTOM approval payload into an SDK approval value.
func ToolApprovalFromEventValue(ev events.AgentCustomEventApprovalValue) ToolApprovalRequestValue {
	return ToolApprovalRequestValue{
		AgentName:       ev.AgentName,
		ToolCallID:      ev.ToolCallID,
		ToolName:        ev.ToolName,
		ToolDisplayName: ev.ToolDisplayName,
		Args:            cloneArgsMap(ev.Args),
		ApprovalToken:   ev.ApprovalToken,
	}
}

// DelegationApprovalFromEventValue copies the CUSTOM delegation payload into an SDK approval value.
func DelegationApprovalFromEventValue(ev events.AgentCustomEventDelegationValue) SubAgentDelegationApprovalRequestValue {
	return SubAgentDelegationApprovalRequestValue{
		AgentName:     ev.AgentName,
		SubAgentName:  ev.SubAgentName,
		Args:          cloneArgsMap(ev.Args),
		ApprovalToken: ev.ApprovalToken,
	}
}

func cloneArgsMap(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// PrepareApprovalFromCustomEvent parses a CUSTOM event and returns Name + Value as SDK types and the approval token for Temporal CompleteActivity.
// Respond is nil; the caller must set it before calling [ApprovalHandler].
// Returns [ErrNotApprovalCustomEvent] when ev.Name is not tool or delegation approval.
func PrepareApprovalFromCustomEvent(ev *events.AgentCustomEvent) (req *ApprovalRequest, approvalToken string, err error) {
	if ev == nil {
		return nil, "", fmt.Errorf("types: nil custom event")
	}
	switch events.AgentCustomEventName(ev.Name) {
	case events.AgentCustomEventNameToolApproval:
		raw, err := events.ParseCustomEventApproval(ev)
		if err != nil {
			return nil, "", err
		}
		v := ToolApprovalFromEventValue(raw)
		return &ApprovalRequest{
				Name:  ApprovalRequestNameTool,
				Value: v,
			},
			v.ApprovalToken,
			nil
	case events.AgentCustomEventNameSubAgentDelegation:
		raw, err := events.ParseCustomEventDelegation(ev)
		if err != nil {
			return nil, "", err
		}
		v := DelegationApprovalFromEventValue(raw)
		return &ApprovalRequest{
				Name:  ApprovalRequestNameSubAgent,
				Value: v,
			},
			v.ApprovalToken,
			nil
	default:
		return nil, "", ErrNotApprovalCustomEvent
	}
}

func parseApprovalPayload[V any](v any) (out V, err error) {
	if v == nil {
		return out, fmt.Errorf("types: nil approval value")
	}
	switch x := v.(type) {
	case V:
		return x, nil
	case *V:
		if x == nil {
			return out, fmt.Errorf("types: nil approval value pointer")
		}
		return *x, nil
	default:
		raw, mErr := json.Marshal(v)
		if mErr != nil {
			return out, fmt.Errorf("types: marshal approval value: %w", mErr)
		}
		if uErr := json.Unmarshal(raw, &out); uErr != nil {
			return out, fmt.Errorf("types: unmarshal approval value: %w", uErr)
		}
		return out, nil
	}
}

// ParseToolApproval decodes Value for ApprovalRequestNameTool (handles map[string]any from JSON).
func ParseToolApproval(req *ApprovalRequest) (ToolApprovalRequestValue, error) {
	if req == nil {
		return ToolApprovalRequestValue{}, errors.New("types: nil approval request")
	}
	if req.Name != ApprovalRequestNameTool {
		return ToolApprovalRequestValue{}, errors.New("types: not a tool approval request")
	}
	if req.Value == nil {
		return ToolApprovalRequestValue{}, errors.New("types: tool approval request has empty value")
	}
	return parseApprovalPayload[ToolApprovalRequestValue](req.Value)
}

// ParseDelegationApproval decodes Value for ApprovalRequestNameSubAgent.
func ParseDelegationApproval(req *ApprovalRequest) (SubAgentDelegationApprovalRequestValue, error) {
	if req == nil {
		return SubAgentDelegationApprovalRequestValue{}, errors.New("types: nil approval request")
	}
	if req.Name != ApprovalRequestNameSubAgent {
		return SubAgentDelegationApprovalRequestValue{}, errors.New("types: not a sub-agent delegation approval request")
	}
	if req.Value == nil {
		return SubAgentDelegationApprovalRequestValue{}, errors.New("types: delegation approval request has empty value")
	}
	return parseApprovalPayload[SubAgentDelegationApprovalRequestValue](req.Value)
}
