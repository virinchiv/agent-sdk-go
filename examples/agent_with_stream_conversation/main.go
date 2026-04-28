package main

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"strings"

	config "github.com/agenticenv/agent-sdk-go/examples"
	"github.com/agenticenv/agent-sdk-go/examples/shared"
	"github.com/agenticenv/agent-sdk-go/pkg/agent"
	"github.com/agenticenv/agent-sdk-go/pkg/conversation/inmem"
	"github.com/agenticenv/agent-sdk-go/pkg/tools"
	"github.com/agenticenv/agent-sdk-go/pkg/tools/calculator"
	"github.com/agenticenv/agent-sdk-go/pkg/tools/echo"
)

// agent_with_stream_conversation demonstrates Stream with conversation and
// proper event handling: streamed text not duplicated from RUN_FINISHED.
func main() {
	cfg := config.LoadFromEnv()

	llmClient, err := config.NewLLMClientFromConfig(cfg)
	if err != nil {
		log.Fatalf("failed to create LLM client: %v", err)
	}

	conv := inmem.NewInMemoryConversation(inmem.WithMaxSize(100))

	reg := tools.NewRegistry()
	reg.Register(echo.New())
	reg.Register(calculator.New())

	opts := []agent.Option{
		agent.WithName("agent-stream-conversation"),
		agent.WithDescription("Stream with conversation; shows event handling pattern to avoid duplicate output"),
		agent.WithSystemPrompt("You are a helpful assistant. Remember context. Use tools: echo, calculator."),
		agent.WithTemporalConfig(&agent.TemporalConfig{
			Host:      cfg.Host,
			Port:      cfg.Port,
			Namespace: cfg.Namespace,
			TaskQueue: cfg.TaskQueue,
		}),
		agent.WithLLMClient(llmClient),
		agent.WithStream(true),
		agent.WithToolRegistry(reg),
		agent.WithToolApprovalPolicy(agent.AutoToolApprovalPolicy()),
		agent.WithConversation(conv),
		agent.WithConversationSize(20),
		agent.WithLogger(config.NewLoggerFromLogConfig(cfg)),
	}

	a, err := agent.NewAgent(opts...)
	if err != nil {
		log.Fatal(config.FormatNewAgentError("failed to create agent", err))
	}
	defer a.Close()

	convID := "session-1"
	if id := os.Getenv("CONVERSATION_ID"); id != "" {
		convID = id
	}

	if len(os.Args) > 1 {
		prompt := strings.Join(os.Args[1:], " ")
		runSingleTurn(context.Background(), a, prompt, convID)
		return
	}

	runInteractive(context.Background(), a, convID)
}

func runSingleTurn(ctx context.Context, a *agent.Agent, prompt, convID string) {
	fmt.Println("user:", prompt)
	fmt.Println("assistant:")
	eventCh, err := a.Stream(ctx, prompt, convID)
	if err != nil {
		log.Printf("Stream failed: %v", err)
		return
	}
	handleEvents(eventCh)
	fmt.Println()
}

func runInteractive(ctx context.Context, a *agent.Agent, convID string) {
	fmt.Println("Multi-turn conversation with streaming. Type 'exit' or 'quit' to end.")
	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("\nuser: ")
		if !scanner.Scan() {
			break
		}
		prompt := strings.TrimSpace(scanner.Text())
		if prompt == "" {
			continue
		}
		if prompt == "exit" || prompt == "quit" || prompt == "bye" {
			break
		}
		eventCh, err := a.Stream(ctx, prompt, convID)
		if err != nil {
			log.Printf("Stream failed: %v", err)
			continue
		}
		fmt.Print("assistant: ")
		handleEvents(eventCh)
		fmt.Println()
	}
}

// handleEvents processes the event stream. Tracks streamedContent so we don't
// re-print RUN_FINISHED body when it was already streamed as TEXT_MESSAGE_CONTENT.
func handleEvents(eventCh <-chan agent.AgentEvent) {
	var streamedContent bool
	var finalContent string
	for ev := range eventCh {
		if ev == nil {
			continue
		}
		if shared.MarksStreamDelta(ev) {
			streamedContent = true
		}
		printEvent(ev, streamedContent)
		if res := shared.RunResultFromFinishedEvent(ev); res != nil && res.Content != "" {
			finalContent = res.Content
		}
	}
	_ = finalContent
}

func printEvent(ev agent.AgentEvent, streamedContent bool) {
	if ev == nil {
		return
	}
	switch eventType := ev.Type(); eventType {
	case agent.AgentEventTypeTextMessageContent:
		if t, ok := ev.(*agent.AgentTextMessageContentEvent); ok && t.Delta != "" {
			fmt.Print(t.Delta)
		}
	case agent.AgentEventTypeReasoningMessageContent:
		if r, ok := ev.(*agent.AgentReasoningMessageContentEvent); ok && r.Delta != "" {
			fmt.Printf("[thinking] %s", r.Delta)
		}
	case agent.AgentEventTypeToolCallStart:
		if t, ok := ev.(*agent.AgentToolCallStartEvent); ok {
			fmt.Printf("\n[%s] %s (%s)\n", eventType, t.ToolCallName, t.ToolCallID)
		}
	case agent.AgentEventTypeToolCallArgs:
		if t, ok := ev.(*agent.AgentToolCallArgsEvent); ok && t.Delta != "" {
			fmt.Printf("[%s] %s %s\n", eventType, t.ToolCallID, t.Delta)
		}
	case agent.AgentEventTypeToolCallResult:
		if t, ok := ev.(*agent.AgentToolCallResultEvent); ok {
			fmt.Printf("[%s] %s: %s\n", eventType, t.ToolCallID, t.Content)
		}
	case agent.AgentEventTypeRunStarted:
		if r, ok := ev.(*agent.AgentRunStartedEvent); ok {
			fmt.Printf("[%s] threadID=%s runID=%s\n", eventType, r.ThreadID, r.RunID)
		}
	case agent.AgentEventTypeRunError:
		if re, ok := ev.(*agent.AgentRunErrorEvent); ok {
			fmt.Printf("[%s] %s\n", eventType, re.Message)
		}
	case agent.AgentEventTypeRunFinished:
		res := shared.RunResultFromFinishedEvent(ev)
		if res != nil && res.Content != "" && !streamedContent {
			fmt.Printf("[%s] %s\n", eventType, res.Content)
		}
		if u := shared.UsageFooter(res); u != "" {
			fmt.Println()
			fmt.Println(u)
		}
	default:
		//fmt.Printf("[%s] %+v\n", eventType, ev)
		return
	}
}
