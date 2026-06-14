// agent_with_run_async demonstrates RunAsync: non-blocking result channel with
// WithApprovalHandler for tool approvals (same as Run).
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	config "github.com/agenticenv/agent-sdk-go/examples"
	"github.com/agenticenv/agent-sdk-go/pkg/agent"
	"github.com/agenticenv/agent-sdk-go/pkg/tools/calculator"
	"github.com/agenticenv/agent-sdk-go/pkg/tools/echo"
)

func main() {
	cfg := config.LoadFromEnv()

	llmClient, err := config.NewLLMClientFromConfig(cfg)
	if err != nil {
		log.Fatalf("failed to create LLM client: %v", err)
	}

	reg := agent.NewToolRegistry()
	if err := agent.RegisterTools(reg,
		echo.New(),
		calculator.New(),
	); err != nil {
		log.Fatalf("register tools: %v", err)
	}
	lineCh := make(chan string)
	go func() {
		scanner := bufio.NewScanner(os.Stdin)
		for scanner.Scan() {
			lineCh <- scanner.Text()
		}
		close(lineCh)
	}()

	opts := []agent.Option{
		agent.WithName("agent-with-run-async"),
		agent.WithDescription("RunAsync demo: WithApprovalHandler, outcome on resultCh"),
		agent.WithSystemPrompt("You are a helpful assistant. Use the echo or calculator tool when asked."),
		agent.WithLLMClient(llmClient),
		agent.WithToolRegistry(reg),
		agent.WithApprovalHandler(makeApprovalHandler(lineCh)),
		agent.WithLogger(config.NewLoggerFromLogConfig(cfg)),
	}
	opts = append(opts, config.ToolApprovalOptions()...)
	opts = append(opts, config.RuntimeOption(cfg)...)

	a, err := agent.NewAgent(opts...)
	if err != nil {
		log.Fatal(config.FormatNewAgentError("failed to create agent", err))
	}
	defer a.Close()

	prompt := strings.Join(os.Args[1:], " ")
	if prompt == "" {
		prompt = "What is 17 + 23?"
	}

	ctx := context.Background()
	resultCh, err := a.RunAsync(ctx, prompt, nil)
	if err != nil {
		log.Fatalf("RunAsync: %v", err)
	}

	fmt.Println("user:", prompt)
	res := <-resultCh

	if res.Error != nil {
		log.Printf("run failed: %v", res.Error)
		return
	}
	if res.Result == nil {
		log.Print("run finished with no result payload")
		return
	}
	fmt.Println("agent:", res.Result.Content)
}

func makeApprovalHandler(lineCh <-chan string) agent.ApprovalHandler {
	return func(ctx context.Context, req *agent.ApprovalRequest) {
		v, err := agent.ParseToolApproval(req)
		if err != nil {
			log.Printf("approval handler: %v", err)
			return
		}
		args := v.Args
		if args == nil {
			args = map[string]any{}
		}
		argsJSON, _ := json.MarshalIndent(args, "", "  ")
		fmt.Printf("\n--- Tool approval required ---\nTool: %s\nArgs:\n%s\nApprove? (y/n): ", v.ToolName, string(argsJSON))
		select {
		case <-ctx.Done():
			return
		case line, ok := <-lineCh:
			if ok && strings.TrimSpace(strings.ToLower(line)) == "y" {
				_ = req.Respond(agent.ApprovalStatusApproved)
			} else if ok {
				_ = req.Respond(agent.ApprovalStatusRejected)
			}
		}
	}
}
