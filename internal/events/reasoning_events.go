package events

import "encoding/json"

// REASONING_START
type AgentReasoningStartEvent struct {
	*BaseEvent
	MessageID string `json:"messageId"`
}

func NewAgentReasoningStartEvent(messageID string) *AgentReasoningStartEvent {
	return &AgentReasoningStartEvent{
		BaseEvent: NewBaseEvent(AgentEventTypeReasoningStart),
		MessageID: messageID,
	}
}

func (e *AgentReasoningStartEvent) ToJSON() ([]byte, error) { return json.Marshal(e) }

// REASONING_MESSAGE_START
type AgentReasoningMessageStartEvent struct {
	*BaseEvent
	MessageID string `json:"messageId"`
	Role      string `json:"role"` // reasoning
}

func NewAgentReasoningMessageStartEvent(messageID, role string) *AgentReasoningMessageStartEvent {
	return &AgentReasoningMessageStartEvent{
		BaseEvent: NewBaseEvent(AgentEventTypeReasoningMessageStart),
		MessageID: messageID,
		Role:      role,
	}
}

func (e *AgentReasoningMessageStartEvent) ToJSON() ([]byte, error) { return json.Marshal(e) }

// REASONING_MESSAGE_CONTENT
type AgentReasoningMessageContentEvent struct {
	*BaseEvent
	MessageID string `json:"messageId"`
	Delta     string `json:"delta"`
}

func NewAgentReasoningMessageContentEvent(messageID, delta string) *AgentReasoningMessageContentEvent {
	return &AgentReasoningMessageContentEvent{
		BaseEvent: NewBaseEvent(AgentEventTypeReasoningMessageContent),
		MessageID: messageID,
		Delta:     delta,
	}
}

func (e *AgentReasoningMessageContentEvent) ToJSON() ([]byte, error) { return json.Marshal(e) }

// REASONING_MESSAGE_END
type AgentReasoningMessageEndEvent struct {
	*BaseEvent
	MessageID string `json:"messageId"`
}

func NewAgentReasoningMessageEndEvent(messageID string) *AgentReasoningMessageEndEvent {
	return &AgentReasoningMessageEndEvent{
		BaseEvent: NewBaseEvent(AgentEventTypeReasoningMessageEnd),
		MessageID: messageID,
	}
}

func (e *AgentReasoningMessageEndEvent) ToJSON() ([]byte, error) { return json.Marshal(e) }

// REASONING_END
type AgentReasoningEndEvent struct {
	*BaseEvent
	MessageID string `json:"messageId"`
}

func NewAgentReasoningEndEvent(messageID string) *AgentReasoningEndEvent {
	return &AgentReasoningEndEvent{
		BaseEvent: NewBaseEvent(AgentEventTypeReasoningEnd),
		MessageID: messageID,
	}
}

func (e *AgentReasoningEndEvent) ToJSON() ([]byte, error) { return json.Marshal(e) }
