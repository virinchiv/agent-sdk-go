package events

import "encoding/json"

// TEXT_MESSAGE_START
type AgentTextMessageStartEvent struct {
	*BaseEvent
	MessageID string `json:"messageId"`
	Role      string `json:"role"` // assistant
}

func NewAgentTextMessageStartEvent(messageID, role string) *AgentTextMessageStartEvent {
	return &AgentTextMessageStartEvent{
		BaseEvent: NewBaseEvent(AgentEventTypeTextMessageStart),
		MessageID: messageID,
		Role:      role,
	}
}

func (e *AgentTextMessageStartEvent) ToJSON() ([]byte, error) { return json.Marshal(e) }

// TEXT_MESSAGE_CONTENT
type AgentTextMessageContentEvent struct {
	*BaseEvent
	MessageID string `json:"messageId"`
	Delta     string `json:"delta"`
}

func NewAgentTextMessageContentEvent(messageID, delta string) *AgentTextMessageContentEvent {
	return &AgentTextMessageContentEvent{
		BaseEvent: NewBaseEvent(AgentEventTypeTextMessageContent),
		MessageID: messageID,
		Delta:     delta,
	}
}

func (e *AgentTextMessageContentEvent) ToJSON() ([]byte, error) { return json.Marshal(e) }

// TEXT_MESSAGE_END
type AgentTextMessageEndEvent struct {
	*BaseEvent
	MessageID string `json:"messageId"`
}

func NewAgentTextMessageEndEvent(messageID string) *AgentTextMessageEndEvent {
	return &AgentTextMessageEndEvent{
		BaseEvent: NewBaseEvent(AgentEventTypeTextMessageEnd),
		MessageID: messageID,
	}
}

func (e *AgentTextMessageEndEvent) ToJSON() ([]byte, error) { return json.Marshal(e) }
