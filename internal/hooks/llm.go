package hooks

import (
	"context"

	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

// BeforeLLMHookInput is the payload passed to [BeforeLLMHook] before an LLM call.
type BeforeLLMHookInput struct {
	RunMeta RunMeta
	Request interfaces.LLMRequest
}

// BeforeLLMHookOutput is the mutable result returned from [BeforeLLMHook].
type BeforeLLMHookOutput struct {
	Request interfaces.LLMRequest
}

// AfterLLMHookInput is the payload passed to [AfterLLMHook] after an LLM call completes.
type AfterLLMHookInput struct {
	RunMeta  RunMeta
	Response interfaces.LLMResponse
}

// AfterLLMHookOutput is the mutable result returned from [AfterLLMHook].
type AfterLLMHookOutput struct {
	Response interfaces.LLMResponse
}

// BeforeLLMHook runs before each LLM request is sent. Return a modified request or an error to abort the run.
type BeforeLLMHook func(ctx context.Context, input BeforeLLMHookInput) (BeforeLLMHookOutput, error)

// AfterLLMHook runs after each LLM response is received. Return a modified response or an error to abort the run.
type AfterLLMHook func(ctx context.Context, input AfterLLMHookInput) (AfterLLMHookOutput, error)

// RunBeforeLLM runs all BeforeLLM hooks in hook group registration order. Hooks within a group
// run in declaration order; each hook receives the output of the previous hook. The first error
// aborts the remaining chain. Returns req unchanged when groups is empty or no BeforeLLM hooks
// are registered.
func RunBeforeLLM(ctx context.Context, groups []HookGroup, meta RunMeta, req interfaces.LLMRequest) (interfaces.LLMRequest, error) {
	current := req
	for _, g := range groups {
		if len(g.Hooks.BeforeLLM) == 0 {
			continue
		}
		groupMeta := meta
		groupMeta.HooksGroup = g.Name
		for _, hook := range g.Hooks.BeforeLLM {
			if hook == nil {
				continue
			}
			out, err := hook(ctx, BeforeLLMHookInput{RunMeta: groupMeta, Request: current})
			if err != nil {
				return interfaces.LLMRequest{}, err
			}
			current = out.Request
		}
	}
	return current, nil
}

// RunAfterLLM runs all AfterLLM hooks in hook group registration order. Hooks within a group run
// in declaration order; each hook receives the output of the previous hook. The first error
// aborts the remaining chain. Returns resp unchanged when groups is empty or no AfterLLM hooks
// are registered.
func RunAfterLLM(ctx context.Context, groups []HookGroup, meta RunMeta, resp interfaces.LLMResponse) (interfaces.LLMResponse, error) {
	current := resp
	for _, g := range groups {
		if len(g.Hooks.AfterLLM) == 0 {
			continue
		}
		groupMeta := meta
		groupMeta.HooksGroup = g.Name
		for _, hook := range g.Hooks.AfterLLM {
			if hook == nil {
				continue
			}
			out, err := hook(ctx, AfterLLMHookInput{RunMeta: groupMeta, Response: current})
			if err != nil {
				return interfaces.LLMResponse{}, err
			}
			current = out.Response
		}
	}
	return current, nil
}
