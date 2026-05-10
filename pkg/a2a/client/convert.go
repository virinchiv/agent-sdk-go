package client

import (
	"encoding/base64"
	"encoding/json"
	"strings"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

// ---------------------------------------------------------------------------
// AgentCard / Skill
// ---------------------------------------------------------------------------

func fromSDKAgentCard(c *a2a.AgentCard, baseURL string) interfaces.A2AAgentCard {
	skills := make([]interfaces.A2ASkillSpec, 0, len(c.Skills))
	for _, s := range c.Skills {
		skills = append(skills, fromSDKSkill(s))
	}
	// Prefer the first declared interface URL; fall back to the base URL used for resolution.
	url := baseURL
	if len(c.SupportedInterfaces) > 0 && c.SupportedInterfaces[0] != nil {
		if u := c.SupportedInterfaces[0].URL; u != "" {
			url = u
		}
	}
	return interfaces.A2AAgentCard{
		Name:             c.Name,
		Description:      c.Description,
		Version:          c.Version,
		URL:              url,
		DocumentationURL: c.DocumentationURL,
		Skills:           skills,
		InputModes:       c.DefaultInputModes,
		OutputModes:      c.DefaultOutputModes,
	}
}

func fromSDKSkill(s a2a.AgentSkill) interfaces.A2ASkillSpec {
	return interfaces.A2ASkillSpec{
		ID:          s.ID,
		Name:        s.Name,
		Description: s.Description,
		Tags:        s.Tags,
		InputModes:  s.InputModes,
		OutputModes: s.OutputModes,
		Examples:    s.Examples,
	}
}

// ---------------------------------------------------------------------------
// Request conversion: interfaces → SDK
// ---------------------------------------------------------------------------

func toSDKSendMessageRequest(req interfaces.A2ASendMessageRequest) *a2a.SendMessageRequest {
	sdkMsg := toSDKMessage(req.Message)
	if req.TaskID != "" {
		sdkMsg.TaskID = a2a.TaskID(req.TaskID)
	}
	if req.SessionID != "" {
		sdkMsg.ContextID = req.SessionID
	}
	sdkReq := &a2a.SendMessageRequest{Message: sdkMsg}
	if len(req.AcceptedOutputModes) > 0 {
		sdkReq.Config = &a2a.SendMessageConfig{
			AcceptedOutputModes: req.AcceptedOutputModes,
		}
	}
	return sdkReq
}

func toSDKMessage(m interfaces.A2AMessage) *a2a.Message {
	role := a2a.MessageRoleAgent
	if strings.EqualFold(m.Role, "user") {
		role = a2a.MessageRoleUser
	}
	parts := make([]*a2a.Part, 0, len(m.Parts))
	for _, p := range m.Parts {
		parts = append(parts, toSDKPart(p))
	}
	msg := a2a.NewMessage(role, parts...)
	if m.MessageID != "" {
		msg.ID = m.MessageID
	}
	return msg
}

func toSDKPart(p interfaces.A2APart) *a2a.Part {
	switch strings.ToLower(strings.TrimSpace(p.Kind)) {
	case "data":
		return a2a.NewDataPart(json.RawMessage(p.Data))
	case "file":
		if p.FileURI != "" {
			part := a2a.NewFileURLPart(a2a.URL(p.FileURI), p.FileMIMEType)
			return part
		}
		if p.FileBytes != "" {
			if raw, err := base64.StdEncoding.DecodeString(p.FileBytes); err == nil {
				part := a2a.NewRawPart(raw)
				part.MediaType = p.FileMIMEType
				return part
			}
		}
	}
	return a2a.NewTextPart(p.Text)
}

// ---------------------------------------------------------------------------
// Result conversion: SDK → interfaces
// ---------------------------------------------------------------------------

func fromSDKSendMessageResult(res a2a.SendMessageResult) interfaces.A2ASendMessageResult {
	switch v := res.(type) {
	case *a2a.Message:
		m := fromSDKMessage(v)
		return interfaces.A2ASendMessageResult{Message: &m}
	case *a2a.Task:
		t := fromSDKTask(v)
		return interfaces.A2ASendMessageResult{Task: &t}
	}
	return interfaces.A2ASendMessageResult{}
}

func fromSDKMessage(m *a2a.Message) interfaces.A2AMessage {
	if m == nil {
		return interfaces.A2AMessage{}
	}
	role := "agent"
	if m.Role == a2a.MessageRoleUser {
		role = "user"
	}
	parts := make([]interfaces.A2APart, 0, len(m.Parts))
	for _, p := range m.Parts {
		if p != nil {
			parts = append(parts, fromSDKPart(p))
		}
	}
	return interfaces.A2AMessage{
		MessageID: m.ID,
		Role:      role,
		Parts:     parts,
	}
}

func fromSDKPart(p *a2a.Part) interfaces.A2APart {
	if p == nil {
		return interfaces.A2APart{Kind: "text"}
	}
	switch v := p.Content.(type) {
	case a2a.Text:
		return interfaces.A2APart{Kind: "text", Text: string(v)}
	case a2a.Raw:
		return interfaces.A2APart{
			Kind:         "file",
			FileBytes:    base64.StdEncoding.EncodeToString([]byte(v)),
			FileMIMEType: p.MediaType,
		}
	case a2a.Data:
		b, _ := json.Marshal(v.Value)
		return interfaces.A2APart{Kind: "data", Data: json.RawMessage(b)}
	case a2a.URL:
		return interfaces.A2APart{
			Kind:         "file",
			FileURI:      string(v),
			FileMIMEType: p.MediaType,
		}
	}
	return interfaces.A2APart{Kind: "text"}
}

func fromSDKTask(t *a2a.Task) interfaces.A2ATask {
	if t == nil {
		return interfaces.A2ATask{}
	}
	var msg *interfaces.A2AMessage
	if t.Status.Message != nil {
		m := fromSDKMessage(t.Status.Message)
		msg = &m
	}
	artifacts := make([]interfaces.A2AArtifact, 0, len(t.Artifacts))
	for _, a := range t.Artifacts {
		if a != nil {
			artifacts = append(artifacts, fromSDKArtifact(a))
		}
	}
	return interfaces.A2ATask{
		ID:        string(t.ID),
		SessionID: t.ContextID,
		Status:    fromSDKTaskState(t.Status.State),
		Message:   msg,
		Artifacts: artifacts,
	}
}

func fromSDKArtifact(a *a2a.Artifact) interfaces.A2AArtifact {
	parts := make([]interfaces.A2APart, 0, len(a.Parts))
	for _, p := range a.Parts {
		if p != nil {
			parts = append(parts, fromSDKPart(p))
		}
	}
	return interfaces.A2AArtifact{
		ArtifactID: string(a.ID),
		Name:       a.Name,
		Parts:      parts,
	}
}

func fromSDKTaskState(s a2a.TaskState) interfaces.A2ATaskStatus {
	switch s {
	case a2a.TaskStateSubmitted:
		return interfaces.A2ATaskStatusSubmitted
	case a2a.TaskStateWorking:
		return interfaces.A2ATaskStatusWorking
	case a2a.TaskStateInputRequired:
		return interfaces.A2ATaskStatusInputRequired
	case a2a.TaskStateCompleted:
		return interfaces.A2ATaskStatusCompleted
	case a2a.TaskStateCanceled:
		return interfaces.A2ATaskStatusCanceled
	case a2a.TaskStateFailed:
		return interfaces.A2ATaskStatusFailed
	case a2a.TaskStateRejected:
		return interfaces.A2ATaskStatusRejected
	case a2a.TaskStateAuthRequired:
		return interfaces.A2ATaskStatusAuthRequired
	default:
		return interfaces.A2ATaskStatusUnknown
	}
}

// ---------------------------------------------------------------------------
// Streaming event conversion
// ---------------------------------------------------------------------------

func fromSDKEvent(ev a2a.Event) interfaces.A2AStreamEvent {
	if ev == nil {
		return interfaces.A2AStreamEvent{}
	}
	switch v := ev.(type) {
	case *a2a.Message:
		m := fromSDKMessage(v)
		return interfaces.A2AStreamEvent{
			Kind:    "message",
			TaskID:  string(v.TaskID),
			Message: &m,
		}
	case *a2a.Task:
		t := fromSDKTask(v)
		isFinal := v.Status.State.Terminal()
		statusUpdate := &interfaces.A2ATaskStatusUpdate{
			Status: fromSDKTaskState(v.Status.State),
		}
		if v.Status.Message != nil {
			m := fromSDKMessage(v.Status.Message)
			statusUpdate.Message = &m
		}
		return interfaces.A2AStreamEvent{
			Kind:       "taskStatus",
			TaskID:     t.ID,
			IsFinal:    isFinal,
			TaskStatus: statusUpdate,
		}
	case *a2a.TaskStatusUpdateEvent:
		isFinal := v.Status.State.Terminal()
		update := &interfaces.A2ATaskStatusUpdate{
			Status: fromSDKTaskState(v.Status.State),
		}
		if v.Status.Message != nil {
			m := fromSDKMessage(v.Status.Message)
			update.Message = &m
		}
		return interfaces.A2AStreamEvent{
			Kind:       "taskStatus",
			TaskID:     string(v.TaskID),
			IsFinal:    isFinal,
			TaskStatus: update,
		}
	case *a2a.TaskArtifactUpdateEvent:
		art := fromSDKArtifact(v.Artifact)
		return interfaces.A2AStreamEvent{
			Kind:     "artifact",
			TaskID:   string(v.TaskID),
			IsFinal:  v.LastChunk,
			Artifact: &art,
		}
	}
	return interfaces.A2AStreamEvent{}
}
