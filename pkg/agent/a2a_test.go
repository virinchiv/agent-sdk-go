package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

// ---------------------------------------------------------------------------
// A2ATool helpers: tool name / display name
// ---------------------------------------------------------------------------

func TestA2AToolName(t *testing.T) {
	if got := a2aToolName("  remote  ", "  search  "); got != "a2a_remote_search" {
		t.Fatalf("got %q", got)
	}
	if a2aToolName("", "x") != "" {
		t.Fatal("expected empty for missing server name")
	}
	if a2aToolName("x", "") != "" {
		t.Fatal("expected empty for missing skill ID")
	}
	if a2aToolName(" ", "y") != "" {
		t.Fatal("expected empty for whitespace server name")
	}
}

func TestA2AToolName_ViaStruct(t *testing.T) {
	tool := NewA2ATool("  srv  ", interfaces.ToolSpec{Name: "  tid  ", Description: "d"}, interfaces.A2ASkillSpec{}, nil)
	want := "a2a_srv_tid"
	if tool.Name() != want {
		t.Fatalf("A2ATool.Name = %q, want %q", tool.Name(), want)
	}
}

func TestA2AToolDisplayName(t *testing.T) {
	if got := a2aToolDisplayName("myserver", "myskill"); got != "myserver A2A myskill Tool" {
		t.Fatalf("got %q", got)
	}
	if a2aToolDisplayName("", "s") != "" || a2aToolDisplayName("s", "") != "" {
		t.Fatal("expected empty for missing parts")
	}
}

// ---------------------------------------------------------------------------
// A2ATool: Name / DisplayName / Description / Parameters (nil safety)
// ---------------------------------------------------------------------------

func TestA2ATool_NilReceiver(t *testing.T) {
	var m *A2ATool
	if m.Name() != "" {
		t.Fatal("nil Name should be empty")
	}
	if m.DisplayName() != "" {
		t.Fatal("nil DisplayName should be empty")
	}
	if m.Description() != "" {
		t.Fatal("nil Description should be empty")
	}
	p := m.Parameters()
	if p["type"] != "object" {
		t.Fatalf("nil Parameters should be default object schema, got %v", p)
	}
}

func TestA2ATool_Parameters_NilSpec(t *testing.T) {
	tool := NewA2ATool("s", interfaces.ToolSpec{Name: "t", Parameters: nil}, interfaces.A2ASkillSpec{}, nil)
	p := tool.Parameters()
	if p["type"] != "object" {
		t.Fatalf("expected default schema, got %v", p)
	}
}

func TestA2ATool_Parameters_CustomSpec(t *testing.T) {
	schema := interfaces.JSONSchema{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}}
	tool := NewA2ATool("s", interfaces.ToolSpec{Name: "t", Parameters: schema}, interfaces.A2ASkillSpec{}, nil)
	p := tool.Parameters()
	if _, ok := p["properties"]; !ok {
		t.Fatalf("expected custom schema to be returned, got %v", p)
	}
}

func TestA2ATool_Description(t *testing.T) {
	tool := NewA2ATool("s", interfaces.ToolSpec{Name: "t", Description: "does stuff"}, interfaces.A2ASkillSpec{}, nil)
	if tool.Description() != "does stuff" {
		t.Fatalf("got %q", tool.Description())
	}
}

// ---------------------------------------------------------------------------
// A2ATool.Execute stubs
// ---------------------------------------------------------------------------

// a2aMessageStub returns a canned A2AMessage reply.
type a2aMessageStub struct {
	reply interfaces.A2AMessage
	err   error
}

func (s a2aMessageStub) Name() string { return "stub" }
func (s a2aMessageStub) Ping(_ context.Context) error {
	return nil
}
func (s a2aMessageStub) ResolveCard(_ context.Context) (interfaces.A2AAgentCard, error) {
	return interfaces.A2AAgentCard{}, nil
}
func (s a2aMessageStub) ListSkills(_ context.Context) ([]interfaces.A2ASkillSpec, error) {
	return nil, nil
}
func (s a2aMessageStub) SendMessage(_ context.Context, _ interfaces.A2ASendMessageRequest) (interfaces.A2ASendMessageResult, error) {
	if s.err != nil {
		return interfaces.A2ASendMessageResult{}, s.err
	}
	return interfaces.A2ASendMessageResult{Message: &s.reply}, nil
}
func (s a2aMessageStub) Close() error { return nil }

// a2aTaskStub returns a canned A2ATask reply.
type a2aTaskStub struct {
	task interfaces.A2ATask
}

func (s a2aTaskStub) Name() string { return "taskstub" }
func (s a2aTaskStub) Ping(_ context.Context) error {
	return nil
}
func (s a2aTaskStub) ResolveCard(_ context.Context) (interfaces.A2AAgentCard, error) {
	return interfaces.A2AAgentCard{}, nil
}
func (s a2aTaskStub) ListSkills(_ context.Context) ([]interfaces.A2ASkillSpec, error) {
	return nil, nil
}
func (s a2aTaskStub) SendMessage(_ context.Context, _ interfaces.A2ASendMessageRequest) (interfaces.A2ASendMessageResult, error) {
	return interfaces.A2ASendMessageResult{Task: &s.task}, nil
}
func (s a2aTaskStub) Close() error { return nil }

// a2aEmptyStub returns neither a message nor a task.
type a2aEmptyStub struct{}

func (a2aEmptyStub) Name() string { return "empty" }
func (a2aEmptyStub) Ping(_ context.Context) error {
	return nil
}
func (a2aEmptyStub) ResolveCard(_ context.Context) (interfaces.A2AAgentCard, error) {
	return interfaces.A2AAgentCard{}, nil
}
func (a2aEmptyStub) ListSkills(_ context.Context) ([]interfaces.A2ASkillSpec, error) {
	return nil, nil
}
func (a2aEmptyStub) SendMessage(_ context.Context, _ interfaces.A2ASendMessageRequest) (interfaces.A2ASendMessageResult, error) {
	return interfaces.A2ASendMessageResult{}, nil
}
func (a2aEmptyStub) Close() error { return nil }

// ---------------------------------------------------------------------------
// A2ATool.Execute
// ---------------------------------------------------------------------------

func TestA2ATool_Execute_NilClient(t *testing.T) {
	tool := &A2ATool{ServerName: "s", Spec: interfaces.ToolSpec{Name: "t"}, Client: nil}
	_, err := tool.Execute(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "nil client") {
		t.Fatalf("got %v", err)
	}
}

func TestA2ATool_Execute_NilReceiver(t *testing.T) {
	var m *A2ATool
	_, err := m.Execute(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "nil client") {
		t.Fatalf("got %v", err)
	}
}

func TestA2ATool_Execute_MessageResult_SinglePart(t *testing.T) {
	stub := a2aMessageStub{
		reply: interfaces.A2AMessage{
			Role:  "agent",
			Parts: []interfaces.A2APart{{Kind: "text", Text: "hello world"}},
		},
	}
	tool := NewA2ATool("s", interfaces.ToolSpec{Name: "t"}, interfaces.A2ASkillSpec{}, stub)
	out, err := tool.Execute(context.Background(), map[string]any{"q": "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello world" {
		t.Fatalf("got %q", out)
	}
}

func TestA2ATool_Execute_MessageResult_MultiPart(t *testing.T) {
	stub := a2aMessageStub{
		reply: interfaces.A2AMessage{
			Role: "agent",
			Parts: []interfaces.A2APart{
				{Kind: "text", Text: "part one"},
				{Kind: "file", FileURI: "https://x.com/f"}, // non-text, skipped
				{Kind: "text", Text: "part two"},
			},
		},
	}
	tool := NewA2ATool("s", interfaces.ToolSpec{Name: "t"}, interfaces.A2ASkillSpec{}, stub)
	out, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if out != "part one\npart two" {
		t.Fatalf("got %q", out)
	}
}

func TestA2ATool_Execute_MessageResult_EmptyParts(t *testing.T) {
	stub := a2aMessageStub{
		reply: interfaces.A2AMessage{Role: "agent", Parts: []interfaces.A2APart{}},
	}
	tool := NewA2ATool("s", interfaces.ToolSpec{Name: "t"}, interfaces.A2ASkillSpec{}, stub)
	out, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Fatalf("expected empty string, got %q", out)
	}
}

func TestA2ATool_Execute_TaskResult(t *testing.T) {
	stub := a2aTaskStub{
		task: interfaces.A2ATask{
			ID:     "task-abc",
			Status: interfaces.A2ATaskStatusCompleted,
		},
	}
	tool := NewA2ATool("s", interfaces.ToolSpec{Name: "t"}, interfaces.A2ASkillSpec{}, stub)
	out, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	s, ok := out.(string)
	if !ok || !strings.Contains(s, "task-abc") {
		t.Fatalf("task result should contain task ID; got %#v", out)
	}
}

func TestA2ATool_Execute_TaskResult_IsValidJSON(t *testing.T) {
	stub := a2aTaskStub{
		task: interfaces.A2ATask{
			ID:     "task-xyz",
			Status: interfaces.A2ATaskStatusWorking,
		},
	}
	tool := NewA2ATool("s", interfaces.ToolSpec{Name: "t"}, interfaces.A2ASkillSpec{}, stub)
	out, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	s, ok := out.(string)
	if !ok {
		t.Fatalf("expected string, got %T", out)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("task result not valid JSON: %v — got %q", err, s)
	}
}

func TestA2ATool_Execute_EmptyResult(t *testing.T) {
	tool := NewA2ATool("s", interfaces.ToolSpec{Name: "t"}, interfaces.A2ASkillSpec{}, a2aEmptyStub{})
	out, err := tool.Execute(context.Background(), map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	if out != "" {
		t.Fatalf("expected empty string, got %#v", out)
	}
}

func TestA2ATool_Execute_ClientError(t *testing.T) {
	stub := a2aMessageStub{err: errors.New("network error")}
	tool := NewA2ATool("s", interfaces.ToolSpec{Name: "t"}, interfaces.A2ASkillSpec{}, stub)
	_, err := tool.Execute(context.Background(), map[string]any{})
	if err == nil || !strings.Contains(err.Error(), "network error") {
		t.Fatalf("got %v", err)
	}
}

func TestA2ATool_Execute_SendsArgsAsJSONText(t *testing.T) {
	var capturedText string
	// Use a closure-capturing stub via a custom interface impl.
	capturing := &capturingA2AClient{captureText: &capturedText}
	tool := NewA2ATool("s", interfaces.ToolSpec{Name: "t"}, interfaces.A2ASkillSpec{}, capturing)
	args := map[string]any{"key": "value", "n": 42}
	_, _ = tool.Execute(context.Background(), args)

	var decoded map[string]any
	if err := json.Unmarshal([]byte(capturedText), &decoded); err != nil {
		t.Fatalf("captured text is not JSON: %v — %q", err, capturedText)
	}
	if decoded["key"] != "value" {
		t.Errorf("key = %v", decoded["key"])
	}
	if decoded["n"] != float64(42) {
		t.Errorf("n = %v", decoded["n"])
	}
}

type capturingA2AClient struct {
	captureText *string
}

func (c *capturingA2AClient) Name() string { return "cap" }
func (c *capturingA2AClient) Ping(_ context.Context) error {
	return nil
}
func (c *capturingA2AClient) ResolveCard(_ context.Context) (interfaces.A2AAgentCard, error) {
	return interfaces.A2AAgentCard{}, nil
}
func (c *capturingA2AClient) ListSkills(_ context.Context) ([]interfaces.A2ASkillSpec, error) {
	return nil, nil
}
func (c *capturingA2AClient) SendMessage(_ context.Context, req interfaces.A2ASendMessageRequest) (interfaces.A2ASendMessageResult, error) {
	if len(req.Message.Parts) > 0 {
		*c.captureText = req.Message.Parts[0].Text
	}
	return interfaces.A2ASendMessageResult{}, nil
}
func (c *capturingA2AClient) Close() error { return nil }

// ---------------------------------------------------------------------------
// a2aCollectText
// ---------------------------------------------------------------------------

func TestA2ACollectText_nil(t *testing.T) {
	if got := a2aCollectText(nil); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestA2ACollectText_empty(t *testing.T) {
	if got := a2aCollectText(&interfaces.A2AMessage{}); got != "" {
		t.Fatalf("got %q", got)
	}
}

func TestA2ACollectText_skipsNonText(t *testing.T) {
	m := &interfaces.A2AMessage{
		Parts: []interfaces.A2APart{
			{Kind: "data", Data: []byte(`{"x":1}`)},
			{Kind: "text", Text: "hello"},
			{Kind: "file", FileURI: "https://x.com/f"},
		},
	}
	if got := a2aCollectText(m); got != "hello" {
		t.Fatalf("got %q", got)
	}
}

func TestA2ACollectText_skipsEmptyText(t *testing.T) {
	m := &interfaces.A2AMessage{
		Parts: []interfaces.A2APart{
			{Kind: "text", Text: ""},
			{Kind: "text", Text: "real"},
		},
	}
	if got := a2aCollectText(m); got != "real" {
		t.Fatalf("got %q", got)
	}
}

func TestA2ACollectText_multipleTextParts(t *testing.T) {
	m := &interfaces.A2AMessage{
		Parts: []interfaces.A2APart{
			{Kind: "text", Text: "line1"},
			{Kind: "text", Text: "line2"},
			{Kind: "text", Text: "line3"},
		},
	}
	if got := a2aCollectText(m); got != "line1\nline2\nline3" {
		t.Fatalf("got %q", got)
	}
}

// ---------------------------------------------------------------------------
// A2ASkillFilter.Apply
// ---------------------------------------------------------------------------

func TestA2ASkillFilter_Apply_NoFilter(t *testing.T) {
	skills := []interfaces.A2ASkillSpec{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	got := types.A2ASkillFilter{}.Apply(skills)
	if len(got) != 3 {
		t.Fatalf("expected 3, got %d", len(got))
	}
}

func TestA2ASkillFilter_Apply_AllowList(t *testing.T) {
	skills := []interfaces.A2ASkillSpec{{ID: "alpha"}, {ID: "beta"}, {ID: "gamma"}}
	f := types.A2ASkillFilter{AllowSkills: []string{"alpha", "gamma"}}
	got := f.Apply(skills)
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	if got[0].ID != "alpha" || got[1].ID != "gamma" {
		t.Fatalf("got %v", got)
	}
}

func TestA2ASkillFilter_Apply_BlockList(t *testing.T) {
	skills := []interfaces.A2ASkillSpec{{ID: "alpha"}, {ID: "beta"}, {ID: "gamma"}}
	f := types.A2ASkillFilter{BlockSkills: []string{"beta"}}
	got := f.Apply(skills)
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d", len(got))
	}
	ids := []string{got[0].ID, got[1].ID}
	for _, id := range ids {
		if id == "beta" {
			t.Fatal("beta should be blocked")
		}
	}
}

func TestA2ASkillFilter_Apply_AllowList_TrimSpace(t *testing.T) {
	skills := []interfaces.A2ASkillSpec{{ID: "alpha"}}
	f := types.A2ASkillFilter{AllowSkills: []string{"  alpha  "}}
	got := f.Apply(skills)
	if len(got) != 1 || got[0].ID != "alpha" {
		t.Fatalf("got %v", got)
	}
}

func TestA2ASkillFilter_Apply_Empty_Skills(t *testing.T) {
	f := types.A2ASkillFilter{AllowSkills: []string{"alpha"}}
	got := f.Apply(nil)
	if len(got) != 0 {
		t.Fatalf("expected empty, got %d", len(got))
	}
}

// ---------------------------------------------------------------------------
// a2aConfigFingerprint
// ---------------------------------------------------------------------------

func TestA2AConfigFingerprint_Empty(t *testing.T) {
	if got := a2aConfigFingerprint(nil, nil); got != "" {
		t.Fatalf("expected empty, got %q", got)
	}
	if got := a2aConfigFingerprint(A2AServers{}, nil); got != "" {
		t.Fatalf("expected empty for empty map, got %q", got)
	}
}

func TestA2AConfigFingerprint_DifferentURL(t *testing.T) {
	a := A2AServers{"agent": A2AConfig{URL: "https://a.example/a2a"}}
	b := A2AServers{"agent": A2AConfig{URL: "https://b.example/a2a"}}
	gotA := a2aConfigFingerprint(a, nil)
	gotB := a2aConfigFingerprint(b, nil)
	if gotA == gotB || gotA == "" || gotB == "" {
		t.Fatalf("expected distinct non-empty fingerprints: %q vs %q", gotA, gotB)
	}
}

func TestA2AConfigFingerprint_DifferentKey(t *testing.T) {
	a := A2AServers{"coder": A2AConfig{URL: "https://x.example/a2a"}}
	b := A2AServers{"planner": A2AConfig{URL: "https://x.example/a2a"}}
	if a2aConfigFingerprint(a, nil) == a2aConfigFingerprint(b, nil) {
		t.Fatal("expected different fingerprints when server key differs")
	}
}

func TestA2AConfigFingerprint_TimeoutChange(t *testing.T) {
	a := A2AServers{"agent": A2AConfig{URL: "https://x.example", Timeout: 10 * time.Second}}
	b := A2AServers{"agent": A2AConfig{URL: "https://x.example", Timeout: 20 * time.Second}}
	if a2aConfigFingerprint(a, nil) == a2aConfigFingerprint(b, nil) {
		t.Fatal("expected different fingerprints when Timeout differs")
	}
}

func TestA2AConfigFingerprint_ZeroTimeout_UsesDefault(t *testing.T) {
	// Zero timeout → defaultA2AToolTimeout; this should produce the same fingerprint
	// as explicitly setting the default value.
	zero := A2AServers{"agent": A2AConfig{URL: "https://x.example", Timeout: 0}}
	explicit := A2AServers{"agent": A2AConfig{URL: "https://x.example", Timeout: defaultA2AToolTimeout}}
	if a2aConfigFingerprint(zero, nil) != a2aConfigFingerprint(explicit, nil) {
		t.Fatal("zero timeout should fingerprint the same as the default value")
	}
}

func TestA2AConfigFingerprint_TokenPresence(t *testing.T) {
	withToken := A2AServers{"agent": A2AConfig{URL: "https://x.example", Token: "secret"}}
	withoutToken := A2AServers{"agent": A2AConfig{URL: "https://x.example"}}
	if a2aConfigFingerprint(withToken, nil) == a2aConfigFingerprint(withoutToken, nil) {
		t.Fatal("expected different fingerprints when token presence differs")
	}
}

func TestA2AConfigFingerprint_TokenValueIgnored(t *testing.T) {
	// Different token values but both non-empty → same fingerprint (values are not hashed).
	a := A2AServers{"agent": A2AConfig{URL: "https://x.example", Token: "token-A"}}
	b := A2AServers{"agent": A2AConfig{URL: "https://x.example", Token: "token-B"}}
	if a2aConfigFingerprint(a, nil) != a2aConfigFingerprint(b, nil) {
		t.Fatal("token value should not affect fingerprint (only presence matters)")
	}
}

func TestA2AConfigFingerprint_HeaderKeys(t *testing.T) {
	withHdr := A2AServers{"agent": A2AConfig{
		URL:     "https://x.example",
		Headers: map[string]string{"X-Tenant": "acme"},
	}}
	withoutHdr := A2AServers{"agent": A2AConfig{URL: "https://x.example"}}
	if a2aConfigFingerprint(withHdr, nil) == a2aConfigFingerprint(withoutHdr, nil) {
		t.Fatal("expected different fingerprints when header keys differ")
	}
}

func TestA2AConfigFingerprint_HeaderValueIgnored(t *testing.T) {
	// Different values, same key → same fingerprint.
	a := A2AServers{"agent": A2AConfig{URL: "https://x.example", Headers: map[string]string{"X-Key": "v1"}}}
	b := A2AServers{"agent": A2AConfig{URL: "https://x.example", Headers: map[string]string{"X-Key": "v2"}}}
	if a2aConfigFingerprint(a, nil) != a2aConfigFingerprint(b, nil) {
		t.Fatal("header values should not affect fingerprint")
	}
}

func TestA2AConfigFingerprint_SkipTLSVerify(t *testing.T) {
	on := A2AServers{"agent": A2AConfig{URL: "https://x.example", SkipTLSVerify: true}}
	off := A2AServers{"agent": A2AConfig{URL: "https://x.example", SkipTLSVerify: false}}
	if a2aConfigFingerprint(on, nil) == a2aConfigFingerprint(off, nil) {
		t.Fatal("expected different fingerprints when SkipTLSVerify differs")
	}
}

func TestA2AConfigFingerprint_SkillFilter(t *testing.T) {
	allow := A2AServers{"agent": A2AConfig{URL: "https://x.example", SkillFilter: types.A2ASkillFilter{AllowSkills: []string{"foo"}}}}
	block := A2AServers{"agent": A2AConfig{URL: "https://x.example", SkillFilter: types.A2ASkillFilter{BlockSkills: []string{"bar"}}}}
	noFilter := A2AServers{"agent": A2AConfig{URL: "https://x.example"}}

	fpAllow := a2aConfigFingerprint(allow, nil)
	fpBlock := a2aConfigFingerprint(block, nil)
	fpNone := a2aConfigFingerprint(noFilter, nil)

	if fpAllow == fpNone || fpBlock == fpNone || fpAllow == fpBlock {
		t.Fatalf("expected three distinct fingerprints: allow=%q block=%q none=%q", fpAllow, fpBlock, fpNone)
	}
}

func TestA2AConfigFingerprint_ExtraClientNames(t *testing.T) {
	gotA := a2aConfigFingerprint(nil, []string{"alpha", "beta"})
	gotB := a2aConfigFingerprint(nil, []string{"gamma"})
	if gotA == gotB || gotA == "" {
		t.Fatalf("expected distinct non-empty fingerprints: %q vs %q", gotA, gotB)
	}
}

func TestA2AConfigFingerprint_ExtraClientNames_Sorted(t *testing.T) {
	// Order of extra client names should not matter.
	a := a2aConfigFingerprint(nil, []string{"alpha", "beta", "gamma"})
	b := a2aConfigFingerprint(nil, []string{"gamma", "alpha", "beta"})
	if a != b {
		t.Fatalf("extra client name order should not affect fingerprint: %q vs %q", a, b)
	}
}

func TestA2AConfigFingerprint_ServerKeyOrder_Stable(t *testing.T) {
	// Adding servers in different map-iteration orders should yield the same fingerprint.
	servers := A2AServers{
		"zz": A2AConfig{URL: "https://z.example"},
		"aa": A2AConfig{URL: "https://a.example"},
		"mm": A2AConfig{URL: "https://m.example"},
	}
	fp1 := a2aConfigFingerprint(servers, nil)
	fp2 := a2aConfigFingerprint(servers, nil)
	if fp1 != fp2 {
		t.Fatal("fingerprint is not deterministic across calls")
	}
}

func TestA2AConfigFingerprint_MultipleServers(t *testing.T) {
	one := A2AServers{"a": A2AConfig{URL: "https://a.example"}}
	two := A2AServers{
		"a": A2AConfig{URL: "https://a.example"},
		"b": A2AConfig{URL: "https://b.example"},
	}
	if a2aConfigFingerprint(one, nil) == a2aConfigFingerprint(two, nil) {
		t.Fatal("expected different fingerprints for different server counts")
	}
}

func TestA2AConfigFingerprint_NonEmpty_ReturnsHex(t *testing.T) {
	fp := a2aConfigFingerprint(A2AServers{"s": A2AConfig{URL: "https://x.example"}}, nil)
	if fp == "" {
		t.Fatal("expected non-empty fingerprint")
	}
	if len(fp) != 64 {
		t.Fatalf("expected 64-char hex SHA-256, got len=%d: %q", len(fp), fp)
	}
}

// ---------------------------------------------------------------------------
// a2aExtraClientNames
// ---------------------------------------------------------------------------

func TestA2AExtraClientNames_Empty(t *testing.T) {
	if got := a2aExtraClientNames(nil); len(got) != 0 {
		t.Fatalf("got %v", got)
	}
}

func TestA2AExtraClientNames_SkipsNil(t *testing.T) {
	got := a2aExtraClientNames([]interfaces.A2AClient{nil})
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

func TestA2AExtraClientNames_SkipsEmptyName(t *testing.T) {
	got := a2aExtraClientNames([]interfaces.A2AClient{a2aNamedStub("")})
	if len(got) != 0 {
		t.Fatalf("expected empty, got %v", got)
	}
}

func TestA2AExtraClientNames_Sorted(t *testing.T) {
	clients := []interfaces.A2AClient{
		a2aNamedStub("gamma"),
		a2aNamedStub("alpha"),
		a2aNamedStub("beta"),
	}
	got := a2aExtraClientNames(clients)
	if len(got) != 3 || got[0] != "alpha" || got[1] != "beta" || got[2] != "gamma" {
		t.Fatalf("got %v", got)
	}
}

func TestA2AExtraClientNames_DeduplicatesViaSort(t *testing.T) {
	// Two clients with the same name are unusual but the function should at least
	// return them (dedup is the caller's concern; fingerprint will still differ if names differ).
	clients := []interfaces.A2AClient{a2aNamedStub("x"), a2aNamedStub("x")}
	got := a2aExtraClientNames(clients)
	if len(got) != 2 {
		t.Fatalf("expected both entries, got %v", got)
	}
}

// a2aNamedStub is a minimal A2AClient whose Name() returns the given string.
type a2aNamedStub string

func (s a2aNamedStub) Name() string { return string(s) }
func (s a2aNamedStub) Ping(_ context.Context) error {
	return nil
}
func (s a2aNamedStub) ResolveCard(_ context.Context) (interfaces.A2AAgentCard, error) {
	return interfaces.A2AAgentCard{}, nil
}
func (s a2aNamedStub) ListSkills(_ context.Context) ([]interfaces.A2ASkillSpec, error) {
	return nil, nil
}
func (s a2aNamedStub) SendMessage(_ context.Context, _ interfaces.A2ASendMessageRequest) (interfaces.A2ASendMessageResult, error) {
	return interfaces.A2ASendMessageResult{}, nil
}
func (s a2aNamedStub) Close() error { return nil }

// Compile-time check that stubs satisfy the interface.
var (
	_ interfaces.A2AClient = a2aMessageStub{}
	_ interfaces.A2AClient = a2aTaskStub{}
	_ interfaces.A2AClient = a2aEmptyStub{}
	_ interfaces.A2AClient = (*capturingA2AClient)(nil)
	_ interfaces.A2AClient = a2aNamedStub("")
)

// verify A2ATool satisfies interfaces.Tool at compile time (already in a2a.go, belt-and-suspenders).
var _ interfaces.Tool = (*A2ATool)(nil)

// ---------------------------------------------------------------------------
// Formatting helpers (verify that fmt is imported — used in stubs above)
// ---------------------------------------------------------------------------

var _ = fmt.Sprintf // prevent "imported and not used" if stub formatting is optimized away
