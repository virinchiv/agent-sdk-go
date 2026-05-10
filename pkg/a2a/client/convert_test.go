package client

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

// ---------------------------------------------------------------------------
// AgentCard / Skill
// ---------------------------------------------------------------------------

func TestFromSDKAgentCard_Full(t *testing.T) {
	card := &a2a.AgentCard{
		Name:               "TestAgent",
		Description:        "does stuff",
		Version:            "2.0",
		DocumentationURL:   "https://docs.example.com",
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"application/json"},
		SupportedInterfaces: []*a2a.AgentInterface{
			{URL: "https://agent.example.com/api"},
		},
		Skills: []a2a.AgentSkill{
			{
				ID:          "skill-1",
				Name:        "Skill One",
				Description: "does thing one",
				Tags:        []string{"tag-a"},
				InputModes:  []string{"text/plain"},
				OutputModes: []string{"text/plain"},
				Examples:    []string{"example query"},
			},
		},
	}

	got := fromSDKAgentCard(card, "https://fallback.example.com")

	if got.Name != "TestAgent" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.Description != "does stuff" {
		t.Errorf("Description = %q", got.Description)
	}
	if got.Version != "2.0" {
		t.Errorf("Version = %q", got.Version)
	}
	if got.DocumentationURL != "https://docs.example.com" {
		t.Errorf("DocumentationURL = %q", got.DocumentationURL)
	}
	// URL should come from first SupportedInterface, not base URL
	if got.URL != "https://agent.example.com/api" {
		t.Errorf("URL = %q, want interface URL", got.URL)
	}
	if len(got.InputModes) != 1 || got.InputModes[0] != "text/plain" {
		t.Errorf("InputModes = %v", got.InputModes)
	}
	if len(got.Skills) != 1 {
		t.Fatalf("Skills count = %d", len(got.Skills))
	}
	s := got.Skills[0]
	if s.ID != "skill-1" || s.Name != "Skill One" || s.Description != "does thing one" {
		t.Errorf("Skill = %+v", s)
	}
	if len(s.Tags) != 1 || s.Tags[0] != "tag-a" {
		t.Errorf("Tags = %v", s.Tags)
	}
	if len(s.Examples) != 1 || s.Examples[0] != "example query" {
		t.Errorf("Examples = %v", s.Examples)
	}
}

func TestFromSDKAgentCard_FallsBackToBaseURL(t *testing.T) {
	card := &a2a.AgentCard{Name: "A"}
	got := fromSDKAgentCard(card, "https://base.example.com")
	if got.URL != "https://base.example.com" {
		t.Errorf("URL = %q, want base URL", got.URL)
	}
}

func TestFromSDKAgentCard_EmptyInterfaceURL_FallsBackToBaseURL(t *testing.T) {
	card := &a2a.AgentCard{
		Name:                "A",
		SupportedInterfaces: []*a2a.AgentInterface{{URL: ""}},
	}
	got := fromSDKAgentCard(card, "https://base.example.com")
	if got.URL != "https://base.example.com" {
		t.Errorf("URL = %q, want base URL", got.URL)
	}
}

func TestFromSDKAgentCard_NoSkills(t *testing.T) {
	card := &a2a.AgentCard{Name: "B"}
	got := fromSDKAgentCard(card, "https://x.com")
	if len(got.Skills) != 0 {
		t.Errorf("expected no skills, got %d", len(got.Skills))
	}
}

// ---------------------------------------------------------------------------
// Part conversions (toSDKPart / fromSDKPart)
// ---------------------------------------------------------------------------

func TestToSDKPart_Text(t *testing.T) {
	p := toSDKPart(interfaces.A2APart{Kind: "text", Text: "hello world"})
	if p == nil {
		t.Fatal("nil part")
	}
	if got := p.Text(); got != "hello world" {
		t.Errorf("Text = %q", got)
	}
}

func TestToSDKPart_TextDefault_UnknownKind(t *testing.T) {
	p := toSDKPart(interfaces.A2APart{Kind: "unknown", Text: "fallback"})
	if p.Text() != "fallback" {
		t.Errorf("fallback text = %q", p.Text())
	}
}

func TestToSDKPart_Data(t *testing.T) {
	raw := json.RawMessage(`{"x":1}`)
	p := toSDKPart(interfaces.A2APart{Kind: "data", Data: raw})
	if p == nil {
		t.Fatal("nil part")
	}
	if p.Data() == nil {
		t.Error("expected data content")
	}
}

func TestToSDKPart_FileURI(t *testing.T) {
	p := toSDKPart(interfaces.A2APart{
		Kind:         "file",
		FileURI:      "https://files.example.com/doc.pdf",
		FileMIMEType: "application/pdf",
	})
	if p == nil {
		t.Fatal("nil part")
	}
	if p.URL() != "https://files.example.com/doc.pdf" {
		t.Errorf("URL = %q", p.URL())
	}
	if p.MediaType != "application/pdf" {
		t.Errorf("MediaType = %q", p.MediaType)
	}
}

func TestToSDKPart_FileBytes(t *testing.T) {
	rawBytes := []byte("binary data")
	encoded := base64.StdEncoding.EncodeToString(rawBytes)
	p := toSDKPart(interfaces.A2APart{
		Kind:         "file",
		FileBytes:    encoded,
		FileMIMEType: "image/png",
	})
	if p == nil {
		t.Fatal("nil part")
	}
	got := p.Raw()
	if string(got) != "binary data" {
		t.Errorf("Raw = %q", got)
	}
	if p.MediaType != "image/png" {
		t.Errorf("MediaType = %q", p.MediaType)
	}
}

func TestToSDKPart_FileBytes_InvalidBase64_FallsBackToText(t *testing.T) {
	p := toSDKPart(interfaces.A2APart{
		Kind:      "file",
		FileBytes: "!!!not-base64!!!",
		Text:      "fallback",
	})
	if p.Text() != "fallback" {
		t.Errorf("expected text fallback, got %q", p.Text())
	}
}

func TestToSDKPart_KindCaseInsensitive(t *testing.T) {
	p := toSDKPart(interfaces.A2APart{Kind: "TEXT", Text: "hi"})
	if p.Text() != "hi" {
		t.Errorf("case-insensitive kind failed: %q", p.Text())
	}
}

func TestFromSDKPart_nil(t *testing.T) {
	got := fromSDKPart(nil)
	if got.Kind != "text" {
		t.Errorf("Kind = %q, want text", got.Kind)
	}
}

func TestFromSDKPart_Text(t *testing.T) {
	p := a2a.NewTextPart("hello")
	got := fromSDKPart(p)
	if got.Kind != "text" {
		t.Errorf("Kind = %q", got.Kind)
	}
	if got.Text != "hello" {
		t.Errorf("Text = %q", got.Text)
	}
}

func TestFromSDKPart_Raw(t *testing.T) {
	rawBytes := []byte("raw content")
	p := a2a.NewRawPart(rawBytes)
	p.MediaType = "image/jpeg"
	got := fromSDKPart(p)
	if got.Kind != "file" {
		t.Errorf("Kind = %q, want file", got.Kind)
	}
	decoded, err := base64.StdEncoding.DecodeString(got.FileBytes)
	if err != nil {
		t.Fatalf("base64 decode: %v", err)
	}
	if string(decoded) != "raw content" {
		t.Errorf("FileBytes = %q", decoded)
	}
	if got.FileMIMEType != "image/jpeg" {
		t.Errorf("FileMIMEType = %q", got.FileMIMEType)
	}
}

func TestFromSDKPart_Data(t *testing.T) {
	p := a2a.NewDataPart(map[string]any{"key": "val"})
	got := fromSDKPart(p)
	if got.Kind != "data" {
		t.Errorf("Kind = %q, want data", got.Kind)
	}
	if len(got.Data) == 0 {
		t.Error("expected non-empty Data")
	}
	var m map[string]any
	if err := json.Unmarshal(got.Data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if m["key"] != "val" {
		t.Errorf("data key = %v", m["key"])
	}
}

func TestFromSDKPart_URL(t *testing.T) {
	p := a2a.NewFileURLPart("https://cdn.example.com/file.mp4", "video/mp4")
	got := fromSDKPart(p)
	if got.Kind != "file" {
		t.Errorf("Kind = %q, want file", got.Kind)
	}
	if got.FileURI != "https://cdn.example.com/file.mp4" {
		t.Errorf("FileURI = %q", got.FileURI)
	}
	if got.FileMIMEType != "video/mp4" {
		t.Errorf("FileMIMEType = %q", got.FileMIMEType)
	}
}

// ---------------------------------------------------------------------------
// Message conversions (toSDKMessage / fromSDKMessage)
// ---------------------------------------------------------------------------

func TestToSDKMessage_UserRole(t *testing.T) {
	m := toSDKMessage(interfaces.A2AMessage{
		Role:  "user",
		Parts: []interfaces.A2APart{{Kind: "text", Text: "hi"}},
	})
	if m.Role != a2a.MessageRoleUser {
		t.Errorf("Role = %v", m.Role)
	}
}

func TestToSDKMessage_AgentRole(t *testing.T) {
	m := toSDKMessage(interfaces.A2AMessage{Role: "agent"})
	if m.Role != a2a.MessageRoleAgent {
		t.Errorf("Role = %v", m.Role)
	}
}

func TestToSDKMessage_RoleCaseInsensitive(t *testing.T) {
	m := toSDKMessage(interfaces.A2AMessage{Role: "USER"})
	if m.Role != a2a.MessageRoleUser {
		t.Errorf("Role = %v", m.Role)
	}
}

func TestToSDKMessage_PreservesCustomMessageID(t *testing.T) {
	m := toSDKMessage(interfaces.A2AMessage{
		MessageID: "custom-id-123",
		Role:      "user",
	})
	if m.ID != "custom-id-123" {
		t.Errorf("ID = %q", m.ID)
	}
}

func TestToSDKMessage_EmptyMessageID_GeneratesOne(t *testing.T) {
	m := toSDKMessage(interfaces.A2AMessage{Role: "user"})
	if m.ID == "" {
		t.Error("expected auto-generated ID")
	}
}

func TestFromSDKMessage_nil(t *testing.T) {
	got := fromSDKMessage(nil)
	if got.MessageID != "" || got.Role != "" {
		t.Errorf("expected zero A2AMessage, got %+v", got)
	}
}

func TestFromSDKMessage_AgentRole(t *testing.T) {
	sdk := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("reply"))
	got := fromSDKMessage(sdk)
	if got.Role != "agent" {
		t.Errorf("Role = %q, want agent", got.Role)
	}
}

func TestFromSDKMessage_UserRole(t *testing.T) {
	sdk := a2a.NewMessage(a2a.MessageRoleUser, a2a.NewTextPart("query"))
	got := fromSDKMessage(sdk)
	if got.Role != "user" {
		t.Errorf("Role = %q, want user", got.Role)
	}
}

func TestFromSDKMessage_Parts(t *testing.T) {
	sdk := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("p1"), a2a.NewTextPart("p2"))
	got := fromSDKMessage(sdk)
	if len(got.Parts) != 2 {
		t.Errorf("Parts count = %d, want 2", len(got.Parts))
	}
}

func TestFromSDKMessage_NilPartsSkipped(t *testing.T) {
	sdk := &a2a.Message{
		ID:    "x",
		Role:  a2a.MessageRoleAgent,
		Parts: []*a2a.Part{a2a.NewTextPart("ok"), nil},
	}
	got := fromSDKMessage(sdk)
	if len(got.Parts) != 1 {
		t.Errorf("Parts = %d, nil not skipped", len(got.Parts))
	}
}

// ---------------------------------------------------------------------------
// SendMessageRequest conversion
// ---------------------------------------------------------------------------

func TestToSDKSendMessageRequest_Basic(t *testing.T) {
	req := interfaces.A2ASendMessageRequest{
		Message: interfaces.A2AMessage{
			Role:  "user",
			Parts: []interfaces.A2APart{{Kind: "text", Text: "hello"}},
		},
	}
	got := toSDKSendMessageRequest(req)
	if got.Message == nil {
		t.Fatal("nil message")
	}
	if got.Config != nil {
		t.Error("Config should be nil when AcceptedOutputModes empty")
	}
}

func TestToSDKSendMessageRequest_WithTaskID(t *testing.T) {
	req := interfaces.A2ASendMessageRequest{
		Message: interfaces.A2AMessage{Role: "user"},
		TaskID:  "task-abc",
	}
	got := toSDKSendMessageRequest(req)
	if string(got.Message.TaskID) != "task-abc" {
		t.Errorf("TaskID = %q", got.Message.TaskID)
	}
}

func TestToSDKSendMessageRequest_WithSessionID(t *testing.T) {
	req := interfaces.A2ASendMessageRequest{
		Message:   interfaces.A2AMessage{Role: "user"},
		SessionID: "ctx-xyz",
	}
	got := toSDKSendMessageRequest(req)
	if got.Message.ContextID != "ctx-xyz" {
		t.Errorf("ContextID = %q", got.Message.ContextID)
	}
}

func TestToSDKSendMessageRequest_WithAcceptedOutputModes(t *testing.T) {
	req := interfaces.A2ASendMessageRequest{
		Message:             interfaces.A2AMessage{Role: "user"},
		AcceptedOutputModes: []string{"text/plain", "application/json"},
	}
	got := toSDKSendMessageRequest(req)
	if got.Config == nil {
		t.Fatal("expected non-nil Config")
	}
	if len(got.Config.AcceptedOutputModes) != 2 {
		t.Errorf("AcceptedOutputModes = %v", got.Config.AcceptedOutputModes)
	}
}

// ---------------------------------------------------------------------------
// SendMessageResult conversion
// ---------------------------------------------------------------------------

func TestFromSDKSendMessageResult_Message(t *testing.T) {
	sdk := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("the answer"))
	got := fromSDKSendMessageResult(sdk)
	if got.Message == nil {
		t.Fatal("expected non-nil Message")
	}
	if got.Task != nil {
		t.Error("expected nil Task")
	}
	if len(got.Message.Parts) != 1 || got.Message.Parts[0].Text != "the answer" {
		t.Errorf("Parts = %+v", got.Message.Parts)
	}
}

func TestFromSDKSendMessageResult_Task(t *testing.T) {
	sdk := &a2a.Task{
		ID:        "t1",
		ContextID: "ctx1",
		Status:    a2a.TaskStatus{State: a2a.TaskStateCompleted},
	}
	got := fromSDKSendMessageResult(sdk)
	if got.Task == nil {
		t.Fatal("expected non-nil Task")
	}
	if got.Message != nil {
		t.Error("expected nil Message")
	}
	if got.Task.ID != "t1" {
		t.Errorf("Task.ID = %q", got.Task.ID)
	}
}

// ---------------------------------------------------------------------------
// Task conversion
// ---------------------------------------------------------------------------

func TestFromSDKTask_nil(t *testing.T) {
	got := fromSDKTask(nil)
	if got.ID != "" {
		t.Errorf("expected zero task, got %+v", got)
	}
}

func TestFromSDKTask_Basic(t *testing.T) {
	sdk := &a2a.Task{
		ID:        "task-1",
		ContextID: "ctx-1",
		Status:    a2a.TaskStatus{State: a2a.TaskStateWorking},
	}
	got := fromSDKTask(sdk)
	if got.ID != "task-1" {
		t.Errorf("ID = %q", got.ID)
	}
	if got.SessionID != "ctx-1" {
		t.Errorf("SessionID = %q", got.SessionID)
	}
	if got.Status != interfaces.A2ATaskStatusWorking {
		t.Errorf("Status = %q", got.Status)
	}
	if got.Message != nil {
		t.Error("expected nil Message")
	}
}

func TestFromSDKTask_WithStatusMessage(t *testing.T) {
	msg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("status info"))
	sdk := &a2a.Task{
		ID:     "task-2",
		Status: a2a.TaskStatus{State: a2a.TaskStateFailed, Message: msg},
	}
	got := fromSDKTask(sdk)
	if got.Message == nil {
		t.Fatal("expected non-nil Message in task")
	}
	if len(got.Message.Parts) != 1 || got.Message.Parts[0].Text != "status info" {
		t.Errorf("Parts = %+v", got.Message.Parts)
	}
}

func TestFromSDKTask_WithArtifact(t *testing.T) {
	sdk := &a2a.Task{
		ID:     "task-3",
		Status: a2a.TaskStatus{State: a2a.TaskStateCompleted},
		Artifacts: []*a2a.Artifact{
			{
				ID:    "art-1",
				Name:  "output.txt",
				Parts: []*a2a.Part{a2a.NewTextPart("result")},
			},
			nil, // nil artifacts should be skipped
		},
	}
	got := fromSDKTask(sdk)
	if len(got.Artifacts) != 1 {
		t.Fatalf("Artifacts = %d, want 1", len(got.Artifacts))
	}
	art := got.Artifacts[0]
	if art.ArtifactID != "art-1" {
		t.Errorf("ArtifactID = %q", art.ArtifactID)
	}
	if art.Name != "output.txt" {
		t.Errorf("Name = %q", art.Name)
	}
	if len(art.Parts) != 1 || art.Parts[0].Text != "result" {
		t.Errorf("Parts = %+v", art.Parts)
	}
}

// ---------------------------------------------------------------------------
// TaskState conversion
// ---------------------------------------------------------------------------

func TestFromSDKTaskState_AllStates(t *testing.T) {
	cases := []struct {
		in   a2a.TaskState
		want interfaces.A2ATaskStatus
	}{
		{a2a.TaskStateSubmitted, interfaces.A2ATaskStatusSubmitted},
		{a2a.TaskStateWorking, interfaces.A2ATaskStatusWorking},
		{a2a.TaskStateInputRequired, interfaces.A2ATaskStatusInputRequired},
		{a2a.TaskStateCompleted, interfaces.A2ATaskStatusCompleted},
		{a2a.TaskStateCanceled, interfaces.A2ATaskStatusCanceled},
		{a2a.TaskStateFailed, interfaces.A2ATaskStatusFailed},
		{a2a.TaskStateRejected, interfaces.A2ATaskStatusRejected},
		{a2a.TaskStateAuthRequired, interfaces.A2ATaskStatusAuthRequired},
		{a2a.TaskState("TASK_STATE_MYSTERY"), interfaces.A2ATaskStatusUnknown},
	}
	for _, tc := range cases {
		got := fromSDKTaskState(tc.in)
		if got != tc.want {
			t.Errorf("fromSDKTaskState(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Streaming event conversion
// ---------------------------------------------------------------------------

func TestFromSDKEvent_nil(t *testing.T) {
	got := fromSDKEvent(nil)
	if got.Kind != "" {
		t.Errorf("expected empty event for nil, got Kind=%q", got.Kind)
	}
}

func TestFromSDKEvent_Message(t *testing.T) {
	sdk := &a2a.Message{
		ID:     "msg-1",
		TaskID: "task-x",
		Role:   a2a.MessageRoleAgent,
		Parts:  []*a2a.Part{a2a.NewTextPart("chunk")},
	}
	got := fromSDKEvent(sdk)
	if got.Kind != "message" {
		t.Errorf("Kind = %q, want message", got.Kind)
	}
	if got.TaskID != "task-x" {
		t.Errorf("TaskID = %q", got.TaskID)
	}
	if got.Message == nil {
		t.Fatal("expected non-nil Message")
	}
	if len(got.Message.Parts) != 1 || got.Message.Parts[0].Text != "chunk" {
		t.Errorf("Message parts = %+v", got.Message.Parts)
	}
}

func TestFromSDKEvent_Task_Terminal(t *testing.T) {
	sdk := &a2a.Task{
		ID:        "task-y",
		ContextID: "ctx-y",
		Status:    a2a.TaskStatus{State: a2a.TaskStateCompleted},
	}
	got := fromSDKEvent(sdk)
	if got.Kind != "taskStatus" {
		t.Errorf("Kind = %q, want taskStatus", got.Kind)
	}
	if got.TaskID != "task-y" {
		t.Errorf("TaskID = %q", got.TaskID)
	}
	if !got.IsFinal {
		t.Error("IsFinal should be true for terminal state")
	}
	if got.TaskStatus == nil {
		t.Fatal("expected non-nil TaskStatus")
	}
	if got.TaskStatus.Status != interfaces.A2ATaskStatusCompleted {
		t.Errorf("Status = %q", got.TaskStatus.Status)
	}
}

func TestFromSDKEvent_Task_NonTerminal(t *testing.T) {
	sdk := &a2a.Task{
		ID:     "task-z",
		Status: a2a.TaskStatus{State: a2a.TaskStateWorking},
	}
	got := fromSDKEvent(sdk)
	if got.IsFinal {
		t.Error("IsFinal should be false for non-terminal state")
	}
}

func TestFromSDKEvent_TaskStatusUpdate_Terminal(t *testing.T) {
	sdk := &a2a.TaskStatusUpdateEvent{
		TaskID:    "task-a",
		ContextID: "ctx-a",
		Status:    a2a.TaskStatus{State: a2a.TaskStateCanceled},
	}
	got := fromSDKEvent(sdk)
	if got.Kind != "taskStatus" {
		t.Errorf("Kind = %q, want taskStatus", got.Kind)
	}
	if got.TaskID != "task-a" {
		t.Errorf("TaskID = %q", got.TaskID)
	}
	if !got.IsFinal {
		t.Error("IsFinal should be true for canceled state")
	}
	if got.TaskStatus.Status != interfaces.A2ATaskStatusCanceled {
		t.Errorf("Status = %q", got.TaskStatus.Status)
	}
}

func TestFromSDKEvent_TaskStatusUpdate_WithMessage(t *testing.T) {
	msg := a2a.NewMessage(a2a.MessageRoleAgent, a2a.NewTextPart("progress"))
	sdk := &a2a.TaskStatusUpdateEvent{
		TaskID: "task-b",
		Status: a2a.TaskStatus{State: a2a.TaskStateWorking, Message: msg},
	}
	got := fromSDKEvent(sdk)
	if got.TaskStatus == nil || got.TaskStatus.Message == nil {
		t.Fatal("expected TaskStatus.Message")
	}
	if !strings.Contains(got.TaskStatus.Message.Parts[0].Text, "progress") {
		t.Errorf("message text = %q", got.TaskStatus.Message.Parts[0].Text)
	}
}

func TestFromSDKEvent_ArtifactUpdate_LastChunk(t *testing.T) {
	sdk := &a2a.TaskArtifactUpdateEvent{
		TaskID:    "task-c",
		ContextID: "ctx-c",
		LastChunk: true,
		Artifact: &a2a.Artifact{
			ID:    "art-x",
			Name:  "report.pdf",
			Parts: []*a2a.Part{a2a.NewRawPart([]byte("pdf bytes"))},
		},
	}
	got := fromSDKEvent(sdk)
	if got.Kind != "artifact" {
		t.Errorf("Kind = %q, want artifact", got.Kind)
	}
	if got.TaskID != "task-c" {
		t.Errorf("TaskID = %q", got.TaskID)
	}
	if !got.IsFinal {
		t.Error("IsFinal should be true for LastChunk")
	}
	if got.Artifact == nil {
		t.Fatal("expected non-nil Artifact")
	}
	if got.Artifact.ArtifactID != "art-x" {
		t.Errorf("ArtifactID = %q", got.Artifact.ArtifactID)
	}
	if got.Artifact.Name != "report.pdf" {
		t.Errorf("Name = %q", got.Artifact.Name)
	}
}

func TestFromSDKEvent_ArtifactUpdate_NotLastChunk(t *testing.T) {
	sdk := &a2a.TaskArtifactUpdateEvent{
		TaskID:    "task-d",
		LastChunk: false,
		Artifact:  &a2a.Artifact{ID: "art-y"},
	}
	got := fromSDKEvent(sdk)
	if got.IsFinal {
		t.Error("IsFinal should be false when LastChunk is false")
	}
}

// ---------------------------------------------------------------------------
// Round-trip: toSDKPart → fromSDKPart
// ---------------------------------------------------------------------------

func TestRoundTrip_TextPart(t *testing.T) {
	orig := interfaces.A2APart{Kind: "text", Text: "round trip me"}
	got := fromSDKPart(toSDKPart(orig))
	if got.Kind != "text" || got.Text != "round trip me" {
		t.Errorf("round-trip = %+v", got)
	}
}

func TestRoundTrip_DataPart(t *testing.T) {
	orig := interfaces.A2APart{Kind: "data", Data: json.RawMessage(`{"n":42}`)}
	sdkPart := toSDKPart(orig)
	got := fromSDKPart(sdkPart)
	if got.Kind != "data" {
		t.Errorf("Kind = %q", got.Kind)
	}
	var m map[string]any
	if err := json.Unmarshal(got.Data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	// JSON numbers unmarshal as float64
	if m["n"] != float64(42) {
		t.Errorf("n = %v", m["n"])
	}
}

func TestRoundTrip_FileBytesPart(t *testing.T) {
	rawBytes := []byte("test binary content")
	encoded := base64.StdEncoding.EncodeToString(rawBytes)
	orig := interfaces.A2APart{
		Kind:         "file",
		FileBytes:    encoded,
		FileMIMEType: "application/octet-stream",
	}
	got := fromSDKPart(toSDKPart(orig))
	if got.Kind != "file" {
		t.Errorf("Kind = %q", got.Kind)
	}
	decoded, err := base64.StdEncoding.DecodeString(got.FileBytes)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if string(decoded) != "test binary content" {
		t.Errorf("FileBytes = %q", decoded)
	}
}

func TestRoundTrip_FileURIPart(t *testing.T) {
	orig := interfaces.A2APart{
		Kind:         "file",
		FileURI:      "https://example.com/img.jpg",
		FileMIMEType: "image/jpeg",
	}
	got := fromSDKPart(toSDKPart(orig))
	if got.Kind != "file" {
		t.Errorf("Kind = %q", got.Kind)
	}
	if got.FileURI != "https://example.com/img.jpg" {
		t.Errorf("FileURI = %q", got.FileURI)
	}
	if got.FileMIMEType != "image/jpeg" {
		t.Errorf("FileMIMEType = %q", got.FileMIMEType)
	}
}
