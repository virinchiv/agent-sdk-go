package hooks

import (
	"context"

	"github.com/agenticenv/agent-sdk-go/internal/types"
)

// ToolCall is the resolved tool invocation passed to tool hooks.
type ToolCall struct {
	// ID is the tool call identifier from the LLM; used to match tool results in the conversation.
	ID string

	// Name is the tool identifier the LLM selected.
	Name string

	// DisplayName is the human-readable tool name when available.
	DisplayName string

	// Kind classifies the tool implementation (native, MCP, sub-agent, etc.).
	Kind types.ToolKind

	// Args are the arguments the LLM produced for this invocation.
	Args map[string]any
}

// BeforeToolHookInput is the payload passed to [BeforeToolHook] before a tool executes.
type BeforeToolHookInput struct {
	RunMeta RunMeta
	Call    ToolCall
}

// BeforeToolHookOutput is the mutable result returned from [BeforeToolHook].
// Only Args may be changed; identity fields on [BeforeToolHookInput].Call are read-only context.
type BeforeToolHookOutput struct {
	Args map[string]any
}

// AfterToolHookInput is the payload passed to [AfterToolHook] after a tool executes.
type AfterToolHookInput struct {
	RunMeta RunMeta
	Call    ToolCall

	// Content is the serialized tool result passed back to the LLM.
	Content string

	// Err is set when tool execution failed.
	Err error
}

// AfterToolHookOutput is the mutable result returned from [AfterToolHook].
type AfterToolHookOutput struct {
	Content string
	Err     error
}

// BeforeToolHook runs immediately before tool.Execute for native and MCP tools, after
// programmatic authorization and interactive approval have succeeded. Return modified args
// or an error to abort the run. Not a substitute for the SDK's tool authorization or approval mechanisms.
type BeforeToolHook func(ctx context.Context, input BeforeToolHookInput) (BeforeToolHookOutput, error)

// AfterToolHook runs after a tool executes. Return a modified result or an error to abort the run.
type AfterToolHook func(ctx context.Context, input AfterToolHookInput) (AfterToolHookOutput, error)

// RunBeforeTool runs all BeforeTool hooks in hook group registration order for hook-eligible
// tool kinds ([types.ToolKind.HooksEligible]). Hooks within a group run in declaration order.
// The first error aborts the remaining chain.
func RunBeforeTool(ctx context.Context, groups []HookGroup, meta RunMeta, call ToolCall) (ToolCall, error) {
	if !call.Kind.HooksEligible() {
		return call, nil
	}
	current := call
	for _, g := range groups {
		if len(g.Hooks.BeforeTool) == 0 {
			continue
		}
		groupMeta := meta
		groupMeta.HooksGroup = g.Name
		for _, hook := range g.Hooks.BeforeTool {
			if hook == nil {
				continue
			}
			out, err := hook(ctx, BeforeToolHookInput{RunMeta: groupMeta, Call: current})
			if err != nil {
				return ToolCall{}, err
			}
			current.Args = cloneToolArgs(out.Args)
		}
	}
	return current, nil
}

// RunAfterTool runs all AfterTool hooks in hook group registration order for hook-eligible tool
// kinds. Hooks within a group run in declaration order. The first error aborts the chain.
func RunAfterTool(ctx context.Context, groups []HookGroup, meta RunMeta, call ToolCall, content string, execErr error) (string, error, error) {
	if !call.Kind.HooksEligible() {
		return content, execErr, nil
	}
	currentContent := content
	currentErr := execErr
	for _, g := range groups {
		if len(g.Hooks.AfterTool) == 0 {
			continue
		}
		groupMeta := meta
		groupMeta.HooksGroup = g.Name
		for _, hook := range g.Hooks.AfterTool {
			if hook == nil {
				continue
			}
			out, err := hook(ctx, AfterToolHookInput{
				RunMeta: groupMeta,
				Call:    call,
				Content: currentContent,
				Err:     currentErr,
			})
			if err != nil {
				return currentContent, currentErr, err
			}
			currentContent, currentErr = out.Content, out.Err
		}
	}
	return currentContent, currentErr, nil
}

func cloneToolArgs(args map[string]any) map[string]any {
	if len(args) == 0 {
		return nil
	}
	out := make(map[string]any, len(args))
	for k, v := range args {
		out[k] = v
	}
	return out
}
