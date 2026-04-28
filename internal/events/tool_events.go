package events

import "encoding/json"

// TOOL_CALL_START
type AgentToolCallStartEvent struct {
	*BaseEvent
	ToolCallID      string `json:"toolCallId"`
	ToolCallName    string `json:"toolCallName"`
	ParentMessageID string `json:"parentMessageId,omitempty"`
}

func NewAgentToolCallStartEvent(toolCallID, toolCallName string, parentMessageID ...string) *AgentToolCallStartEvent {
	e := &AgentToolCallStartEvent{
		BaseEvent:    NewBaseEvent(AgentEventTypeToolCallStart),
		ToolCallID:   toolCallID,
		ToolCallName: toolCallName,
	}
	if len(parentMessageID) > 0 {
		e.ParentMessageID = parentMessageID[0]
	}
	return e
}

func (e *AgentToolCallStartEvent) ToJSON() ([]byte, error) { return json.Marshal(e) }

// TOOL_CALL_ARGS
type AgentToolCallArgsEvent struct {
	*BaseEvent
	ToolCallID string `json:"toolCallId"`
	Delta      string `json:"delta"`
}

func NewAgentToolCallArgsEvent(toolCallID, delta string) *AgentToolCallArgsEvent {
	return &AgentToolCallArgsEvent{
		BaseEvent:  NewBaseEvent(AgentEventTypeToolCallArgs),
		ToolCallID: toolCallID,
		Delta:      delta,
	}
}

func (e *AgentToolCallArgsEvent) ToJSON() ([]byte, error) { return json.Marshal(e) }

// TOOL_CALL_END
type AgentToolCallEndEvent struct {
	*BaseEvent
	ToolCallID string `json:"toolCallId"`
}

func NewAgentToolCallEndEvent(toolCallID string) *AgentToolCallEndEvent {
	return &AgentToolCallEndEvent{
		BaseEvent:  NewBaseEvent(AgentEventTypeToolCallEnd),
		ToolCallID: toolCallID,
	}
}

func (e *AgentToolCallEndEvent) ToJSON() ([]byte, error) { return json.Marshal(e) }

// TOOL_CALL_RESULT
type AgentToolCallResultEvent struct {
	*BaseEvent
	MessageID  string `json:"messageId"`
	ToolCallID string `json:"toolCallId"`
	Content    string `json:"content"`
	Role       string `json:"role,omitempty"` // tool
}

func NewAgentToolCallResultEvent(messageID, toolCallID, content string, role ...string) *AgentToolCallResultEvent {
	e := &AgentToolCallResultEvent{
		BaseEvent:  NewBaseEvent(AgentEventTypeToolCallResult),
		MessageID:  messageID,
		ToolCallID: toolCallID,
		Content:    content,
	}
	if len(role) > 0 {
		e.Role = role[0]
	}
	return e
}

func (e *AgentToolCallResultEvent) ToJSON() ([]byte, error) { return json.Marshal(e) }
