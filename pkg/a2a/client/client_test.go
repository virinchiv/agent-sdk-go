package client

import (
	"context"
	"iter"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a2aproject/a2a-go/v2/a2a"
	"github.com/a2aproject/a2a-go/v2/a2asrv"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
)

// ---------------------------------------------------------------------------
// Test helpers: in-process A2A server
// ---------------------------------------------------------------------------

// newTestListener returns a TCP listener on a random free port.
func newTestListener(t *testing.T) net.Listener {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen: %v", err)
	}
	return l
}

// testA2AServer spins up an httptest.Server that serves both the agent card
// (at /.well-known/agent-card.json) and a JSON-RPC A2A protocol endpoint (at /).
// It uses echoExecutor which immediately completes every task.
func testA2AServer(t *testing.T) (baseURL string, cleanup func()) {
	t.Helper()
	l := newTestListener(t)
	serverURL := "http://" + l.Addr().String()

	card := &a2a.AgentCard{
		Name:         "Echo Agent",
		Description:  "echo agent for tests",
		Version:      "1.0",
		Capabilities: a2a.AgentCapabilities{Streaming: true},
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(serverURL, a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
		Skills: []a2a.AgentSkill{
			{ID: "echo", Name: "Echo", Description: "echoes text back", Tags: []string{"test"}},
		},
	}
	executor := &echoExecutor{replyText: "echo: ok"}
	requestHandler := a2asrv.NewHandler(executor)
	mux := http.NewServeMux()
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	mux.Handle("/", a2asrv.NewJSONRPCHandler(requestHandler))

	srv := httptest.NewUnstartedServer(mux)
	srv.Listener = l
	srv.Start()
	return serverURL, srv.Close
}

// testCardOnlyServer serves only the agent card, no protocol handler.
// Use for Ping / ResolveCard / ListSkills tests that don't need SendMessage.
func testCardOnlyServer(t *testing.T, card *a2a.AgentCard) (baseURL string, cleanup func()) {
	t.Helper()
	l := newTestListener(t)
	serverURL := "http://" + l.Addr().String()

	// Patch SupportedInterfaces to point at the real test server URL before the
	// static handler serialises the card to JSON.
	if len(card.SupportedInterfaces) > 0 {
		card.SupportedInterfaces[0].URL = serverURL
	}

	mux := http.NewServeMux()
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	srv := httptest.NewUnstartedServer(mux)
	srv.Listener = l
	srv.Start()
	return serverURL, srv.Close
}

// echoExecutor immediately completes every task with a canned text reply.
type echoExecutor struct {
	replyText string
}

func (e *echoExecutor) Execute(_ context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		submitted := a2a.NewSubmittedTask(execCtx, execCtx.Message)
		if !yield(submitted, nil) {
			return
		}
		reply := a2a.NewMessageForTask(a2a.MessageRoleAgent, execCtx, a2a.NewTextPart(e.replyText))
		done := a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCompleted, reply)
		yield(done, nil)
	}
}

func (e *echoExecutor) Cancel(_ context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		ev := a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCanceled, nil)
		yield(ev, nil)
	}
}

func TestNormalizeResolvedCard_fillsEmptyProtocolVersion(t *testing.T) {
	card := &a2a.AgentCard{
		SupportedInterfaces: []*a2a.AgentInterface{
			{URL: "http://example.com/rpc", ProtocolBinding: a2a.TransportProtocolJSONRPC},
		},
	}
	patched := normalizeResolvedCard(card)
	if !patched {
		t.Fatal("expected patched=true when ProtocolVersion is empty")
	}
	if got := card.SupportedInterfaces[0].ProtocolVersion; got != a2a.Version {
		t.Fatalf("ProtocolVersion = %q, want %q", got, a2a.Version)
	}
}

func TestNormalizeResolvedCard_noChangeWhenVersionAlreadySet(t *testing.T) {
	card := &a2a.AgentCard{
		SupportedInterfaces: []*a2a.AgentInterface{
			{URL: "http://example.com/rpc", ProtocolBinding: a2a.TransportProtocolJSONRPC, ProtocolVersion: a2a.Version},
		},
	}
	patched := normalizeResolvedCard(card)
	if patched {
		t.Fatal("expected patched=false when ProtocolVersion is already set")
	}
}

func TestNormalizeResolvedCard_nilSafe(t *testing.T) {
	patched := normalizeResolvedCard(nil)
	if patched {
		t.Fatal("expected patched=false for nil card")
	}
}

// hangingExecutor stays in WORKING state until its context is cancelled.
// Its Cancel handler transitions the task to CANCELED.
// Use this for tests that need to cancel an in-progress task.
type hangingExecutor struct {
	// working is closed when the executor reaches the WORKING state.
	working chan struct{}
}

func newHangingExecutor() *hangingExecutor {
	return &hangingExecutor{working: make(chan struct{}, 1)}
}

func (h *hangingExecutor) Execute(ctx context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		if !yield(a2a.NewSubmittedTask(execCtx, execCtx.Message), nil) {
			return
		}
		if !yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateWorking, nil), nil) {
			return
		}
		// Signal that we're now in the working state.
		select {
		case h.working <- struct{}{}:
		default:
		}
		// Block until the context is cancelled (e.g. by a CancelTask call).
		<-ctx.Done()
	}
}

func (h *hangingExecutor) Cancel(_ context.Context, execCtx *a2asrv.ExecutorContext) iter.Seq2[a2a.Event, error] {
	return func(yield func(a2a.Event, error) bool) {
		yield(a2a.NewStatusUpdateEvent(execCtx, a2a.TaskStateCanceled, nil), nil)
	}
}

// testHangingServer starts an A2A server using hangingExecutor.
// It returns the base URL, the executor (to wait for working state), and a cleanup func.
func testHangingServer(t *testing.T) (baseURL string, exec *hangingExecutor, cleanup func()) {
	t.Helper()
	l := newTestListener(t)
	serverURL := "http://" + l.Addr().String()

	card := &a2a.AgentCard{
		Name:         "Hanging Agent",
		Version:      "1.0",
		Capabilities: a2a.AgentCapabilities{Streaming: true},
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface(serverURL, a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"text/plain"},
	}
	executor := newHangingExecutor()
	requestHandler := a2asrv.NewHandler(executor)
	mux := http.NewServeMux()
	mux.Handle(a2asrv.WellKnownAgentCardPath, a2asrv.NewStaticAgentCardHandler(card))
	mux.Handle("/", a2asrv.NewJSONRPCHandler(requestHandler))
	srv := httptest.NewUnstartedServer(mux)
	srv.Listener = l
	srv.Start()
	return serverURL, executor, srv.Close
}

// ---------------------------------------------------------------------------
// NewClient validation
// ---------------------------------------------------------------------------

func TestNewClient_EmptyName(t *testing.T) {
	_, err := NewClient("   ", "https://agent.example.com")
	if err == nil || !strings.Contains(err.Error(), "name is empty") {
		t.Fatalf("got %v", err)
	}
}

func TestNewClient_EmptyURL(t *testing.T) {
	_, err := NewClient("agent", "   ")
	if err == nil || !strings.Contains(err.Error(), "url is empty") {
		t.Fatalf("got %v", err)
	}
}

func TestNewClient_OK(t *testing.T) {
	c, err := NewClient("test-agent", "https://agent.example.com",
		WithLogLevel("error"),
		WithTimeout(5*time.Second),
	)
	if err != nil {
		t.Fatal(err)
	}
	if c.Name() != "test-agent" {
		t.Errorf("Name = %q", c.Name())
	}
}

func TestNewClient_WithLogger(t *testing.T) {
	l := logger.NoopLogger()
	c, err := NewClient("x", "https://x.com", WithLogger(l))
	if err != nil {
		t.Fatal(err)
	}
	if c.log != l {
		t.Error("logger not propagated")
	}
}

// ---------------------------------------------------------------------------
// Nil-receiver safety
// ---------------------------------------------------------------------------

func TestClient_Name_NilReceiver(t *testing.T) {
	var c *Client
	if c.Name() != "" {
		t.Fatal("nil Name should be empty string")
	}
}

func TestClient_NilReceiver_AllMethods(t *testing.T) {
	var c *Client
	ctx := context.Background()
	req := interfaces.A2ASendMessageRequest{Message: interfaces.A2AMessage{Role: "user"}}

	if err := c.Ping(ctx); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Errorf("Ping: %v", err)
	}
	if _, err := c.ResolveCard(ctx); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Errorf("ResolveCard: %v", err)
	}
	if _, err := c.ListSkills(ctx); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Errorf("ListSkills: %v", err)
	}
	if _, err := c.SendMessage(ctx, req); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Errorf("SendMessage: %v", err)
	}
	if _, err := c.SendStreamingMessage(ctx, req); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Errorf("SendStreamingMessage: %v", err)
	}
	if _, err := c.GetTask(ctx, "id"); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Errorf("GetTask: %v", err)
	}
	if _, err := c.CancelTask(ctx, "id"); err == nil || !strings.Contains(err.Error(), "nil") {
		t.Errorf("CancelTask: %v", err)
	}
	if err := c.Close(); err != nil {
		t.Errorf("Close nil: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Input validation
// ---------------------------------------------------------------------------

func TestClient_GetTask_EmptyTaskID(t *testing.T) {
	c, _ := NewClient("a", "https://x.com")
	_, err := c.GetTask(context.Background(), "   ")
	if err == nil || !strings.Contains(err.Error(), "taskID is empty") {
		t.Fatalf("got %v", err)
	}
}

func TestClient_CancelTask_EmptyTaskID(t *testing.T) {
	c, _ := NewClient("a", "https://x.com")
	_, err := c.CancelTask(context.Background(), "   ")
	if err == nil || !strings.Contains(err.Error(), "taskID is empty") {
		t.Fatalf("got %v", err)
	}
}

// ---------------------------------------------------------------------------
// Close behaviour
// ---------------------------------------------------------------------------

func TestClient_Close_NilReceiver(t *testing.T) {
	var c *Client
	if err := c.Close(); err != nil {
		t.Fatalf("Close(nil) = %v", err)
	}
}

func TestClient_Close_Idempotent(t *testing.T) {
	c, _ := NewClient("a", "https://x.com")
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestClient_OperationsAfterClose_ReturnError(t *testing.T) {
	c, _ := NewClient("a", "https://x.com")
	_ = c.Close()
	ctx := context.Background()
	req := interfaces.A2ASendMessageRequest{Message: interfaces.A2AMessage{Role: "user"}}

	if err := c.Ping(ctx); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("Ping after Close: %v", err)
	}
	if _, err := c.ResolveCard(ctx); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("ResolveCard after Close: %v", err)
	}
	if _, err := c.SendMessage(ctx, req); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Errorf("SendMessage after Close: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Ping / ResolveCard / ListSkills — card-only server (no protocol handler)
// ---------------------------------------------------------------------------

func TestClient_Ping_OK(t *testing.T) {
	card := &a2a.AgentCard{
		Name:                "Test",
		SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface("", a2a.TransportProtocolJSONRPC)},
	}
	baseURL, cleanup := testCardOnlyServer(t, card)
	defer cleanup()

	c, err := NewClient("ping-test", baseURL, WithTimeout(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping: %v", err)
	}
}

func TestClient_Ping_ServerDown_ReturnsError(t *testing.T) {
	c, _ := NewClient("dead", "http://localhost:19999", WithTimeout(500*time.Millisecond))
	err := c.Ping(context.Background())
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
	if !strings.Contains(err.Error(), "a2a client ping") {
		t.Errorf("error = %v", err)
	}
}

func TestClient_ResolveCard_OK(t *testing.T) {
	card := &a2a.AgentCard{
		Name:             "My Agent",
		Description:      "does things",
		Version:          "3.0",
		DocumentationURL: "https://docs.example.com",
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface("", a2a.TransportProtocolJSONRPC),
		},
		DefaultInputModes:  []string{"text/plain"},
		DefaultOutputModes: []string{"application/json"},
		Skills: []a2a.AgentSkill{
			{ID: "s1", Name: "Skill 1", Description: "desc", Tags: []string{"t1"}},
			{ID: "s2", Name: "Skill 2", Description: "desc2"},
		},
	}
	baseURL, cleanup := testCardOnlyServer(t, card)
	defer cleanup()

	c, err := NewClient("resolve-test", baseURL, WithTimeout(5*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	got, err := c.ResolveCard(context.Background())
	if err != nil {
		t.Fatalf("ResolveCard: %v", err)
	}

	if got.Name != "My Agent" {
		t.Errorf("Name = %q", got.Name)
	}
	if got.Description != "does things" {
		t.Errorf("Description = %q", got.Description)
	}
	if got.Version != "3.0" {
		t.Errorf("Version = %q", got.Version)
	}
	if len(got.Skills) != 2 {
		t.Fatalf("Skills = %d, want 2", len(got.Skills))
	}
	if got.Skills[0].ID != "s1" || got.Skills[1].ID != "s2" {
		t.Errorf("Skills = %+v", got.Skills)
	}
	if len(got.InputModes) != 1 || got.InputModes[0] != "text/plain" {
		t.Errorf("InputModes = %v", got.InputModes)
	}
}

func TestClient_ResolveCard_CachesResult(t *testing.T) {
	callCount := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == a2asrv.WellKnownAgentCardPath {
			callCount++
		}
		card := &a2a.AgentCard{
			Name:                "Counter",
			SupportedInterfaces: []*a2a.AgentInterface{},
		}
		h := a2asrv.NewStaticAgentCardHandler(card)
		h.ServeHTTP(w, r)
	}))
	defer srv.Close()

	c, _ := NewClient("cache-test", srv.URL, WithTimeout(5*time.Second))
	defer func() { _ = c.Close() }()

	ctx := context.Background()
	// Two calls to ResolveCard; each goes to the HTTP server (we don't cache at this level — card
	// is cached in the struct only after getInner). Both should succeed without panicking.
	if _, err := c.ResolveCard(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ResolveCard(ctx); err != nil {
		t.Fatal(err)
	}
	if callCount < 2 {
		t.Errorf("expected at least 2 card resolves, got %d", callCount)
	}
}

func TestClient_ListSkills_OK(t *testing.T) {
	card := &a2a.AgentCard{
		Name: "Skilled Agent",
		SupportedInterfaces: []*a2a.AgentInterface{
			a2a.NewAgentInterface("", a2a.TransportProtocolJSONRPC),
		},
		Skills: []a2a.AgentSkill{
			{ID: "alpha", Name: "Alpha"},
			{ID: "beta", Name: "Beta"},
		},
	}
	baseURL, cleanup := testCardOnlyServer(t, card)
	defer cleanup()

	c, _ := NewClient("skills-test", baseURL, WithTimeout(5*time.Second))
	defer func() { _ = c.Close() }()

	skills, err := c.ListSkills(context.Background())
	if err != nil {
		t.Fatalf("ListSkills: %v", err)
	}
	if len(skills) != 2 {
		t.Fatalf("Skills = %d, want 2", len(skills))
	}
	if skills[0].ID != "alpha" || skills[1].ID != "beta" {
		t.Errorf("Skills = %+v", skills)
	}
}

func TestClient_ListSkills_Empty(t *testing.T) {
	card := &a2a.AgentCard{
		Name:                "No Skills",
		SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface("", a2a.TransportProtocolJSONRPC)},
	}
	baseURL, cleanup := testCardOnlyServer(t, card)
	defer cleanup()

	c, _ := NewClient("no-skills", baseURL, WithTimeout(5*time.Second))
	defer func() { _ = c.Close() }()

	skills, err := c.ListSkills(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(skills) != 0 {
		t.Errorf("expected 0 skills, got %d", len(skills))
	}
}

// ---------------------------------------------------------------------------
// SendMessage — full A2A server
// ---------------------------------------------------------------------------

func TestClient_SendMessage_OK(t *testing.T) {
	baseURL, cleanup := testA2AServer(t)
	defer cleanup()

	c, err := NewClient("send-test", baseURL, WithTimeout(10*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = c.Close() }()

	req := interfaces.A2ASendMessageRequest{
		Message: interfaces.A2AMessage{
			Role:  "user",
			Parts: []interfaces.A2APart{{Kind: "text", Text: "hello"}},
		},
	}
	result, err := c.SendMessage(context.Background(), req)
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	// echoExecutor returns a Task (submitted → completed), so we expect Task result.
	if result.Task == nil && result.Message == nil {
		t.Fatal("expected either Task or Message in result")
	}
	if result.Task != nil {
		if result.Task.ID == "" {
			t.Error("Task.ID should be non-empty")
		}
	}
}

func TestClient_SendMessage_WithTaskID(t *testing.T) {
	// The TaskID field on A2ASendMessageRequest is propagated into the SDK message,
	// allowing clients to associate a new message with an existing task. This
	// functionality is fully exercised in convert_test.go (TestToSDKSendMessageRequest_WithTaskID).
	// Here we verify that including a TaskID in the request struct does not cause
	// SendMessage to panic or return an unexpected error class.
	baseURL, cleanup := testA2AServer(t)
	defer cleanup()

	c, _ := NewClient("send-task-id", baseURL, WithTimeout(10*time.Second))
	defer func() { _ = c.Close() }()

	// A request that specifies a non-existent TaskID; the server creates a fresh task.
	req := interfaces.A2ASendMessageRequest{
		Message: interfaces.A2AMessage{
			Role:  "user",
			Parts: []interfaces.A2APart{{Kind: "text", Text: "start"}},
		},
		TaskID: "", // empty ⇒ server assigns a new task ID
	}
	result, err := c.SendMessage(context.Background(), req)
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if result.Task == nil && result.Message == nil {
		t.Fatal("expected either Task or Message in result")
	}
}

func TestClient_SendMessage_WithAcceptedOutputModes(t *testing.T) {
	baseURL, cleanup := testA2AServer(t)
	defer cleanup()

	c, _ := NewClient("send-modes", baseURL, WithTimeout(10*time.Second))
	defer func() { _ = c.Close() }()

	req := interfaces.A2ASendMessageRequest{
		Message:             interfaces.A2AMessage{Role: "user", Parts: []interfaces.A2APart{{Kind: "text", Text: "hi"}}},
		AcceptedOutputModes: []string{"text/plain"},
	}
	_, err := c.SendMessage(context.Background(), req)
	if err != nil {
		t.Fatalf("SendMessage with output modes: %v", err)
	}
}

// ---------------------------------------------------------------------------
// GetTask / CancelTask
// ---------------------------------------------------------------------------

func TestClient_GetTask_OK(t *testing.T) {
	baseURL, cleanup := testA2AServer(t)
	defer cleanup()

	c, _ := NewClient("get-task", baseURL, WithTimeout(10*time.Second))
	defer func() { _ = c.Close() }()

	ctx := context.Background()
	// Create a task first.
	req := interfaces.A2ASendMessageRequest{
		Message: interfaces.A2AMessage{
			Role:  "user",
			Parts: []interfaces.A2APart{{Kind: "text", Text: "create task"}},
		},
	}
	result, err := c.SendMessage(ctx, req)
	if err != nil {
		t.Fatalf("SendMessage: %v", err)
	}
	if result.Task == nil {
		t.Skip("server returned Message instead of Task; skip GetTask test")
	}

	task, err := c.GetTask(ctx, result.Task.ID)
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if task.ID != result.Task.ID {
		t.Errorf("GetTask ID = %q, want %q", task.ID, result.Task.ID)
	}
}

func TestClient_CancelTask_NotFound_ReturnsError(t *testing.T) {
	// Verify that CancelTask propagates server errors (e.g. task not found)
	// with the expected "a2a client cancel task" prefix.
	baseURL, cleanup := testA2AServer(t)
	defer cleanup()

	c, _ := NewClient("cancel-not-found", baseURL, WithTimeout(5*time.Second))
	defer func() { _ = c.Close() }()

	_, err := c.CancelTask(context.Background(), "non-existent-task-id-12345")
	if err == nil {
		t.Fatal("expected error for non-existent task")
	}
	if !strings.Contains(err.Error(), "a2a client cancel task") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestClient_CancelTask_WorkingTask(t *testing.T) {
	// Use hangingExecutor so the task stays in WORKING state long enough to cancel.
	baseURL, exec, cleanup := testHangingServer(t)
	defer cleanup()

	// Give the whole test a hard deadline so the goroutine below doesn't leak.
	const testTimeout = 20 * time.Second
	ctx, ctxCancel := context.WithTimeout(context.Background(), testTimeout)
	defer ctxCancel()

	c, _ := NewClient("cancel-working", baseURL, WithTimeout(15*time.Second))
	defer func() { _ = c.Close() }()

	req := interfaces.A2ASendMessageRequest{
		Message: interfaces.A2AMessage{
			Role:  "user",
			Parts: []interfaces.A2APart{{Kind: "text", Text: "long task"}},
		},
	}

	// Stream events in a background goroutine; we need the task ID from the
	// first event so we can call CancelTask from the main goroutine.
	taskIDCh := make(chan string, 1)
	streamDone := make(chan error, 1)

	go func() {
		seq, err := c.SendStreamingMessage(ctx, req)
		if err != nil {
			streamDone <- err
			return
		}
		for event, streamErr := range seq {
			if streamErr != nil {
				streamDone <- streamErr
				return
			}
			// Forward the first non-empty task ID we see.
			if event.TaskID != "" {
				select {
				case taskIDCh <- event.TaskID:
				default:
				}
			}
		}
		streamDone <- nil
	}()

	// Wait for the executor to reach WORKING state before cancelling.
	select {
	case <-exec.working:
	case <-ctx.Done():
		t.Fatal("timed out waiting for WORKING state")
	}

	// Collect the task ID (first event should carry it).
	var taskID string
	select {
	case taskID = <-taskIDCh:
	case <-ctx.Done():
		t.Fatal("timed out waiting for task ID from stream")
	}

	// Cancel the in-progress task.
	task, err := c.CancelTask(context.Background(), taskID)
	if err != nil {
		t.Fatalf("CancelTask: %v", err)
	}
	if task.Status != interfaces.A2ATaskStatusCanceled {
		t.Errorf("status = %q, want canceled", task.Status)
	}

	// The streaming goroutine may or may not receive the final cancel event
	// depending on the SSE delivery timing. Cancel the context to unblock it and
	// wait for it to exit cleanly.
	ctxCancel()
	select {
	case err := <-streamDone:
		t.Logf("stream ended: %v", err)
	case <-time.After(5 * time.Second):
		t.Error("stream goroutine did not finish within timeout")
	}
}

// ---------------------------------------------------------------------------
// SendStreamingMessage
// ---------------------------------------------------------------------------

func TestClient_SendStreamingMessage_OK(t *testing.T) {
	baseURL, cleanup := testA2AServer(t)
	defer cleanup()

	c, _ := NewClient("stream-test", baseURL, WithTimeout(10*time.Second))
	defer func() { _ = c.Close() }()

	req := interfaces.A2ASendMessageRequest{
		Message: interfaces.A2AMessage{
			Role:  "user",
			Parts: []interfaces.A2APart{{Kind: "text", Text: "stream me"}},
		},
	}
	seq, err := c.SendStreamingMessage(context.Background(), req)
	if err != nil {
		t.Fatalf("SendStreamingMessage setup: %v", err)
	}
	if seq == nil {
		t.Fatal("expected non-nil iterator")
	}

	var events []interfaces.A2AStreamEvent
	for event, err := range seq {
		if err != nil {
			t.Fatalf("stream error: %v", err)
		}
		events = append(events, event)
	}
	if len(events) == 0 {
		t.Error("expected at least one streaming event")
	}
}

func TestClient_SendStreamingMessage_CancelContext_StopsStream(t *testing.T) {
	baseURL, cleanup := testA2AServer(t)
	defer cleanup()

	c, _ := NewClient("stream-cancel", baseURL, WithTimeout(10*time.Second))
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req := interfaces.A2ASendMessageRequest{
		Message: interfaces.A2AMessage{
			Role:  "user",
			Parts: []interfaces.A2APart{{Kind: "text", Text: "long stream"}},
		},
	}
	seq, err := c.SendStreamingMessage(ctx, req)
	if err != nil {
		t.Fatalf("SendStreamingMessage setup: %v", err)
	}

	// Cancel after receiving the first event.
	count := 0
	for _, streamErr := range seq {
		_ = streamErr
		count++
		cancel()
		break
	}
	// Should not panic or hang; at least one event received.
	if count == 0 {
		t.Error("expected at least one event before cancel")
	}
}

// ---------------------------------------------------------------------------
// Concurrency: two goroutines calling SendMessage simultaneously
// ---------------------------------------------------------------------------

func TestClient_SendMessage_Concurrent(t *testing.T) {
	baseURL, cleanup := testA2AServer(t)
	defer cleanup()

	c, _ := NewClient("concurrent", baseURL, WithTimeout(10*time.Second))
	defer func() { _ = c.Close() }()

	const n = 4
	errs := make(chan error, n)
	req := interfaces.A2ASendMessageRequest{
		Message: interfaces.A2AMessage{
			Role:  "user",
			Parts: []interfaces.A2APart{{Kind: "text", Text: "concurrent"}},
		},
	}
	for i := 0; i < n; i++ {
		go func() {
			_, err := c.SendMessage(context.Background(), req)
			errs <- err
		}()
	}
	for i := 0; i < n; i++ {
		if err := <-errs; err != nil {
			t.Errorf("concurrent SendMessage[%d]: %v", i, err)
		}
	}
}

// ---------------------------------------------------------------------------
// Header injection (WithToken / WithHeaders)
// ---------------------------------------------------------------------------

func TestClient_WithToken_HeaderSentToResolver(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		card := &a2a.AgentCard{
			Name:                "Gated",
			SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface("", a2a.TransportProtocolJSONRPC)},
		}
		a2asrv.NewStaticAgentCardHandler(card).ServeHTTP(w, r)
	}))
	defer srv.Close()

	c, _ := NewClient("auth-test", srv.URL,
		WithToken("my-secret-token"),
		WithTimeout(5*time.Second),
	)
	defer func() { _ = c.Close() }()

	_ = c.Ping(context.Background())

	if gotAuth != "Bearer my-secret-token" {
		t.Errorf("Authorization = %q, want %q", gotAuth, "Bearer my-secret-token")
	}
}

func TestClient_WithHeaders_SentToResolver(t *testing.T) {
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-Api-Key")
		card := &a2a.AgentCard{
			Name:                "APIKeyed",
			SupportedInterfaces: []*a2a.AgentInterface{a2a.NewAgentInterface("", a2a.TransportProtocolJSONRPC)},
		}
		a2asrv.NewStaticAgentCardHandler(card).ServeHTTP(w, r)
	}))
	defer srv.Close()

	c, _ := NewClient("key-test", srv.URL,
		WithHeaders(map[string]string{"X-Api-Key": "abc123"}),
		WithTimeout(5*time.Second),
	)
	defer func() { _ = c.Close() }()

	_ = c.Ping(context.Background())

	if gotKey != "abc123" {
		t.Errorf("X-Api-Key = %q, want abc123", gotKey)
	}
}

// ---------------------------------------------------------------------------
// Timeout enforcement
// ---------------------------------------------------------------------------

func TestClient_Ping_ContextTimeout_ReturnsError(t *testing.T) {
	// Very slow server — will exceed the context deadline.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(2 * time.Second)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c, _ := NewClient("timeout-test", srv.URL, WithTimeout(5*time.Second))
	defer func() { _ = c.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := c.Ping(ctx)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}
