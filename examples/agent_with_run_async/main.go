// agent_with_run_async demonstrates RunAsync: result and approval channels without
// WithApprovalHandler or Stream. Complete each approval with req.Respond.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	config "github.com/agenticenv/agent-sdk-go/examples"
	"github.com/agenticenv/agent-sdk-go/pkg/agent"
	"github.com/agenticenv/agent-sdk-go/pkg/tools"
	"github.com/agenticenv/agent-sdk-go/pkg/tools/calculator"
	"github.com/agenticenv/agent-sdk-go/pkg/tools/echo"
)

func main() {
	cfg := config.LoadFromEnv()

	llmClient, err := config.NewLLMClientFromConfig(cfg)
	if err != nil {
		log.Fatalf("failed to create LLM client: %v", err)
	}

	reg := tools.NewRegistry()
	reg.Register(echo.New())
	reg.Register(calculator.New())

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
		agent.WithDescription("RunAsync demo: approvals on approvalCh, outcome on resultCh"),
		agent.WithSystemPrompt("You are a helpful assistant. Use the echo or calculator tool when asked."),
		agent.WithLLMClient(llmClient),
		agent.WithToolRegistry(reg),
		agent.WithLogger(config.NewLoggerFromLogConfig(cfg)),
	}
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
	resultCh, approvalCh, err := a.RunAsync(ctx, prompt, "")
	if err != nil {
		log.Fatalf("RunAsync: %v", err)
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for req := range approvalCh {
			v, err := agent.ParseToolApproval(req)
			if err != nil {
				log.Printf("approval from RunAsync: %v", err)
				continue
			}
			args := v.Args
			if args == nil {
				args = map[string]any{}
			}
			argsJSON, _ := json.MarshalIndent(args, "", "  ")
			fmt.Printf("\n--- Tool approval required ---\nTool: %s\nArgs:\n%s\nApprove? (y/n): ", v.ToolName, string(argsJSON))
			line, ok := <-lineCh
			if ok && strings.TrimSpace(strings.ToLower(line)) == "y" {
				if err := req.Respond(agent.ApprovalStatusApproved); err != nil {
					log.Printf("respond approved: %v", err)
				}
			} else if ok {
				if err := req.Respond(agent.ApprovalStatusRejected); err != nil {
					log.Printf("respond rejected: %v", err)
				}
			}
		}
	}()

	fmt.Println("user:", prompt)
	res := <-resultCh
	wg.Wait()

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
