package main

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/agenticenv/agent-sdk-go/pkg/agent"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

var (
	emailPattern = regexp.MustCompile(`[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}`)
	ssnPattern   = regexp.MustCompile(`\b\d{3}-\d{2}-\d{4}\b`)
)

func redactPII(s string) string {
	s = emailPattern.ReplaceAllString(s, "[REDACTED_EMAIL]")
	s = ssnPattern.ReplaceAllString(s, "[REDACTED_SSN]")
	return s
}

func hookLog(hook, detail string) {
	fmt.Fprintf(os.Stderr, "[hooks] %s: %s\n", hook, detail)
}

// HookOptions returns [agent.WithHooks] groups that demonstrate every hook point.
// Register the same groups on both the agent starter and Temporal worker when using AGENT_RUNTIME=temporal.
func HookOptions() []agent.Option {
	return []agent.Option{
		agent.WithHooks("pii-scrubber", agent.AgentHooks{
			BeforeLLM: []agent.BeforeLLMHook{beforeLLMRedact},
			AfterLLM:  []agent.AfterLLMHook{afterLLMRedact},
			BeforeTool: []agent.BeforeToolHook{
				func(ctx context.Context, in agent.BeforeToolHookInput) (agent.BeforeToolHookOutput, error) {
					args := cloneArgs(in.Call.Args)
					for k, v := range args {
						if s, ok := v.(string); ok {
							args[k] = redactPII(s)
						}
					}
					hookLog("BeforeTool", fmt.Sprintf("group=%s tool=%s args scrubbed", in.RunMeta.HooksGroup, in.Call.Name))
					return agent.BeforeToolHookOutput{Args: args}, nil
				},
			},
			AfterTool: []agent.AfterToolHook{
				func(ctx context.Context, in agent.AfterToolHookInput) (agent.AfterToolHookOutput, error) {
					content := redactPII(in.Content)
					hookLog("AfterTool", fmt.Sprintf("group=%s tool=%s result scrubbed", in.RunMeta.HooksGroup, in.Call.Name))
					return agent.AfterToolHookOutput{Content: content, Err: in.Err}, nil
				},
			},
			BeforeRetrieve: []agent.BeforeRetrieveHook{
				func(ctx context.Context, in agent.BeforeRetrieveHookInput) (agent.BeforeRetrieveHookOutput, error) {
					q := strings.TrimSpace(in.Query)
					if q != "" && !strings.HasPrefix(q, "kb:") {
						q = "kb: " + q
					}
					hookLog("BeforeRetrieve", fmt.Sprintf("group=%s query=%q", in.RunMeta.HooksGroup, q))
					return agent.BeforeRetrieveHookOutput{Query: q}, nil
				},
			},
			AfterRetrieve: []agent.AfterRetrieveHook{
				func(ctx context.Context, in agent.AfterRetrieveHookInput) (agent.AfterRetrieveHookOutput, error) {
					filtered := make([]interfaces.Document, 0, len(in.Documents))
					for _, doc := range in.Documents {
						if ssnPattern.MatchString(doc.Content) {
							hookLog("AfterRetrieve", fmt.Sprintf("group=%s dropped doc from %s (SSN)", in.RunMeta.HooksGroup, doc.Source))
							continue
						}
						doc.Content = redactPII(doc.Content)
						filtered = append(filtered, doc)
					}
					return agent.AfterRetrieveHookOutput{Documents: filtered}, nil
				},
			},
			BeforeMemoryLoad: []agent.BeforeMemoryLoadHook{
				func(ctx context.Context, in agent.BeforeMemoryLoadHookInput) (agent.BeforeMemoryLoadHookOutput, error) {
					if in.Scope.TenantID == "" {
						return agent.BeforeMemoryLoadHookOutput{}, fmt.Errorf("tenant ID required for memory recall")
					}
					hookLog("BeforeMemoryLoad", fmt.Sprintf("group=%s tenant=%s query=%q", in.RunMeta.HooksGroup, in.Scope.TenantID, in.Query))
					return agent.BeforeMemoryLoadHookOutput{
						Query: in.Query, Limit: in.Limit, MinScore: in.MinScore, Kinds: in.Kinds,
					}, nil
				},
			},
			AfterMemoryLoad: []agent.AfterMemoryLoadHook{
				func(ctx context.Context, in agent.AfterMemoryLoadHookInput) (agent.AfterMemoryLoadHookOutput, error) {
					ctxBlock := redactPII(in.PromptContext)
					if strings.TrimSpace(ctxBlock) != "" {
						ctxBlock = "## Memories (scrubbed)\n" + ctxBlock
					}
					hookLog("AfterMemoryLoad", fmt.Sprintf("group=%s context len=%d", in.RunMeta.HooksGroup, len(ctxBlock)))
					return agent.AfterMemoryLoadHookOutput{PromptContext: ctxBlock}, nil
				},
			},
			BeforeMemoryStore: []agent.BeforeMemoryStoreHook{
				func(ctx context.Context, in agent.BeforeMemoryStoreHookInput) (agent.BeforeMemoryStoreHookOutput, error) {
					if in.Scope.TenantID == "" {
						return agent.BeforeMemoryStoreHookOutput{}, fmt.Errorf("tenant ID required for memory store")
					}
					rec := in.Record
					rec.Text = redactPII(rec.Text)
					hookLog("BeforeMemoryStore", fmt.Sprintf("group=%s tenant=%s text scrubbed", in.RunMeta.HooksGroup, in.Scope.TenantID))
					return agent.BeforeMemoryStoreHookOutput{Record: rec, ID: in.ID}, nil
				},
			},
			AfterMemoryStore: []agent.AfterMemoryStoreHook{
				func(ctx context.Context, in agent.AfterMemoryStoreHookInput) (agent.AfterMemoryStoreHookOutput, error) {
					hookLog("AfterMemoryStore", fmt.Sprintf("group=%s id=%s", in.RunMeta.HooksGroup, in.ID))
					return agent.AfterMemoryStoreHookOutput{}, nil
				},
			},
		}),
		agent.WithHooks("audit", agent.AgentHooks{
			AfterLLM: []agent.AfterLLMHook{
				func(ctx context.Context, in agent.AfterLLMHookInput) (agent.AfterLLMHookOutput, error) {
					tokens := int64(0)
					if in.Response.Usage != nil {
						tokens = in.Response.Usage.TotalTokens
					}
					hookLog("AfterLLM", fmt.Sprintf("group=%s run=%s iter=%d tokens=%d", in.RunMeta.HooksGroup, in.RunMeta.RunID, in.RunMeta.Iteration, tokens))
					return agent.AfterLLMHookOutput{Response: in.Response}, nil
				},
			},
		}),
	}
}

func beforeLLMRedact(_ context.Context, in agent.BeforeLLMHookInput) (agent.BeforeLLMHookOutput, error) {
	req := in.Request
	req.SystemMessage = redactPII(req.SystemMessage)
	for i := range req.Messages {
		req.Messages[i].Content = redactPII(req.Messages[i].Content)
	}
	hookLog("BeforeLLM", fmt.Sprintf("group=%s run=%s iter=%d messages scrubbed", in.RunMeta.HooksGroup, in.RunMeta.RunID, in.RunMeta.Iteration))
	return agent.BeforeLLMHookOutput{Request: req}, nil
}

func afterLLMRedact(_ context.Context, in agent.AfterLLMHookInput) (agent.AfterLLMHookOutput, error) {
	resp := in.Response
	resp.Content = redactPII(resp.Content)
	hookLog("AfterLLM", fmt.Sprintf("group=%s response scrubbed", in.RunMeta.HooksGroup))
	return agent.AfterLLMHookOutput{Response: resp}, nil
}

func cloneArgs(args map[string]any) map[string]any {
	if len(args) == 0 {
		return nil
	}
	out := make(map[string]any, len(args))
	for k, v := range args {
		out[k] = v
	}
	return out
}
