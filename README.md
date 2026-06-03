# Agent SDK for Go

**Build production-grade AI agents in Go** — backed by [Temporal](https://temporal.io) for durable, crash-resilient execution, or run in-process with zero setup. See [Capabilities](#capabilities) for the full feature set.

[![CI](https://github.com/agenticenv/agent-sdk-go/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/agenticenv/agent-sdk-go/actions)
[![Release](https://img.shields.io/github/v/release/agenticenv/agent-sdk-go?label=Release)](https://github.com/agenticenv/agent-sdk-go/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/agenticenv/agent-sdk-go.svg)](https://pkg.go.dev/github.com/agenticenv/agent-sdk-go)
[![Go Report Card](https://goreportcard.com/badge/github.com/agenticenv/agent-sdk-go)](https://goreportcard.com/report/github.com/agenticenv/agent-sdk-go)
[![License](https://img.shields.io/github/license/agenticenv/agent-sdk-go?label=License&cacheSeconds=0)](LICENSE)
[![Mentioned in Awesome Go](https://awesome.re/mentioned-badge.svg)](https://github.com/avelino/awesome-go)

> **Versioning:** [Semantic versioning](https://semver.org/); published lines are **git tags** (e.g. `v0.1.2`). See the **[latest release](https://github.com/agenticenv/agent-sdk-go/releases/latest)** — the README does not pin a patch number so it stays accurate after each tag.
>
> **Note:** Independent community library — **not** affiliated with Temporal Technologies.

## Table of Contents

- [Overview](#overview)
- [Capabilities](#capabilities)
- [Reference apps](#reference-apps)
- [Runtimes](#runtimes)
  - [Temporal](#temporal-runtime)
  - [In-Process](#in-process-runtime)
- [Getting Started](#getting-started)
  - [Prerequisites](#prerequisites)
  - [Create an agent and run](#create-an-agent-and-run)
  - [Temporal connection](#temporal-connection)
  - [LLM providers](#create-an-llm-client-openai-anthropic-or-gemini)
  - [Stream events](#stream-events-stream)
  - [Token usage](#token-usage-llmusage)
  - [Tools](#tools)
  - [MCP](#mcp-model-context-protocol)
  - [A2A](#a2a-agent-to-agent)
  - [Retrieval (RAG)](#retrieval-rag)
  - [Sub-agents](#sub-agents)
  - [Approvals](#approvals)
  - [Timeouts and deadlines](#timeouts-and-deadlines)
  - [Custom tools](#custom-tools)
  - [Response format](#response-format)
  - [Reasoning / extended thinking](#reasoning--extended-thinking)
  - [Multiple agents](#multiple-agents)
  - [Agent and worker in separate processes](#agent-and-worker-in-separate-processes)
  - [Conversation](#conversation-message-history)
  - [AG-UI Protocol](#ag-ui-protocol)
- [Observability](#observability)
  - [Wire OTLP](#wire-otlp-traces--metrics--logs-in-one-block)
  - [Bring your own tracer / metrics](#bring-your-own-tracer--metrics)
  - [Traces](#traces-spans)
  - [Metrics](#metrics)
  - [Logs](#logs)
- [Configuration](#configuration)
- [Development](#development)
  - [Code Coverage](#code-coverage)
- [Setup and run examples](#setup-and-run-examples)
- [Production Readiness Checklist](#production-readiness-checklist)
- [Disclaimer](#disclaimer)

## Overview

**agent-sdk-go** is a Go SDK for production AI agents — tools, MCP, A2A, human-in-the-loop approvals, and multi-agent delegation.

Every agent run on the **Temporal runtime** is a durable workflow: it survives process crashes and deploys, supports horizontal scaling, and is observable as a real service operation. This is the recommended runtime for production workloads where run durability matters. A **running Temporal server** is required.

The **in-process runtime** runs the agent loop directly in your process with no external dependencies — ideal for development, testing, and deployments where Temporal is not available. It has full feature parity: tools, MCP, A2A, sub-agents, streaming, AG-UI, approvals, conversation, and observability.

`pkg/agent` exposes three entry points — `Run`, `Stream`, and `RunAsync`. Add `WithTemporalConfig` or `WithTemporalClient` for the Temporal runtime; omit it for in-process. See [Getting Started](#getting-started) or [Runtimes](#runtimes).

## Capabilities

- **LLM providers** — OpenAI, Anthropic, and Gemini out of the box; bring your own via `interfaces.LLMClient`.
- **Tools** — Register built-in or custom tools via `interfaces.Tool`; optional **parallel vs sequential** execution for multiple tool calls in one LLM round (`WithAgentToolExecutionMode`).
- **Human-in-the-loop** — Approval gates on tool calls and delegation across `Run`, `RunAsync`, and `Stream`.
- **Conversation** — Persist multi-turn message history via `WithConversation`; in-memory store for in-process; in-memory and Redis for Temporal. Bring your own via `interfaces.Conversation`.
- **Sub-agents** — Delegate to specialist agents via `WithSubAgents`; recursive delegation with depth limiting; all sub-agent events fan in to the parent stream on both runtimes.
- **MCP** — Extend agent capabilities by connecting any MCP server as a tool source via `WithMCPConfig` or `WithMCPClients`.
- **A2A** — Connect remote [Agent-to-Agent](https://github.com/a2aproject/A2A) agents as tool providers via `WithA2AConfig` or `WithA2AClients`; or expose the agent itself as an A2A server via `WithA2ADefaultServer` / `WithA2AServer` and `RunA2A`.
- **Retrieval (RAG)** — Ground agent responses in external knowledge bases via a pluggable `Retriever` interface with built-in Weaviate and pgvector support; extend with your own implementation.
- **Streaming** — Partial tokens and events via `Stream` and `WithStream`.
- **AG-UI** — Stream events conform to the [AG-UI protocol](https://docs.ag-ui.com); agents work out of the box with any AG-UI compatible frontend such as [CopilotKit](https://copilotkit.ai).
- **Reasoning** — Extended thinking / chain-of-thought where supported (Anthropic, Gemini).
- **Token usage** — Track input, output, and reasoning token counts per run.
- **Observability** — OpenTelemetry traces, metrics, and structured logs; export to any OTLP-compatible backend.
- **Durable execution** ★ — Runs survive process crashes and restarts; Temporal workflow history ensures no step is lost.
- **Scale** ★ — Add Temporal workers to scale agent execution horizontally; split agent and worker across separate processes.

> ★ Temporal runtime only.

## Reference apps

Demo applications that use **agent-sdk-go** end-to-end:

- **[Agent Chat](https://github.com/agenticenv/agent-chat)** — Web chat demo with durable conversations; a good reference for wiring the SDK into an HTTP-backed app.

## Runtimes

### Temporal

**Temporal** powers agents through three moving parts: a **Temporal client** that launches agent workflows, **workers** (typically `NewAgentWorker`) that poll task queues and execute workflow and activity code, and **workflow history** that makes each run durable. Workers are stateless — they replay and advance history, not hold state themselves.

- **Workflows** — Durable, **replay-safe** orchestration: the agent “loop” (model rounds, tool routing, when to delegate). Workflow code must stay deterministic; long work happens in activities.
- **Activities** — **LLM calls**, **tool** execution (including **MCP tool** calls), **conversation** updates, approval steps—side effects and I/O. Retries, timeouts, and failure handling apply here.
- **Child workflows** — **Sub-agent delegation** is modeled as **child workflows** so specialists can run on their own task queues with their own workers.
- **Workers & task queues** — Processes **poll** a queue and run scheduled workflow and activity tasks; **scale horizontally** by adding workers. Each agent / sub-agent typically has its own **task queue** name.
- **Long runs** — Very long agent sessions and high-volume event pipelines automatically trigger **continue-as-new** internally, starting a fresh run under the same **workflow ID** while preserving all state; retry transient update or delivery failures as usual.

```mermaid
graph TD
    A[Agent] --> WF[Workflow: agent loop]
    W[Agent Worker] --> WF
    WF --> LLM[Activity: LLM call]
    WF --> Appr[Activity: approval]
    WF --> Child[Child Workflow: sub-agent loop]
    WF --> Tool[Activity: tool execution]
    WF --> Mem[Activity: save memory]
    Child --> LLM2[Activity: LLM call]
    Child --> Tool2[Activity: tool execution]
    Child --> Mem2[Activity: save memory]
```

Details: [Temporal connection](#temporal-connection), [Sub-agents](#sub-agents), [Agent and worker in separate processes](#agent-and-worker-in-separate-processes).

### Durable agents: survive crashes, restarts, and deploys

Because every agent run is a Temporal workflow, **the process can crash and restart without losing a single step** — tool calls already made are not replayed, approvals already given are not re-requested, and the agent resumes exactly where it left off. `DisableLocalWorker` and `NewAgentWorker` let you split the client and execution across OS processes or machines while the cluster holds all state.

**[examples/durable_agent](examples/durable_agent)** walks through this split across two terminals, including crash and retry scenarios. Step-by-step exercises: **[examples/durable_agent/README.md](examples/durable_agent/README.md)**.

### Streaming and approvals

Stream events and approval events cross two boundaries: **Temporal** (durable workflow) and **your** hosts and subscribers. The guarantees differ on each side. These constraints matter most in interactive, user-facing scenarios — autonomous backend agents are largely unaffected since they do not depend on live event delivery.

- **Agent runs are durable.** After a worker restart or deploy, the run resumes from recorded progress in Temporal. You do not need a single process alive for the entire run.
- **Live stream is not backfilled.** Incremental stream traffic — tokens, tool updates — is delivered as produced. If your client was disconnected, you may miss chunks even though the agent completed successfully in Temporal.
- **Approvals degrade gracefully.** If an approval event cannot be delivered, the run continues rather than hanging — the tool is skipped with a clear message. This is intentional for autonomous backend execution; for interactive scenarios, design your UX so users are not silently blocked.
- **Your responsibility.** Keep worker processes supervised and restarting on crash, maintain a stable connection to your Temporal cluster, and ensure stream subscribers can reconnect.
- **Client reconnection and UX.** For interactive apps, if the process serving `Stream` crashes, the workflow continues in Temporal but your client loses the connection. Once a stream is lost, reconnecting to that specific run is not supported — the recommended approach is to block the user from sending a new prompt until the current one completes, then fetch the final response and display it. This keeps conversation turns sequential and avoids out-of-order state. For autonomous agents, this is a non-issue since the caller waits for completion and the workflow finishes regardless.

### In-Process

Runs the agent loop directly in your process — zero setup, zero infrastructure.

**When to use:**
- Development, testing, and prototyping
- Deployments where Temporal is not needed
- Short-lived runs where crash recovery is not required

All capabilities listed above apply except ★ items. Conversation uses the in-memory store only. If the process crashes, the run is lost — no replay, no remote workers.

**Switching to Temporal:** add `WithTemporalConfig` (or `WithTemporalClient`) to any `NewAgent` call — no other code changes required.

## Getting Started

How to **use** the SDK—agents, LLMs, Temporal connection, examples.

### Prerequisites

**Go 1.26+** (see `go.mod`) and credentials for your LLM provider are always required.

- **In-process runtime** (default): no additional setup — just `go get` and an LLM API key.
- **Temporal runtime**: a running Temporal server is required. See **[Temporal setup](temporal-setup.md)**.

**Module:** `github.com/agenticenv/agent-sdk-go`

```bash
go get github.com/agenticenv/agent-sdk-go@latest
```

### Create an agent and run

**Local runtime** (no Temporal required):

```go
import (
    "github.com/agenticenv/agent-sdk-go/pkg/agent"
    "github.com/agenticenv/agent-sdk-go/pkg/llm"
    "github.com/agenticenv/agent-sdk-go/pkg/llm/openai"
)

llmClient, _ := openai.NewClient(
    llm.WithAPIKey("sk-..."),
    llm.WithModel("gpt-4o"),
)

a, _ := agent.NewAgent(
    agent.WithSystemPrompt("You are a helpful assistant."),
    agent.WithLLMClient(llmClient),
)
defer a.Close()

result, err := a.Run(ctx, "Hello", "")
// result.Content, result.AgentName, result.Model
```

**Temporal runtime** (durable execution):

```go
a, _ := agent.NewAgent(
    agent.WithTemporalConfig(&agent.TemporalConfig{
        Host: "localhost", Port: 7233,
        Namespace: "default", TaskQueue: "my-app",
    }),
    agent.WithSystemPrompt("You are a helpful assistant."),
    agent.WithLLMClient(llmClient),
)
defer a.Close()

result, err := a.Run(ctx, "Hello", "")
```

[examples/simple_agent](examples/simple_agent)

### Temporal connection

Provide **either** `WithTemporalConfig` or `WithTemporalClient`, not both.

**Option 1 — WithTemporalConfig** (simple, local dev):

```go
agent.WithTemporalConfig(&agent.TemporalConfig{
    Host: "localhost", Port: 7233,
    Namespace: "default", TaskQueue: "my-app",
})
```

**Option 2 — WithTemporalClient** (TLS, API key auth, Temporal Cloud):

Use when you need mTLS, Temporal Cloud API keys, or other connection options. Create the client yourself and pass it. You must also call `WithTaskQueue`. The agent does not close the client; you own its lifecycle.

```go
import "go.temporal.io/sdk/client"

tc, _ := client.Dial(client.Options{
    HostPort:  "namespace-id.tmprl.cloud:7233",
    Namespace: "my-namespace",
    Credentials: client.NewAPIKeyStaticCredentials(apiKey),
    // Or: ConnectionOptions for mTLS, etc.
})
defer tc.Close()

a, _ := agent.NewAgent(
    agent.WithTemporalClient(tc),
    agent.WithTaskQueue("my-app"),
    agent.WithLLMClient(llmClient),
)
defer a.Close()
```

[examples/agent_with_temporal_client](examples/agent_with_temporal_client) demonstrates the full pattern.

### Create an LLM client (OpenAI, Anthropic, or Gemini)

```go
// OpenAI
llmClient, err := openai.NewClient(
    llm.WithAPIKey("sk-..."),
    llm.WithModel("gpt-4o"),
    llm.WithBaseURL("https://api.openai.com/v1"),  // optional
)

// Anthropic
llmClient, err := anthropic.NewClient(
    llm.WithAPIKey("..."),
    llm.WithModel("claude-3-5-sonnet-20241022"),
)

// Gemini
llmClient, err := gemini.NewClient(
    llm.WithAPIKey("..."),  // or GOOGLE_API_KEY
    llm.WithModel("gemini-2.5-flash"),
)
```

### Supported LLMs


| Provider      | Package             | Notes                     |
| ------------- | ------------------- | ------------------------- |
| **OpenAI**    | `pkg/llm/openai`    | GPT-4o, GPT-4o-mini, etc. |
| **Anthropic** | `pkg/llm/anthropic` | Claude models             |
| **Gemini**    | `pkg/llm/gemini`    | gemini-2.5-flash, etc.    |


Other providers: implement `[interfaces.LLMClient](pkg/interfaces/llm.go)` (`Generate`, `GenerateStream`, metadata). Copy patterns from `pkg/llm/`.

### Stream events (Stream)

`Stream` returns a channel of `AgentEvent`. Use `agent.WithStream(true)` for partial tokens as they arrive. For **AG-UI** clients, see [AG-UI Protocol](#ag-ui-protocol); for **Temporal** vs. live delivery, see [Streaming and approvals](#streaming-and-approvals).

Lifecycle events include `**RUN_STARTED`**, `**RUN_FINISHED**`, `**RUN_ERROR**`, and (for some flows) `**STEP_STARTED**` / `**STEP_FINISHED**`. Step events bracket a sub-agent child workflow; see [Sub-agents](#sub-agents).

```go
a, _ := agent.NewAgent(
    agent.WithTemporalConfig(...),
    agent.WithLLMClient(...),
    agent.WithStream(true),
)
defer a.Close()

eventCh, err := a.Stream(ctx, "What's 17 * 23?", "")
for ev := range eventCh {
    if ev == nil {
        continue
    }
    switch ev.Type() {
    case agent.AgentEventTypeTextMessageContent:
        if t, ok := ev.(*agent.AgentTextMessageContentEvent); ok {
            fmt.Print(t.Delta)
        }
    case agent.AgentEventTypeToolCallStart:
        if t, ok := ev.(*agent.AgentToolCallStartEvent); ok {
            fmt.Printf("tool: %s\n", t.ToolCallName)
        }
    case agent.AgentEventTypeRunFinished:
        fmt.Println("done")
    }
}
```

[examples/agent_with_stream](examples/agent_with_stream)

#### Displaying stream events

Streaming text deltas (`TEXT_MESSAGE_*`) versus the `**RUN_FINISHED**` body often duplicate—don’t print both. Use `AgentName` on typed events / results to distinguish agents in delegation; several `**RUN_FINISHED**` events may appear before the root run completes. See [examples/agent_with_stream_conversation](examples/agent_with_stream_conversation).

### Token usage (`LLMUsage`)

Each LLM completion can report token counts via `[interfaces.LLMUsage](pkg/interfaces/llm.go)` on `[interfaces.LLMResponse.Usage](pkg/interfaces/llm.go)`. OpenAI, Anthropic, and Gemini clients populate `**PromptTokens**`, `**CompletionTokens**`, `**TotalTokens**`, and optional `**CachedPromptTokens**` / `**ReasoningTokens**` when the provider returns them.

- `**Agent.Run` / `RunAsync`:** `**Usage`** on [*AgentRunResult](pkg/agent/agent.go) is the **sum** across all LLM calls in that run (including tool rounds). Use it for cost estimates, quotas, and logging.
- `**Stream`:** the same aggregate appears as `**Usage*`* on `**RUN_FINISHED**`: assert `**[*AgentRunFinishedEvent](pkg/agent/agent.go)**`, then `**Result**` as `**[*AgentRunResult](pkg/agent/agent.go)**`. OpenAI streaming `**include_usage**` surfaces totals there. Helpers: [examples/shared/utils.go](examples/shared/utils.go) (`UsageFooter`, `RunResultFromFinishedEvent`).

Examples: [examples/simple_agent](examples/simple_agent) (prints usage after `Run`), [examples/agent_with_stream](examples/agent_with_stream) (prints usage on `**RUN_FINISHED**`).

### Tools

Register tools and pass to the agent. Use `agent.WithToolApprovalPolicy(agent.AutoToolApprovalPolicy())` to skip approval (or omit for default approval flow).

**Tool execution mode.** When the model returns **several tool calls** in one assistant turn, you can run them **in parallel** (default) or **sequentially** (one completes before the next starts, in model order):

- `agent.WithAgentToolExecutionMode(agent.AgentToolExecutionModeParallel)` — **default** if you omit the option; good when tool calls are independent and you want lower latency.
- `agent.WithAgentToolExecutionMode(agent.AgentToolExecutionModeSequential)` — use when **order matters** or tools **must not run at the same time** (shared mutable state, rate limits, or similar).

Use the **same** `WithAgentToolExecutionMode` value on `**NewAgent`** and `**NewAgentWorker**` (and on any **sub-agents** you register) for a given app or deployment so every process that runs that agent agrees on the option.

Custom tools may also implement:

- `interfaces.ToolApproval` — tool-level hint for **interactive human approval**. Use this when a person should decide whether the tool runs, and no agent-level approval policy is set.
- `interfaces.ToolAuthorizer` — tool-level **programmatic authorization**. Use this when code should decide whether the tool runs before approval/execute (for example: scopes, tenancy, environment flags, or feature access). Return `Allow=false` to deny the tool call without executing it.

```go
reg := tools.NewRegistry()
reg.Register(calculator.New())
reg.Register(weather.New())

a, _ := agent.NewAgent(
    agent.WithTemporalConfig(...),
    agent.WithLLMClient(...),
    agent.WithToolRegistry(reg),
    agent.WithToolApprovalPolicy(agent.AutoToolApprovalPolicy()),
    // Optional: agent.AgentToolExecutionModeSequential for ordered tool batches; default is Parallel.
    // agent.WithAgentToolExecutionMode(agent.AgentToolExecutionModeSequential),
)
defer a.Close()

result, _ := a.Run(ctx, "What's the weather in Tokyo?", "")
```

[examples/agent_with_tools](examples/agent_with_tools)

### MCP (Model Context Protocol)

MCP servers extend your agent with external tools that work identically to built-in tools across `Run`, `Stream`, `RunAsync`, and approval gates. Each server needs a **unique** name in config (the `WithMCPConfig` map key or the first argument to `mcpclient.NewClient`); tools are registered under stable names so they do not collide when several servers expose the same logical tool id.

At `NewAgent`, the SDK connects to each server, discovers its tools, applies any `**ToolFilter`** (`AllowTools`/`BlockTools`), and registers the results — failing fast if a server is unreachable.

Use `mcp.MCPStdio` (local process) or `mcp.MCPStreamableHTTP` (remote) from `pkg/mcp` for transport. Streamable HTTP supports `Token`, `OAuthClientCreds`, custom `Headers`, and `SkipTLSVerify` for local HTTPS. You can register multiple servers per agent with different transports, timeouts, retries, and filters per server.

Pass `WithMCPConfig` or `WithMCPClients` into `agent.NewAgent` alongside your other options.

**Option 1 — `WithMCPConfig`**

Declare each server as one entry in `agent.MCPServers`: set `Transport`, and optionally `ToolFilter`, `Timeout`, and `RetryAttempts`. The map key is the server name (must be unique).

```go
import (
    "time"

    "github.com/agenticenv/agent-sdk-go/pkg/agent"
    "github.com/agenticenv/agent-sdk-go/pkg/mcp"
)

agent.WithMCPConfig(agent.MCPServers{
    // Subprocess MCP (stdio): different command/args per local server
    "local": {
        Transport: mcp.MCPStdio{
            Command: "node",
            Args:    []string{"path/to/your-mcp-server.js", "--verbose"},
        },
        Timeout: 60 * time.Second,
    },
    // Remote streamable HTTP: different URL, bearer token, tool filter, and retries
    "remote": {
        Transport: mcp.MCPStreamableHTTP{
            URL:   "https://mcp.example.com/mcp",
            Token: "replace-with-bearer-or-use-OAuthClientCreds-or-Headers",
        },
        ToolFilter:    mcp.MCPToolFilter{AllowTools: []string{"search", "wiki"}},
        Timeout:       30 * time.Second,
        RetryAttempts: 3,
    },
})
```

**Option 2 — `WithMCPClients`**

Build one client per server with `mcpclient.NewClient` (server name, transport, then options such as `WithTimeout`, `WithRetryAttempts`, `WithToolFilter`, `WithLogger`). Pass every client to `agent.WithMCPClients` in one call; each `NewClient` name must be unique.

> Note: The packaged client is built on the official [Go MCP SDK](https://github.com/modelcontextprotocol/go-sdk) (`modelcontextprotocol/go-sdk`).

```go
import (
    "time"

    "github.com/agenticenv/agent-sdk-go/pkg/agent"
    "github.com/agenticenv/agent-sdk-go/pkg/mcp"
    mcpclient "github.com/agenticenv/agent-sdk-go/pkg/mcp/client"
)

localCl, err := mcpclient.NewClient("local",
    mcp.MCPStdio{Command: "node", Args: []string{"path/to/your-mcp-server.js"}},
    mcpclient.WithTimeout(60*time.Second),
)
if err != nil {
    // handle
}

remoteCl, err := mcpclient.NewClient("remote",
    mcp.MCPStreamableHTTP{
        URL:   "https://mcp.example.com/mcp",
        Token: "replace-with-bearer-or-use-OAuthClientCreds-or-Headers",
    },
    mcpclient.WithTimeout(30*time.Second),
    mcpclient.WithRetryAttempts(3),
    mcpclient.WithToolFilter(mcp.MCPToolFilter{AllowTools: []string{"search", "wiki"}}),
)
if err != nil {
    // handle
}

a, err := agent.NewAgent(
    agent.WithTemporalConfig(...),
    agent.WithLLMClient(...),
    agent.WithMCPClients(localCl, remoteCl),
    agent.WithToolApprovalPolicy(agent.AutoToolApprovalPolicy()),
)
if err != nil {
    // handle
}
defer a.Close()
```

You may use **Option 1** for some servers and **Option 2** for others on the same agent; keep server names unique across both.

[examples/agent_with_mcp_config](examples/agent_with_mcp_config) and [examples/agent_with_mcp_client](examples/agent_with_mcp_client) show MCP from env (`stdio` or streamable HTTP, URL-only OK, optional bearer/OAuth). Variables: [examples/env.sample](examples/env.sample). Running examples from `examples/`: [examples/README.md](examples/README.md). **MCP transports and testing against real servers:** [examples/agent_with_mcp_config/README.md](examples/agent_with_mcp_config/README.md).

### A2A (Agent-to-Agent)

#### A2A Server

Any agent can be exposed as a standards-compliant [A2A](https://github.com/a2aproject/A2A) HTTP server. Call `RunA2A(ctx)` after `NewAgent`; the server blocks until the context is cancelled then gracefully shuts down. It mounts:

- `GET /.well-known/agent-card.json` — the agent card (name, description, skills, streaming capability, supported transports, optional auth schemes).
- `POST /` — **JSON-RPC v2 only** (PascalCase methods per a2asrv, e.g. `SendMessage`, `SendStreamingMessage`, `GetTask`).

The agent card advertises `TransportProtocolJSONRPC` for `POST /`. Clients must use v2 JSON-RPC method names and the JSON-RPC binding (not legacy slash-style method strings or HTTP+JSON REST paths on this server).

**Supported authentication schemes:**

- **Bearer token (static)** — one or more pre-shared tokens; declared in the agent card as an HTTP `Bearer` security scheme and enforced on every inbound call via `Authorization: Bearer <token>`.

*Planned (not yet supported):* OAuth 2.0 / OIDC, API key, mTLS.

Use `WithA2ADefaultServer` for zero-config local development (binds to `localhost:9999`) or `WithA2AServer` to set a custom hostname, port, and tokens:

```go
a, err := agent.NewAgent(
    agent.WithName("my-agent"),
    agent.WithDescription("Helpful assistant exposed as an A2A server."),
    agent.WithLLMClient(llmClient),
    agent.WithStream(),                         // advertises streaming capability
    agent.WithA2ADefaultServer(),               // localhost:9999, no auth
    // agent.WithA2AServer(&agent.A2AServerConfig{
    //     Hostname:     "0.0.0.0",
    //     Port:         8080,
    //     BearerTokens: []string{"secret-token"},
    //     // Optional: AgentCard: &interfaces.A2AAgentCard{...} (same shape as ResolveCard); merged into the wire card.
    // }),
)
if err != nil {
    // handle
}
defer a.Close()

ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
defer stop()

if err := a.RunA2A(ctx); err != nil {
    log.Fatal(err)
}
```

[examples/agent_with_a2a_server](examples/agent_with_a2a_server) shows a full server example with env-based config (`A2A_SERVER_HOST`, `A2A_SERVER_PORT`, `A2A_SERVER_BEARER_TOKENS`). Variables: [examples/env.sample](examples/env.sample). Running examples from `examples/`: [examples/README.md](examples/README.md). **Inbound server — curl, `a2a` CLI, bearer, cross-test with `agent_with_a2a_config`:** [examples/agent_with_a2a_server/README.md](examples/agent_with_a2a_server/README.md).

#### A2A Client

Remote [A2A](https://github.com/a2aproject/A2A) agents connect as tool providers: the SDK fetches the agent card, discovers skills, and registers each skill as a first-class tool available to the LLM across `Run`, `Stream`, `RunAsync`, and approval gates. Each server entry needs a **unique** name (the `WithA2AConfig` map key or the first argument to `a2aclient.NewClient`); tools are registered under stable names (`a2a_<server>_<skillId>`) that do not collide across multiple remote agents.

At `NewAgent`, the SDK resolves the agent card, applies any `**SkillFilter`** (`AllowSkills`/`BlockSkills`), and registers the resulting tools — failing fast if a server is unreachable.

Configure auth, timeout, and skill filtering per server entry. `SkipTLSVerify` is available for local HTTPS development only.

Pass `WithA2AConfig` or `WithA2AClients` into `agent.NewAgent` alongside your other options.

**Option 1 — `WithA2AConfig`**

Declare each remote agent as one entry in `agent.A2AServers`: set `URL`, and optionally `Token`, `Headers`, `Timeout`, `SkillFilter`, and `SkipTLSVerify`. The map key is the connection name.

```go
import (
    "time"

    "github.com/agenticenv/agent-sdk-go/pkg/agent"
    a2apkg "github.com/agenticenv/agent-sdk-go/pkg/a2a"
)

agent.WithA2AConfig(agent.A2AServers{
    "assistant": {
        URL:     "https://assistant.example.com",
        Timeout: 30 * time.Second,
    },
    "researcher": {
        URL:         "https://researcher.example.com",
        Token:       "replace-with-bearer-token",
        SkillFilter: a2apkg.A2ASkillFilter{AllowSkills: []string{"search", "summarize"}},
        Timeout:     60 * time.Second,
    },
})
```

**Option 2 — `WithA2AClients`**

Build one client per remote agent with `a2aclient.NewClient` (connection name, base URL, then options such as `WithTimeout`, `WithToken`, `WithSkillFilter`, `WithLogger`). Pass every client to `agent.WithA2AClients` in one call; each name must be unique.

```go
import (
    "time"

    "github.com/agenticenv/agent-sdk-go/pkg/agent"
    a2apkg "github.com/agenticenv/agent-sdk-go/pkg/a2a"
    a2aclient "github.com/agenticenv/agent-sdk-go/pkg/a2a/client"
)

assistantCl, err := a2aclient.NewClient("assistant", "https://assistant.example.com",
    a2aclient.WithTimeout(30*time.Second),
)
if err != nil {
    // handle
}

researcherCl, err := a2aclient.NewClient("researcher", "https://researcher.example.com",
    a2aclient.WithToken("replace-with-bearer-token"),
    a2aclient.WithTimeout(60*time.Second),
    a2aclient.WithSkillFilter(a2apkg.A2ASkillFilter{AllowSkills: []string{"search", "summarize"}}),
)
if err != nil {
    // handle
}

a, err := agent.NewAgent(
    agent.WithTemporalConfig(...),
    agent.WithLLMClient(...),
    agent.WithA2AClients(assistantCl, researcherCl),
    agent.WithToolApprovalPolicy(agent.AutoToolApprovalPolicy()),
)
if err != nil {
    // handle
}
defer a.Close()
```

You may use **Option 1** for some remote agents and **Option 2** for others on the same agent; keep connection names unique across both.

[examples/agent_with_a2a_config](examples/agent_with_a2a_config) and [examples/agent_with_a2a_client](examples/agent_with_a2a_client) show A2A from env (`A2A_URL`, optional bearer/headers/filter). Variables: [examples/env.sample](examples/env.sample). Running examples from `examples/`: [examples/README.md](examples/README.md). **Remote agent setup (e.g. `a2a-samples` helloworld), curl checks:** [examples/agent_with_a2a_config/README.md](examples/agent_with_a2a_config/README.md).

### Retrieval (RAG)

Retrieval-Augmented Generation (RAG) lets agents query external knowledge bases and ground responses in up-to-date or domain-specific content — without hardcoding it into the prompt.

Built-in retriever implementations are in `pkg/retriever/weaviate` and `pkg/retriever/pgvector`. Bring your own by implementing `interfaces.Retriever` (`Name`, `Search`).

**Retriever modes**

- **Agentic** (default) — LLM decides when to call the retriever as a tool, the same way it calls any other tool. Best for multi-step agents where retrieval is not always needed.
- **Prefetch** — Retrieval fires before every LLM call. Retrieved context is injected automatically. Best for always-grounded Q&A or enterprise knowledge-base scenarios.
- **Hybrid** — Both: retriever context is pre-fetched and injected (prefetch), and the LLM can also call the retriever as a tool (agentic).

Set mode with `agent.WithRetrieverMode`:

```go
agent.WithRetrieverMode(agent.RetrieverModeAgentic)   // default
agent.WithRetrieverMode(agent.RetrieverModePrefetch)
agent.WithRetrieverMode(agent.RetrieverModeHybrid)    // prefetch + agentic
```

**Weaviate** (local Docker, zero auth for dev):

```go
import "github.com/agenticenv/agent-sdk-go/pkg/retriever/weaviate"

r, err := weaviate.NewRetriever("product_knowledge",
    weaviate.WithHost("localhost:8080"),
    weaviate.WithClassName("ProductDocs"),
)

a, _ := agent.NewAgent(
    agent.WithRetrievers(r),
    agent.WithRetrieverMode(agent.RetrieverModeAgentic),
    ...
)
```

**pgvector** (Postgres with pgvector extension; requires an embed function):

```go
import "github.com/agenticenv/agent-sdk-go/pkg/retriever/pgvector"

r, err := pgvector.NewRetriever("support_knowledge", embedFn,
    pgvector.WithDSN("postgres://user:pass@localhost:5432/mydb"),
    pgvector.WithTable("documents"),
)
```

**Custom retriever** — implement `interfaces.Retriever`:

```go
type Retriever interface {
    Name() string
    Search(ctx context.Context, query string) ([]interfaces.Document, error)
}
```

**Multiple retrievers** — pass as many as needed; each must have a unique name:

```go
agent.WithRetrievers(productRetriever, supportRetriever)
```

[examples/agent_with_retriever/weaviate](examples/agent_with_retriever/weaviate) · [examples/agent_with_retriever/pgvector](examples/agent_with_retriever/pgvector)

### Sub-agents

Build each specialist with `NewAgent` (its own `TaskQueue`, LLM, tools, and prompts). Register specialists on the main agent with `WithSubAgents`. Use `WithName` and `WithDescription` when you want clearer labels for routing. Use `WithMaxSubAgentDepth` only if the default nesting limit is not enough. Run `Run`, `Stream`, or `RunAsync` on the main agent. Sub-agents always run without a conversation ID—they do not inherit the main agent session history. If you use `DisableLocalWorker`, pair each `NewAgentWorker` with the same options as the `NewAgent` that runs that agent.

For streaming scenarios, the main agent is the single subscription point. When using `Stream`, events from all delegated sub-agents fan in to the same main-agent stream, including sub-agent tool approvals and tool call/result events.

`**STEP_STARTED` / `STEP_FINISHED`:** When delegation actually runs a sub-agent **child workflow**, the parent run emits `**AgentEventTypeStepStarted`** then `**AgentEventTypeStepFinished**`, each with `**StepName**` set to the sub-agent’s route name (the `Name` in the `WithSubAgents` configuration). `**STEP_FINISHED**` is emitted when the child returns, whether the sub-run succeeded or failed (a failed run still surfaces as an error string in the following tool result). These are not emitted when delegation is skipped (e.g. empty sub-agent task queue or max depth).

```go
mathAgent, _ := agent.NewAgent(
    agent.WithName("MathSpecialist"),
    agent.WithDescription("Arithmetic; uses calculator tools."),
    agent.WithTemporalConfig(&agent.TemporalConfig{
        Host: "localhost", Port: 7233, Namespace: "default",
        TaskQueue: "my-app-math",
    }),
    agent.WithLLMClient(llmClient),
    agent.WithToolRegistry(mathTools),
    agent.WithToolApprovalPolicy(agent.AutoToolApprovalPolicy()),
)
defer mathAgent.Close()

mainAgent, _ := agent.NewAgent(
    agent.WithName("Main agent"),
    agent.WithSystemPrompt("You are a helpful assistant."),
    agent.WithTemporalConfig(&agent.TemporalConfig{
        Host: "localhost", Port: 7233, Namespace: "default",
        TaskQueue: "my-app-main-agent",
    }),
    agent.WithLLMClient(llmClient),
    agent.WithSubAgents(mathAgent),
    agent.WithMaxSubAgentDepth(2),
    agent.WithToolApprovalPolicy(agent.AutoToolApprovalPolicy()),
)
defer mainAgent.Close()

result, _ := mainAgent.Run(ctx, "What is 144 divided by 12?", "")
```

[examples/agent_with_subagents](examples/agent_with_subagents)

**Stream event fan-in:** Subscribe once on the main agent; the stream includes the full tree (tool events, `**AgentEventTypeCustom`** for approvals/delegation, optional `**AgentEventTypeStepStarted` / `AgentEventTypeStepFinished**` around sub-agent runs, `**AgentEventTypeRunFinished**`, etc.). For each event, use `**ev.Type()**` and type-assert to the concrete struct (see [examples/agent_with_stream](examples/agent_with_stream), [examples/agent_with_subagents](examples/agent_with_subagents)). For `**CUSTOM**`, assert `***AgentCustomEvent**`, then `[ParseCustomEventApproval](pkg/agent/event.go)` or `[ParseCustomEventDelegation](pkg/agent/event.go)` to read `**AgentName**`, `**ApprovalToken**`, `**ToolName**` or `**SubAgentName**`, and call `[OnApproval](pkg/agent/approval.go)` with the token.

### Approvals

The model can trigger registry tools (`WithTools` / registry), MCP tools, and delegation to specialists (`WithSubAgents`). **User approval** can be required before any of those run. `WithToolApprovalPolicy` is the one setting that governs all of them. If you omit it, the default is **require-all**—each path goes through your approval handler. For `Run`, set `WithApprovalHandler` whenever approvals can occur. See [examples/agent_with_subagents](examples/agent_with_subagents).

#### Built-in approval policies

These three types are provided by the `agent` package. For anything else, implement `interfaces.AgentToolApprovalPolicy` (`RequiresApproval`) and pass that value to `WithToolApprovalPolicy`.

- `**RequireAllToolApprovalPolicy`** (default when you omit `WithToolApprovalPolicy`) — every registry tool call, MCP call, and delegation to a sub-agent goes through your approval handler before it runs.
- `**AutoToolApprovalPolicy()**` — nothing requires approval; use only when you fully trust the agent and its tools.
- `**AllowlistToolApprovalPolicy**` — only the tools, specialists, and MCP tool ids you list skip approval; everything else still requires approval. Build the policy from `agent.AllowlistToolApprovalConfig` (`ToolNames`, `SubAgentNames`, optional `MCPTools`), check the error, then pass the result to `WithToolApprovalPolicy`.
  ```go
  approvalPol, err := agent.AllowlistToolApprovalPolicy(agent.AllowlistToolApprovalConfig{
      ToolNames:     []string{"calculator"},
      SubAgentNames: []string{"MathSpecialist"},
      MCPTools:      map[string][]string{"remote": {"search"}}, // optional
  })
  if err != nil {
      log.Fatal(err)
  }

  a, err := agent.NewAgent(
      agent.WithToolApprovalPolicy(approvalPol),
      // ... WithApprovalHandler, WithTemporalConfig, etc.
  )
  if err != nil {
      log.Fatal(err)
  }
  ```
- Custom tools may implement `interfaces.ToolApproval`; in a standard `NewAgent` configuration, the configured `WithToolApprovalPolicy` is the approval gate used by that agent.

#### Sub-agents (approval behavior)

- `**ApprovalRequest**` (Run / RunAsync): `Name` is `[ApprovalRequestNameTool](pkg/agent/approval.go)` or `[ApprovalRequestNameSubAgent](pkg/agent/approval.go)`; decode `**Value**` with `[ParseToolApproval](pkg/agent/approval.go)` (`**ToolName**`, `**AgentName**`) or `[ParseDelegationApproval](pkg/agent/approval.go)` (`**SubAgentName**`, `**AgentName**`).
- **Stream:** match `**AgentEventTypeCustom`**, parse with `**ParseCustomEventApproval**` / `**ParseCustomEventDelegation**`, then `**OnApproval(ctx, token, status)**` with `**ApprovalToken**` from the parsed value (same payload shape as `**ApprovalRequest.Value**` on Run).
- **Parent (main agent):** one policy for its whole list—e.g. `RequireAll` → approving delegation to MathSpecialist is the same flow as approving `calculator` on that agent. `AutoToolApprovalPolicy()` → no approval for delegation or other tools on that agent.
- **Specialist:** separate agent, **its own** `WithToolApprovalPolicy`. Calculator calls inside the specialist use **that** policy, not the parent’s.

```text
Main agent: WithToolApprovalPolicy(RequireAll)     → delegate to math → user approval
Math agent:  WithToolApprovalPolicy(Auto)         → calculator inside specialist → no approval
Math agent:  WithToolApprovalPolicy(RequireAll)   → calculator inside specialist → approval (fan-in on main stream)
```

Each `ApprovalRequest` includes `Respond`; call `req.Respond(Approved|Rejected)` when ready (same as RunAsync):

```go
a, _ := agent.NewAgent(
    agent.WithApprovalHandler(func(ctx context.Context, req *agent.ApprovalRequest) {
        // Prompt user, then:
        _ = req.Respond(agent.ApprovalStatusApproved) // or Rejected
    }),
    // ...
)
a.Run(ctx, prompt, "")
```

**Stream** — approval and delegation requests are `**CUSTOM`** events (`[AgentEventTypeCustom](pkg/agent/event.go)`). Parse with `[ParseCustomEventApproval](pkg/agent/event.go)` / `[ParseCustomEventDelegation](pkg/agent/event.go)`, then call `[OnApproval](pkg/agent/approval.go)` with the token from the value field (see [examples/durable_agent/agent/main.go](examples/durable_agent/agent/main.go)):

```go
for ev := range eventCh {
    if ev == nil || ev.Type() != agent.AgentEventTypeCustom {
        continue
    }
    ce, ok := ev.(*agent.AgentCustomEvent)
    if !ok {
        continue
    }
    if v, err := agent.ParseCustomEventApproval(ce); err == nil {
        _ = a.OnApproval(ctx, v.ApprovalToken, agent.ApprovalStatusApproved)
    } else if d, err := agent.ParseCustomEventDelegation(ce); err == nil {
        _ = a.OnApproval(ctx, d.ApprovalToken, agent.ApprovalStatusApproved)
    }
}
```

**RunAsync** — channel-based completion without streaming. Do not set `WithApprovalHandler` for this path (it is replaced for the duration of the run). Receive each pending approval on `approvalCh` and call `req.Respond` (same idea as `WithApprovalHandler`):

```go
resultCh, approvalCh, err := a.RunAsync(ctx, prompt, "")
if err != nil { /* validation error before goroutine started */ }

go func() {
    for req := range approvalCh {
        _ = req.Respond(agent.ApprovalStatusApproved) // or Rejected
    }
}()

res := <-resultCh
if res.Err != nil { /* handle */ }
// res.Response.Content
```

For **Run** / **RunAsync**, use `req.Respond` only. For **Stream**, use `**OnApproval`** as in the snippet above—the activity token string is `**ApprovalToken**` from `**ParseCustomEventApproval**` / `**ParseCustomEventDelegation**` (not a field on the `**AgentEvent**` interface).

[examples/agent_with_tools/approval](examples/agent_with_tools/approval)

[examples/agent_with_run_async](examples/agent_with_run_async)

**Approval timeout:** `WithApprovalTimeout` (default: `timeout − 30s`) limits how long the user has to approve or reject a tool. If they do not respond in time:

- **Run:** `Run()` returns `nil, err` with the failure.
- **Stream:** An `AgentEventError` is emitted on the event channel with the error message.
- **RunAsync:** `resultCh` receives `RunAsyncResult` with `Err` set.

### Timeouts and deadlines

You can limit run duration in two ways:

**Option 1 — Context with deadline** (per-call):

```go
ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
defer cancel()
result, err := a.Run(ctx, "Hello", "")
```

**Option 2 — Agent `WithTimeout`** (when ctx has no deadline):

```go
a, _ := agent.NewAgent(
    agent.WithTimeout(5 * time.Minute),
    // ...
)
result, err := a.Run(context.Background(), "Hello", "")
```

**Notes:**

- ctx deadline always wins. If ctx has 2 min but agent has `WithTimeout(10 min)`, the run ends at 2 min.
- approvalTimeout (per-approval limit) comes from agent config. If ctx has 1 hour and you use neither option, approval still expires at ~4.5 min (default). Set `WithTimeout` or `WithApprovalTimeout` for longer approvals.
- **Agent mode default timeout:** If neither `ctx` nor `WithTimeout` is set, the default timeout follows `WithAgentMode`: `**AgentModeInteractive`** → **5 minutes**; `**AgentModeAutonomous`** → **60 minutes**.

### Custom tools

Implement `interfaces.Tool`: `Name()`, `Description()`, `Parameters()`, `Execute()`. Register with `agent.WithTools(tool1, tool2)`.

[examples/agent_with_tools/custom](examples/agent_with_tools/custom)

### Response format

By default the agent uses **text-only** output. Use `agent.WithResponseFormat` to request structured output (e.g. JSON with a schema).

**Default (text):** No `WithResponseFormat` — the LLM responds as plain text.

```go
a, _ := agent.NewAgent(
    agent.WithTemporalConfig(...),
    agent.WithLLMClient(...),
    // No WithResponseFormat — text output
)
```

**JSON with schema:** Use `interfaces.ResponseFormatJSON` and a valid JSON Schema. The schema must have `type: "object"` at the root with `properties`:

```go
import "github.com/agenticenv/agent-sdk-go/pkg/interfaces"

a, _ := agent.NewAgent(
    agent.WithTemporalConfig(...),
    agent.WithLLMClient(...),
    agent.WithResponseFormat(&interfaces.ResponseFormat{
        Type:   interfaces.ResponseFormatJSON,
        Name:   "AgentResponse",
        Schema: interfaces.JSONSchema{
            "type":       "object",
            "properties": interfaces.JSONSchema{
                "response": interfaces.JSONSchema{"type": "string"},
            },
            "required": []any{"response"},
        },
    }),
)
```

[examples/agent_with_json_response](examples/agent_with_json_response) — runnable example: `WithResponseFormat` with `interfaces.ResponseFormat` and `interfaces.JSONSchema` (no tools; validates and pretty-prints JSON on stdout).

**Text explicitly:** Force plain text even if you later add other config:

```go
agent.WithResponseFormat(&interfaces.ResponseFormat{Type: interfaces.ResponseFormatText})
```

**Note:** Structured Outputs (JSON schema) require supported models (e.g. `gpt-4o`, `gpt-4o-mini`). Older models may use JSON mode instead. See your provider docs.

### Reasoning / extended thinking

Use `**Reasoning: &interfaces.LLMReasoning{...}`** on `**WithLLMSampling**` (same struct on `**interfaces.LLMRequest**`). Fields are **generic**; each provider maps them:

- `**Enabled`** — **OpenAI**: does not infer `reasoning_effort` from `**Enabled`** alone (standard chat models reject that parameter). **Anthropic**: if `**BudgetTokens`** is 0, uses **1024** tokens minimum for extended thinking. **Gemini**: helps turn on thought output (`IncludeThoughts`).
- `**Effort`** — **OpenAI** → `reasoning_effort` only when non-empty (use with reasoning-capable models). **Gemini** → `ThinkingLevel` for `low` / `medium` / `high` / `minimal` (only when `**BudgetTokens`** is 0; Gemini forbids budget and level together). **Anthropic** does not use `**Effort`** for its thinking API.
- `**BudgetTokens**` — **Anthropic** extended-thinking budget (≥1024 when non-zero; smaller values are clamped). **Gemini** → `ThinkingBudget` (wins over `**Effort`** → level). **OpenAI** does not use this field.

Streaming still emits `**AgentEventThinkingDelta`** from Anthropic when the API returns thinking deltas.

Runnable example: [examples/agent_with_reasoning](examples/agent_with_reasoning) (`go run ./examples/agent_with_reasoning/` from the repo root; Temporal + `.env` as in [examples/README.md](examples/README.md)).

### Multiple agents

Use `agent.WithInstanceId` when multiple agents share a base TaskQueue:

```go
a1, _ := agent.NewAgent(
    agent.WithTemporalConfig(cfg),
    agent.WithInstanceId("agent-1"),
    ...
)
a2, _ := agent.NewAgent(
    agent.WithTemporalConfig(cfg),
    agent.WithInstanceId("agent-2"),
    ...
)
```

[examples/multiple_agents](examples/multiple_agents)

### Agent and worker in separate processes

Agent process: use `agent.DisableLocalWorker()`. Worker process: use `agent.NewAgentWorker()` with the same config.

```go
// Worker process
w, _ := agent.NewAgentWorker(agent.WithTemporalConfig(...), agent.WithLLMClient(...))
defer w.Close()
go w.Start()

// Agent process
a, _ := agent.NewAgent(
    agent.WithTemporalConfig(...),
    agent.WithLLMClient(...),
    agent.DisableLocalWorker(),
)
result, _ := a.Run(ctx, "Hello", "")
```

[examples/agent_with_worker](examples/agent_with_worker) · [examples/durable_agent](examples/durable_agent)

> **Interactive vs autonomous:** By default the agent uses `AgentModeInteractive`
> (5-minute timeout, worker check enabled). For long-running background agents, set
> `agent.WithAgentMode(agent.AgentModeAutonomous)` to skip the worker check and use a
> 60-minute default timeout. See `[WithAgentMode](#configuration)` for full detail.

### Conversation (message history)

Pass `agent.WithConversation(conv)` to persist message history for multi-turn context. Use `agent.WithConversationSize(n)` to limit how many messages are fetched for LLM context (default 20).

**Conversation ID:** When the agent is configured with a conversation, pass the same `conversationID` to both `Run(ctx, prompt, conversationID)` and `Stream(ctx, prompt, conversationID)` for the same session—so history is shared across turns.

Choose implementation by deployment:


| Deployment                                                           | Use                                                       |
| -------------------------------------------------------------------- | --------------------------------------------------------- |
| **Single process** (agent and worker in same process)                | `inmem.NewInMemoryConversation`                           |
| **Remote workers** (`DisableLocalWorker` or `EnableRemoteWorkers()`) | `redis.NewRedisConversation` or another distributed store |


To add a new conversation store (e.g., Postgres, MongoDB), implement the `interfaces.Conversation` interface in `[pkg/interfaces/conversation.go](pkg/interfaces/conversation.go)`. The interface requires `AddMessage`, `ListMessages`, `Clear`, and `IsDistributed`. See `pkg/conversation/inmem` and `pkg/conversation/redis` for reference.

In-memory cannot be used with remote workers—the agent will return an error at build time.

**Remote workers:** Agent and worker must use the same conversation store (same Redis config) so both processes access the same data. Only the process that calls `Run` or `Stream` passes the conversation ID; the worker does not.

```go
// Single process (default)
conv := inmem.NewInMemoryConversation(inmem.WithMaxSize(100))
a, _ := agent.NewAgent(
    agent.WithTemporalConfig(...),
    agent.WithLLMClient(...),
    agent.WithConversation(conv),
    agent.WithConversationSize(20), // optional; default 20
)
result, _ := a.Run(ctx, "Hello", "session-1")

// Worker process
convW, _ := redis.NewRedisConversation(redis.WithAddr("localhost:6379"))
defer convW.Close()
w, _ := agent.NewAgentWorker(
    agent.WithTemporalConfig(...),
    agent.WithLLMClient(...),
    agent.WithConversation(convW),
)
go w.Start()

// Agent process
convA, _ := redis.NewRedisConversation(redis.WithAddr("localhost:6379"))
defer convA.Close()
a, _ := agent.NewAgent(
    agent.WithTemporalConfig(...),
    agent.WithLLMClient(...),
    agent.DisableLocalWorker(),
    agent.WithConversation(convA),
)
result, _ := a.Run(ctx, "Hello", "session-1")
```

**Lifecycle:** You own the conversation. Call `Clear` when ending a session or when you no longer need the history. The agent never calls `Clear`.

**Example (in-memory, single process):**

```go
import (
    "github.com/agenticenv/agent-sdk-go/pkg/agent"
    "github.com/agenticenv/agent-sdk-go/pkg/conversation/inmem"
)

conv := inmem.NewInMemoryConversation(inmem.WithMaxSize(100))
a, _ := agent.NewAgent(
    agent.WithTemporalConfig(...),
    agent.WithLLMClient(...),
    agent.WithConversation(conv),
    agent.WithConversationSize(20),
)
defer a.Close()

convID := "session-1"
a.Run(ctx, "I'm Alice. Remember that.", convID)
a.Run(ctx, "What's my name?", convID) // agent uses history: "Alice"
```

[examples/agent_with_conversation](examples/agent_with_conversation)

### AG-UI Protocol

Agent stream events follow the [AG-UI open protocol](https://docs.ag-ui.com), making your agents natively compatible with any AG-UI frontend without extra integration work.

Events like `RUN_STARTED`, `TEXT_MESSAGE_CONTENT`, `TOOL_CALL_START`, and `REASONING_MESSAGE_CONTENT` are emitted in the correct AG-UI sequence during every `Stream()` call. Serialize any event with `event.ToJSON()` and forward it over SSE, WebSocket, or Redis to a TypeScript/React frontend using the AG-UI client SDK.

For a complete server + UI reference, see [examples/agent_with_agui](examples/agent_with_agui) (Go SSE server in `server/main.go`, Next.js + CopilotKit bridge in `ui/app/api/copilotkit/route.ts`).

```go
ch, err := a.Stream(ctx, prompt, conversationID)
if err != nil {
    return err
}
for ev := range ch {
    if ev == nil {
        continue
    }
    data, err := ev.ToJSON()
    if err != nil {
        continue
    }
    _ = data // e.g. SSE or WebSocket
}
```

---

## Observability

The SDK emits **traces**, **metrics**, and **logs** via OpenTelemetry. All signals are **no-op by default** — if you set nothing, the agent runs without any overhead. Wire them only when you need them.

### Default: no-op, zero config

No `WithObservabilityConfig`, no `WithTracer`, no `WithMetrics` — the agent uses built-in no-op implementations. No extra imports or init code required.

### Wire OTLP (traces + metrics + logs in one block)

```go
import "github.com/agenticenv/agent-sdk-go/pkg/agent"

a, _ := agent.NewAgent(
    agent.WithTemporalConfig(...),
    agent.WithLLMClient(...),
    agent.WithObservabilityConfig(&agent.ObservabilityConfig{
        Endpoint: "collector:4317",           // gRPC (default) or HTTP URL
        Protocol: agent.OTLPProtocolGRPC,    // or OTLPProtocolHTTP
        Insecure: true,                       // dev only; omit for TLS
        // DisableTraces:  true,             // opt out of traces only
        // DisableMetrics: true,             // opt out of metrics only
        // DisableLogs:    true,             // opt out of OTLP logs only
    }),
)
defer a.Close() // flushes all OTLP signals
```

Use the **same `WithObservabilityConfig`** on `NewAgent` and `NewAgentWorker` so spans and metrics from the worker activities are exported to the same backend.

### Bring your own tracer / metrics

Use `WithTracer` and `WithMetrics` when you already have an OTel provider set up or want a custom implementation. Both accept any value that satisfies `interfaces.Tracer` / `interfaces.Metrics`.

```go
import (
    "github.com/agenticenv/agent-sdk-go/pkg/agent"
    "github.com/agenticenv/agent-sdk-go/pkg/observability"
)

tracer, _ := observability.NewTracer(ctx, "my-service", "collector:4317",
    observability.WithInsecure(),
)
metrics, _ := observability.NewMetrics(ctx, "my-service", "collector:4317",
    observability.WithInsecure(),
)

a, _ := agent.NewAgent(
    agent.WithTemporalConfig(...),
    agent.WithLLMClient(...),
    agent.WithTracer(tracer),
    agent.WithMetrics(metrics),
)
```

### Traces (spans)

| Span | Emitted by |
|---|---|
| `agent.run` | `Agent.Run` / `Agent.RunAsync` |
| `agent.stream` | `Agent.Stream` (dispatch phase) |
| `a2a.execute` | A2A server executor per request |
| `llm.generate` | `AgentLLMActivity` (sync LLM call) |
| `llm.stream` | `AgentLLMStreamActivity` (streaming LLM call) |
| `tool.execute` | `AgentToolExecuteActivity` |
| `tool.authorize` | `AgentToolAuthorizeActivity` |
| `conversation.get_messages` | Fetch conversation history activity |
| `conversation.add_messages` | Persist conversation activity |

Common attributes: `agent.name`, `conversation.id`, `input.length`, `model`, `provider`, `tool`.

### Metrics

All metric names are defined in `internal/types/metrics.go`.

**Agent API** (emitted by `Agent.Run` / `Agent.Stream`):

| Metric | Kind | Description |
|---|---|---|
| `agent.run.started` | counter | Each `Run` / `RunAsync` invocation |
| `agent.run.completed` | counter | Successful run |
| `agent.run.failed` | counter | Failed run (includes error attribute) |
| `agent.run.duration_ms` | histogram | Run wall-clock time in ms |
| `agent.stream.started` | counter | Each `Stream` invocation |
| `agent.stream.dispatched` | counter | Workflow successfully dispatched |
| `agent.stream.failed` | counter | Dispatch failed |
| `agent.stream.duration_ms` | histogram | Dispatch wall-clock time in ms |

**Runtime** (emitted by Temporal activities; attributes: `model`, `provider`, `tool`):

| Metric | Kind | Description |
|---|---|---|
| `agent.llm.call.started` | counter | LLM call started |
| `agent.llm.call.completed` | counter | LLM call succeeded |
| `agent.llm.call.failed` | counter | LLM call failed |
| `agent.llm.latency_ms` | histogram | LLM wall-clock time in ms |
| `agent.llm.tokens.input` | histogram | Prompt tokens (when provider reports usage) |
| `agent.llm.tokens.output` | histogram | Completion tokens (when provider reports usage) |
| `agent.tool.call.started` | counter | Tool execute started |
| `agent.tool.call.completed` | counter | Tool execute succeeded |
| `agent.tool.call.failed` | counter | Tool execute failed |
| `agent.tool.latency_ms` | histogram | Tool wall-clock time in ms |

### Logs

Structured logs (`Info`, `Debug`, `Warn`, `Error`) are emitted throughout the agent lifecycle and workflow execution. The default logger writes to stderr. Use `WithLogger` to inject your own, `WithLogLevel` to set the level (`debug`, `info`, `warn`, `error`).

When `WithObservabilityConfig` is set (and `DisableLogs` is not true), SDK logs are also exported to your OTLP backend automatically — no extra `WithLogger` call needed.

```go
agent.WithLogLevel("debug") // show all SDK internal log lines
```

---

## Configuration

A Temporal connection (`WithTemporalConfig` or `WithTemporalClient`) is **optional** — omit it to use the **local runtime** (in-process, no Temporal). Set it to use the **Temporal runtime** (durable execution). All other options work the same on both runtimes.

- **WithTemporalConfig**: Temporal connection (Host, Port, Namespace, TaskQueue). Use for simple setups. See [Temporal connection](#temporal-connection).
- **WithTemporalClient**: Pre-configured Temporal client. Use for TLS, API key auth, Temporal Cloud. Requires `WithTaskQueue`. Agent does not close the client.
- **WithTaskQueue**: Task queue name. Required when using `WithTemporalClient`. Ignored when using `WithTemporalConfig`.
- **WithResponseFormat**: LLM response format. Omit for text-only. Use `&interfaces.ResponseFormat{Type, Name, Schema}` for JSON with schema. See [Response format](#response-format).
- **WithConversation**: Message history store. Use `inmem` for single process; `redis` for remote workers. Pass same `conversationID` to `Run` and `Stream` for a session. See [Conversation](#conversation-message-history).
- **WithConversationSize**: Max messages to fetch for LLM context (default 20). Only applies when `WithConversation` is set.
- **EnableRemoteWorkers**: Pass `EnableRemoteWorkers()` when using `DisableLocalWorker` with approval or streaming (starts the event worker/workflow path).
- **WithSubAgents**: Attach specialist agents the main agent can delegate to. Each needs its own task queue and worker. See [Sub-agents](#sub-agents).
- **WithMaxSubAgentDepth**: Maximum delegation hops from this agent (default 2). See [Sub-agents](#sub-agents).
- **WithMaxIterations**: Max LLM rounds (default 5).
- **WithStream**: Enable `Stream` partial content streaming.
- **Token usage:** Not a separate option. On `**Run`**, read `**Usage**` on `**[*AgentRunResult](pkg/agent/agent.go)**` when set. On `**Stream**`, assert `**[*AgentRunFinishedEvent](pkg/agent/agent.go)**` with `**[*AgentRunResult](pkg/agent/agent.go)**` in `**Result**` (aggregate across LLM/tool rounds when the provider reports it). See [Token usage](#token-usage-llmusage).
- **WithLLMSampling**: Pass `&agent.LLMSampling{...}`; nil or zero fields leave that knob to the provider default. Which fields apply where:
  - `**Temperature`** — OpenAI, Anthropic, Gemini.
  - `**MaxTokens**` — OpenAI, Anthropic, Gemini (max output / completion tokens).
  - `**TopP**` — OpenAI, Gemini only (not sent to Anthropic).
  - `**TopK**` — Anthropic only (not sent to OpenAI or Gemini).
  - `**Reasoning**` (`*interfaces.LLMReasoning`) — optional generic controls; each LLM client maps them:
    - `**Enabled**` — requests reasoning/thinking on providers that support it (see below).
    - `**Effort**` — `"none"` … `"xhigh"`; **OpenAI** → `reasoning_effort` when non-empty; **Gemini** → `ThinkingLevel` when `low` / `medium` / `high` / `minimal`; **Anthropic** ignores (use `**BudgetTokens`** for extended thinking).
    - `**BudgetTokens**` — **Anthropic** extended-thinking budget (non-zero; SDK clamps below 1024 to 1024); **Gemini** `thinkingBudget` (if set, `**Effort`** is not sent as `ThinkingLevel`—Gemini allows only one of budget or level); **OpenAI** ignores.
- **WithApprovalTimeout**: Max wait per tool approval; must be less than agent timeout. Defaults to timeout−30s when tools require approval. Capped at 31 days.
- **WithAgentMode**: Sets the agent mode. Default timeout when neither `ctx` nor `WithTimeout` is set follows the mode (**5 minutes** / **60 minutes**). Worker-check and queueing behaviour below are **mostly relevant to the Temporal runtime** (this SDK’s execution backend).
  - `**AgentModeInteractive` (default)** — Optimised for chat, REPL, and web apps where a human is waiting. When `DisableLocalWorker` is set, the agent checks for available external workers before submitting work — if no worker is ready within the check window, it returns a clear error immediately rather than leaving the user with a hanging prompt. Default timeout is **5 minutes**.
  - `**AgentModeAutonomous`** — Optimised for background jobs, pipelines, and long-running tasks where no human is watching. The worker check is skipped entirely — if no worker is available, Temporal queues the workflow and waits for one naturally. Default timeout is **60 minutes**.

**Env config:** [examples/README.md](examples/README.md) for examples; [cmd/README.md](cmd/README.md) for CLI.

---

## Development

Contributors: see **[CONTRIBUTING.md](CONTRIBUTING.md)** for prerequisites (Go, Temporal setup, workflow, and guidelines).
Project policies: **[SECURITY.md](SECURITY.md)** for vulnerability reporting and **[CODE_OF_CONDUCT.md](CODE_OF_CONDUCT.md)** for community standards.

Quick commands: `make test` | `make lint` | `make fmt` | `make spell` | `make tidy` | `make test-coverage` (`make lint` runs `gofmt -s`, `misspell`, then `go vet` + `golangci-lint`)

## Code Coverage

Coverage reports (PR and default branch) are on **[Codecov](https://app.codecov.io/gh/agenticenv/agent-sdk-go)**. Locally, run `make test-coverage` to produce `coverage.out` and `coverage.html`.

---

## Setup and run examples

> Note: Meet **[Getting Started — Prerequisites](#prerequisites)** (Go, LLM, Temporal). For installing or running a Temporal server (Docker, CLI, Cloud, self-hosted), use **[Temporal setup](temporal-setup.md)**. Then:

```bash
git clone <repo-url>
cd agent-sdk-go
cp examples/env.sample examples/.env
# Edit examples/.env: set LLM_APIKEY, LLM_MODEL
```

See **[examples/README.md](examples/README.md)** for how to run examples, env vars ([examples/env.sample](examples/env.sample)), and optional **README.md** files inside specific example directories.

### CLI configuration

The CLI uses a YAML config file. Copy the sample and add your values:

```bash
cp cmd/config.sample.yaml cmd/config.yaml
# Edit cmd/config.yaml: set llm.apiKey (or use AGENT_LLM_APIKEY env var)
go run ./cmd
```

Or run with a custom config path: `go run ./cmd -config /path/to/config.yaml`.

- **config.sample.yaml** — template in the repo (safe to commit)
- **config.yaml** — your config (gitignored; copy from sample)
- **Env overrides** — `AGENT_LLM_APIKEY`, `AGENT_TEMPORAL_HOST`, etc. override file values

See **[cmd/README.md](cmd/README.md)** for CLI details and env vars.

## Production Readiness Checklist

- **Run and approval limits** — Use `WithTimeout` and/or a context deadline on `Run` / `Stream`; use `WithApprovalTimeout` when tools require approval (activity retry counts inside workflows are fixed in the SDK, not user-tunable).
- **Bound agent loops** — Set `WithMaxIterations` and, if you use sub-agents, `WithMaxSubAgentDepth`.
- **Tool and delegation risk** — Choose `WithToolApprovalPolicy` per agent (main and specialists); use human review for dangerous tools, MCP-exposed capabilities, and delegation where policy requires it.
- **MCP** — Remote servers widen your tool surface; prefer TLS in production; avoid `SkipTLSVerify` outside local dev; protect bearer tokens, OAuth secrets, and header-based credentials.
- **Split processes** — If you use `DisableLocalWorker` or `EnableRemoteWorkers()`, use a distributed conversation store (e.g. Redis) and exercise approval/streaming paths in integration tests.
- **Secrets and data** — Keep LLM and Temporal credentials out of source control; treat tool arguments and model output as untrusted in your app.
- **LLM safety** — Validate and sanitize prompts, tool args, and model output at your integration boundary.
- **Observability** — Wire `WithObservabilityConfig` (or `WithTracer` / `WithMetrics`) on both `NewAgent` and `NewAgentWorker` so all signals reach your backend. Confirm your OTLP collector is reachable before deploying; use `Insecure: true` only in dev.
- **Operations** — Use your logger (`WithLogger` / `WithLogLevel`) and the Temporal UI/history for a given run; after upgrading this module, confirm workflows still replay in your environment.

---

## Disclaimer

This project is provided "as is" under the Apache License 2.0. When building AI agents that execute real-world actions, ensure appropriate safeguards, validation, and human-in-the-loop approval workflows are in place. You are responsible for compliance, access control, and operational safety in your deployment. For security issues, follow **[SECURITY.md](SECURITY.md)**.
