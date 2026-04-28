package events

import "encoding/json"

// RUN_STARTED
type AgentRunStartedEvent struct {
	*BaseEvent
	ThreadID    string `json:"threadId"`
	RunID       string `json:"runId"`
	ParentRunID string `json:"parentRunId,omitempty"`
}

func NewAgentRunStartedEvent(threadID, runID string, parentRunID ...string) *AgentRunStartedEvent {
	e := &AgentRunStartedEvent{
		BaseEvent: NewBaseEvent(AgentEventTypeRunStarted),
		ThreadID:  threadID,
		RunID:     runID,
	}
	if len(parentRunID) > 0 {
		e.ParentRunID = parentRunID[0]
	}
	return e
}

func (e *AgentRunStartedEvent) ToJSON() ([]byte, error) { return json.Marshal(e) }

// RUN_FINISHED
type AgentRunFinishedEvent struct {
	*BaseEvent
	ThreadID string `json:"threadId"`
	RunID    string `json:"runId"`
	Result   any    `json:"result,omitempty"` // ← any not json.RawMessage
}

func NewAgentRunFinishedEvent(threadID, runID string, result any) *AgentRunFinishedEvent {
	return &AgentRunFinishedEvent{
		BaseEvent: NewBaseEvent(AgentEventTypeRunFinished),
		ThreadID:  threadID,
		RunID:     runID,
		Result:    result,
	}
}

func (e *AgentRunFinishedEvent) ToJSON() ([]byte, error) { return json.Marshal(e) }

// RUN_ERROR
type AgentRunErrorEvent struct {
	*BaseEvent
	Message string  `json:"message"`
	Code    *string `json:"code,omitempty"` // ← *string not string
}

func NewAgentRunErrorEvent(message string, code ...string) *AgentRunErrorEvent {
	e := &AgentRunErrorEvent{
		BaseEvent: NewBaseEvent(AgentEventTypeRunError),
		Message:   message,
	}
	if len(code) > 0 && code[0] != "" {
		e.Code = &code[0]
	}
	return e
}

func (e *AgentRunErrorEvent) ToJSON() ([]byte, error) { return json.Marshal(e) }

// STEP_STARTED
type AgentStepStartedEvent struct {
	*BaseEvent
	StepName string `json:"stepName"`
}

func NewAgentStepStartedEvent(stepName string) *AgentStepStartedEvent {
	return &AgentStepStartedEvent{
		BaseEvent: NewBaseEvent(AgentEventTypeStepStarted),
		StepName:  stepName,
	}
}

func (e *AgentStepStartedEvent) ToJSON() ([]byte, error) { return json.Marshal(e) }

// STEP_FINISHED
type AgentStepFinishedEvent struct {
	*BaseEvent
	StepName string `json:"stepName"`
}

func NewAgentStepFinishedEvent(stepName string) *AgentStepFinishedEvent {
	return &AgentStepFinishedEvent{
		BaseEvent: NewBaseEvent(AgentEventTypeStepFinished),
		StepName:  stepName,
	}
}

func (e *AgentStepFinishedEvent) ToJSON() ([]byte, error) { return json.Marshal(e) }
