package agent

import (
	"github.com/agenticenv/agent-sdk-go/internal/hooks"
)

// Core
type AgentHooks = hooks.AgentHooks
type HookGroup = hooks.HookGroup
type RunMeta = hooks.RunMeta

// LLM
type BeforeLLMHookInput = hooks.BeforeLLMHookInput
type BeforeLLMHookOutput = hooks.BeforeLLMHookOutput
type AfterLLMHookInput = hooks.AfterLLMHookInput
type AfterLLMHookOutput = hooks.AfterLLMHookOutput
type BeforeLLMHook = hooks.BeforeLLMHook
type AfterLLMHook = hooks.AfterLLMHook

// Tools
type BeforeToolHookInput = hooks.BeforeToolHookInput
type BeforeToolHookOutput = hooks.BeforeToolHookOutput
type AfterToolHookInput = hooks.AfterToolHookInput
type AfterToolHookOutput = hooks.AfterToolHookOutput
type BeforeToolHook = hooks.BeforeToolHook
type AfterToolHook = hooks.AfterToolHook

// Retriever
type BeforeRetrieveHookInput = hooks.BeforeRetrieveHookInput
type BeforeRetrieveHookOutput = hooks.BeforeRetrieveHookOutput
type AfterRetrieveHookInput = hooks.AfterRetrieveHookInput
type AfterRetrieveHookOutput = hooks.AfterRetrieveHookOutput
type BeforeRetrieveHook = hooks.BeforeRetrieveHook
type AfterRetrieveHook = hooks.AfterRetrieveHook

// Memory
type BeforeMemoryLoadHookInput = hooks.BeforeMemoryLoadHookInput
type BeforeMemoryLoadHookOutput = hooks.BeforeMemoryLoadHookOutput
type AfterMemoryLoadHookInput = hooks.AfterMemoryLoadHookInput
type AfterMemoryLoadHookOutput = hooks.AfterMemoryLoadHookOutput
type BeforeMemoryStoreHookInput = hooks.BeforeMemoryStoreHookInput
type BeforeMemoryStoreHookOutput = hooks.BeforeMemoryStoreHookOutput
type AfterMemoryStoreHookInput = hooks.AfterMemoryStoreHookInput
type AfterMemoryStoreHookOutput = hooks.AfterMemoryStoreHookOutput
type BeforeMemoryLoadHook = hooks.BeforeMemoryLoadHook
type AfterMemoryLoadHook = hooks.AfterMemoryLoadHook
type BeforeMemoryStoreHook = hooks.BeforeMemoryStoreHook
type AfterMemoryStoreHook = hooks.AfterMemoryStoreHook
