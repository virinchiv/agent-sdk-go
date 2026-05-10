# Examples

These programs exercise **agent-sdk-go** (`github.com/agenticenv/agent-sdk-go`). Agents run as Temporal workflows, so a running Temporal service is mandatory for every example below.

**Prerequisite:** These examples use **agent-sdk-go** on the **Temporal** runtime. A **running Temporal server** is required before you run them. See **[Temporal setup](../temporal-setup.md)** for Docker, Temporal CLI, ports, Cloud, and self-hosted options.

## Default connection

The examples use `TEMPORAL_HOST`, `TEMPORAL_PORT`, and `TEMPORAL_NAMESPACE` from `.env` (default: localhost, 7233, default). Adjust if your Temporal runs elsewhere.

## Examples overview

| Example | What it demonstrates |
|---------|---------------------|
| `simple_agent` | Minimal agent, no tools — Temporal config, system prompt, LLM client, single `Run()`; prints `AgentResponse.Usage` (token counts) when the provider reports them |
| `agent_with_temporal_client` | Caller-owned Temporal client — `WithTemporalClient` + `WithTaskQueue`; create and close client yourself (TLS, API key, Cloud) |
| `agent_with_conversation` | In-memory conversation with `WithConversation` — multi-turn context, same `conversationID` for `Run` |
| `agent_with_tools` | Built-in tools (echo, calculator, weather, wikipedia, search) with auto-approval |
| `agent_with_stream` | Streaming with `Stream` — **`TEXT_MESSAGE_*`**, **`TOOL_CALL_*`**, **`RUN_FINISHED`**; prints token usage from **`RUN_FINISHED`** result when present |
| `agent_copilotkit` | Go **`POST /agui` SSE** + **Next.js + CopilotKit** ([`agent_copilotkit/README.md`](agent_copilotkit/README.md)) — two processes: agent server, then `ui/` dev server |
| `agent_with_stream_conversation` | Stream + conversation; avoid printing the same text twice (**`TEXT_MESSAGE_CONTENT`** deltas vs **`RUN_FINISHED`** body) |
| `agent_with_tools_approval` | Tools + `WithApprovalHandler` — user approves or rejects each tool run (Run only) |
| `agent_with_run_async` | `RunAsync` — `resultCh` + `approvalCh`; use `req.Respond` (no `WithApprovalHandler`) |
| `agent_with_custom_tools` | Custom tools via `WithTools` — implementing `interfaces.Tool` |
| `agent_with_tool_authorizer` | Custom tool authorization via `interfaces.ToolAuthorizer` — denied calls surface as `tool_result` with `denied` status |
| `multiple_agents` | Multiple agents with `WithInstanceId` — sequential or concurrent |
| `agent_with_subagents` | Main agent + math specialist — `WithSubAgents`, separate task queues; prints **`STEP_STARTED` / `STEP_FINISHED`** (sub-agent name) around each child run when using `Stream` |
| `agent_with_json_response` | Structured LLM output — `WithResponseFormat` + `interfaces.JSONSchema` (JSON with schema; no tools) |
| `agent_with_reasoning` | Generic `interfaces.LLMReasoning` via `WithLLMSampling` — `Stream` to observe `thinking_delta` (e.g. Anthropic) |
| `agent_with_worker` | Agent and worker in separate processes — `DisableLocalWorker` + `NewAgentWorker`; agent uses **`Stream`** |
| `durable_agent` | Same split-process layout — agent uses **`Stream`** (`WithStream`); durability scenarios: [`durable_agent/README.md`](durable_agent/README.md) |
| `agent_with_mcp_config` | MCP via `WithMCPConfig` — transport from env; see **`env.sample`** — **[README](agent_with_mcp_config/README.md)** (testing & sample servers) |
| `agent_with_mcp_client` | Same as above via `mcpclient.NewClient` + `WithMCPClients` — **[README](agent_with_mcp_client/README.md)** |
| `agent_with_a2a_config` | Outbound A2A via `WithA2AConfig` — **`A2A_URL`** etc.; **[README](agent_with_a2a_config/README.md)** |
| `agent_with_a2a_client` | Same env, explicit **`pkg/a2a/client`** — **[README](agent_with_a2a_client/README.md)** |
| `agent_with_a2a_server` | **Inbound** A2A server — **`A2A_SERVER_*`**; **[README](agent_with_a2a_server/README.md)** (curl, **`a2a` CLI**, client example) |

## Setup

```bash
cp env.sample .env
# Edit .env: set LLM_APIKEY, LLM_MODEL (see LLM_PROVIDER: openai, anthropic, or gemini)
```

## Run examples

### Minimal agent (no tools)

```bash
go run ./simple_agent "Hello, what can you do?"
```

### Agent with caller-owned Temporal client

Uses `WithTemporalClient` and `WithTaskQueue`. The example creates the Temporal client, passes it to the agent, and closes it when done. Use this pattern for TLS, Temporal Cloud API keys, or other connection options.

```bash
go run ./agent_with_temporal_client "Hello, what can you do?"
```

### Agent with conversation (multi-turn)

Uses in-memory conversation. Run **interactive mode** (no args) for multi-turn in one process—history is shared across turns. With args, runs a single turn (useful for testing).

```bash
# Interactive: type prompts, get responses; history shared. Type 'exit' to end.
go run ./agent_with_conversation

# Single turn (new process each run; no shared history)
go run ./agent_with_conversation "Hello, remember I'm Alice"
```

### Agent with tools

```bash
go run ./agent_with_tools "What's the weather in Tokyo?"
```

### Streaming (partial content as tokens arrive)

```bash
go run ./agent_with_stream "What's the current time and what's 17 * 23?"
```

### Structured JSON response (`WithResponseFormat`)

Uses `agent.WithResponseFormat` with `interfaces.ResponseFormatJSON`, `Name`, and `interfaces.JSONSchema`. No tools—keeps the run in structured-output mode. Prints validated, indented JSON.

```bash
go run ./agent_with_json_response
go run ./agent_with_json_response "What is the capital of Japan?"
```

### Reasoning / thinking (`WithLLMSampling` + `LLMReasoning`)

Sets `WithLLMSampling` with `Reasoning: &interfaces.LLMReasoning{Enabled, Effort, BudgetTokens}` and uses **`Stream`** so you can see **`thinking_delta`** events when the provider emits them (e.g. Anthropic extended thinking). Pick a model that supports reasoning/thinking for your `LLM_PROVIDER`.

```bash
go run ./agent_with_reasoning
go run ./agent_with_reasoning "Why is the sky blue? One short paragraph."
```

### Streaming + conversation (event handling pattern)

Interactive multi-turn with `Stream`. Uses **`AgentEventTypeTextMessageContent`** (deltas) and **`AgentEventTypeRunFinished`** (final body) so the same answer is not printed twice.

```bash
go run ./agent_with_stream_conversation
go run ./agent_with_stream_conversation "What is 5 * 8?"
```

### Sub-agents (main agent + specialist)

Two agents in one process: main agent with a math specialist registered via `WithSubAgents`. Requires workers on **both** task queues (each `NewAgent` starts its own embedded worker). Main agent uses default tool approval (**RequireAll**): delegating to the specialist prompts on **stdin** (`y` / `n`). Specialist uses **AutoToolApprovalPolicy** so calculator does not prompt. Same stdin pattern as [agent_with_tools_approval](#tools--approval-custom-tools-multiple-agents-worker-split).

```bash
go run ./agent_with_subagents "What is 987 times 654?"
```

### Tools + approval, custom tools, multiple agents, worker split

```bash
go run ./agent_with_tools_approval "What is 15 + 27?"
go run ./agent_with_run_async "What is 15 + 27?"
go run ./agent_with_custom_tools "Reverse 'hello world'"
go run ./agent_with_tool_authorizer "Get the protected note for roadmap."
go run ./multiple_agents "What is 7 times 8?"
go run ./multiple_agents concurrent "What is 7 times 8?"

# Agent and worker in separate processes: two terminals — worker in terminal 1,
# agent in terminal 2 (after the worker is up).
go run ./agent_with_worker/worker    # terminal 1: worker
go run ./agent_with_worker/agent "Hello from remote agent!"   # terminal 2: agent

# durable_agent: same two-terminal flow; streaming REPL. Scenarios:
# durable_agent/README.md
go run ./durable_agent/worker       # terminal 1
go run ./durable_agent/agent "Hello from remote agent!"   # terminal 2
```

### MCP (`agent_with_mcp_config`, `agent_with_mcp_client`)

Same **`MCP_*`** env (see **`env.sample`**); differs only in **`WithMCPConfig`** vs **`mcpclient.NewClient`** + **`WithMCPClients`**.

```bash
go run ./agent_with_mcp_config
go run ./agent_with_mcp_config "List tools you can call."
go run ./agent_with_mcp_client
go run ./agent_with_mcp_client "List tools you can call."
```

**Configure transports, test against real MCP servers (streamable HTTP walkthrough, stdio, links):** **[agent_with_mcp_config/README.md](agent_with_mcp_config/README.md)**.

### A2A client (`agent_with_a2a_config`, `agent_with_a2a_client`)

Outbound A2A tools — set **`A2A_URL`** (and optional **`A2A_*`** in **`env.sample`**).

```bash
go run ./agent_with_a2a_config
go run ./agent_with_a2a_config "What tools do you have available?"
go run ./agent_with_a2a_client
go run ./agent_with_a2a_client "What tools do you have available?"
```

**Run a sample remote agent (e.g. `a2a-samples` helloworld), curl checks:** **[agent_with_a2a_config/README.md](agent_with_a2a_config/README.md)**.

### A2A server (`agent_with_a2a_server`)

Inbound JSON-RPC server — **`A2A_SERVER_*`**, optional bearer tokens.

```bash
go run ./agent_with_a2a_server
```

**curl, `a2a` CLI, testing with `agent_with_a2a_config`:** **[agent_with_a2a_server/README.md](agent_with_a2a_server/README.md)**.

## Logging

Examples send conversation (user prompt, assistant response) to **stdout** and internal logs to **stderr**. By default only errors are logged.

- **See logs while evaluating:** Set `LOG_LEVEL=info` or `LOG_LEVEL=debug` in `.env`, or run:
  ```bash
  LOG_LEVEL=debug go run ./simple_agent "Hello, what can you do?"
  ```
- **Save logs to a file:** Redirect stderr to a file:
  ```bash
  LOG_LEVEL=info go run ./simple_agent "Hello" 2>debug.log
  ```
- **Suppress logs:** Show only conversation output:
  ```bash
  go run ./simple_agent "Hello" 2>/dev/null
  ```

## Env vars

| Env var | Description |
|---------|-------------|
| `TEMPORAL_HOST`, `TEMPORAL_PORT`, `TEMPORAL_NAMESPACE`, `TEMPORAL_TASKQUEUE` | Temporal connection |
| `LLM_PROVIDER` | `openai`, `anthropic`, or `gemini` (see `env.sample`) |
| `LLM_APIKEY` | API key |
| `LLM_MODEL` | e.g. `gpt-4o`, `claude-3-5-sonnet-20241022` |
| `LLM_BASEURL` | Optional (custom/proxy endpoints) |
| `LOG_LEVEL` | `error` (default), `warn`, `info`, `debug` — logs go to stderr |
| `SERPER_API_KEY` | For search tool |
| `MCP_TRANSPORT` | **Required** for MCP examples: `stdio` or `streamable_http` (aliases: `local`, `http`, `remote`, …) |
| `MCP_SERVER_NAME` | Optional server id for wiring (defaults: `local` for stdio, `remote` for HTTP) |
| `MCP_STREAMABLE_HTTP_URL` | Remote MCP base URL (required for `streamable_http`) |
| `MCP_STDIO_COMMAND` | Executable for local subprocess MCP (required for `stdio`) |
| `MCP_STDIO_ARGS` | Optional JSON array of argv strings, e.g. `["-y","@scope/pkg","/dir"]` |
| `MCP_STDIO_ENV` | Optional JSON object of extra subprocess env vars |
| `MCP_BEARER_TOKEN` | Optional static bearer for MCP HTTP; ignored when OAuth env trio is all set |
| `MCP_TIMEOUT_SECONDS` | Optional; positive seconds cap MCP connect+RPC timeout |
| `MCP_RETRY_ATTEMPTS` | Optional; max attempts per MCP operation when > 0 |
| `MCP_ALLOW_TOOLS`, `MCP_BLOCK_TOOLS` | Optional comma-separated allow/block tool lists (mutually exclusive) |
| `MCP_CLIENT_ID`, `MCP_CLIENT_SECRET`, `MCP_TOKEN_URL` | Optional together: OAuth2 client credentials for MCP HTTP transport |
| `MCP_SKIP_TLS_VERIFY` | Optional; set to `true` to skip TLS verify for MCP/token HTTP (dev only) |
| `A2A_URL` | **Required** for A2A examples: remote agent base URL |
| `A2A_SERVER_NAME` | Optional connection id (default: `remote`) — used in tool names |
| `A2A_TIMEOUT_SECONDS` | Optional; positive seconds cap per A2A HTTP operation |
| `A2A_TOKEN` | Optional static bearer for the A2A HTTP client |
| `A2A_HEADERS` | Optional JSON object of extra HTTP headers |
| `A2A_SKIP_TLS_VERIFY` | Optional; `true` skips TLS verification for A2A HTTP (dev only) |
| `A2A_ALLOW_SKILLS`, `A2A_BLOCK_SKILLS` | Optional comma-separated allow/block skill ID lists (mutually exclusive) |
| `A2A_SERVER_HOST` | Optional bind hostname for **`agent_with_a2a_server`** (empty → default **localhost**) |
| `A2A_SERVER_PORT` | Optional TCP port for **`agent_with_a2a_server`** (0 → default **9999**) |
| `A2A_SERVER_BEARER_TOKENS` | Optional comma-separated bearer secrets for inbound JSON-RPC on **`agent_with_a2a_server`** |
