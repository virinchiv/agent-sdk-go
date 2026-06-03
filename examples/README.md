# Examples

These programs exercise **agent-sdk-go** (`github.com/agenticenv/agent-sdk-go`). By default examples run on the **local** runtime (in-process, no external services). Set `AGENT_RUNTIME=temporal` in `.env` for durable Temporal execution.

## Runtime

| Mode | How to enable | Requirement |
|------|--------------|-------------|
| `local` (default) | `AGENT_RUNTIME=local` (or unset) | Nothing — runs in-process |
| `temporal` | `AGENT_RUNTIME=temporal` | Running Temporal server — see **[Temporal setup](../temporal-setup.md)** |

When using Temporal the examples read `TEMPORAL_HOST`, `TEMPORAL_PORT`, and `TEMPORAL_NAMESPACE` from `.env` (default: localhost, 7233, default).

## Examples overview

### Works with both runtimes

These examples run with `AGENT_RUNTIME=local` (default) or `AGENT_RUNTIME=temporal`. No Temporal server needed to try them.

| Example | What it demonstrates |
|---------|---------------------|
| `simple_agent` | Minimal agent, no tools — system prompt, LLM client, single `Run()`; prints `AgentResponse.Usage` (token counts) when the provider reports them |
| `agent_with_conversation` | In-memory conversation with `WithConversation` — multi-turn context, same `conversationID` for `Run` |
| `agent_with_tools/basic` | Built-in tools (echo, calculator, weather, wikipedia, search) with auto-approval |
| `agent_with_tools/approval` | Tools + `WithApprovalHandler` — user approves or rejects each tool run (`Run` only) |
| `agent_with_tools/authorizer` | Custom tool authorization via `interfaces.ToolAuthorizer` — denied calls surface as `tool_result` with `denied` status |
| `agent_with_tools/custom` | Custom tools via `WithTools` — implementing `interfaces.Tool` |
| `agent_with_stream` | Streaming with `Stream` — **`TEXT_MESSAGE_*`**, **`TOOL_CALL_*`**, **`RUN_FINISHED`**; prints token usage from **`RUN_FINISHED`** result when present |
| `agent_with_agui` | Go **`POST /agui` SSE** + **Next.js + CopilotKit** ([`agent_with_agui/README.md`](agent_with_agui/README.md)) — two processes: agent server, then `ui/` dev server |
| `agent_with_stream_conversation` | Stream + conversation; avoid printing the same text twice (**`TEXT_MESSAGE_CONTENT`** deltas vs **`RUN_FINISHED`** body) |
| `agent_with_run_async` | `RunAsync` — `resultCh` + `approvalCh`; use `req.Respond` (no `WithApprovalHandler`) |
| `multiple_agents` | Multiple agents with `WithInstanceId` — sequential or concurrent |
| `agent_with_subagents` | Main agent + math specialist — `WithSubAgents`; prints **`STEP_STARTED` / `STEP_FINISHED`** (sub-agent name) around each child run when using `Stream` |
| `agent_with_json_response` | Structured LLM output — `WithResponseFormat` + `interfaces.JSONSchema` (JSON with schema; no tools) |
| `agent_with_reasoning` | Generic `interfaces.LLMReasoning` via `WithLLMSampling` — `Stream` to observe `thinking_delta` (e.g. Anthropic) |
| `agent_with_mcp_config` | MCP via `WithMCPConfig` — transport from env; see **`env.sample`** — **[README](agent_with_mcp_config/README.md)** (testing & sample servers) |
| `agent_with_mcp_client` | Same as above via `mcpclient.NewClient` + `WithMCPClients` — **[README](agent_with_mcp_client/README.md)** |
| `agent_with_a2a_config` | Outbound A2A via `WithA2AConfig` — **`A2A_URL`** etc.; **[README](agent_with_a2a_config/README.md)** |
| `agent_with_a2a_client` | Same env, explicit **`pkg/a2a/client`** — see `agent_with_a2a_config` for setup |
| `agent_with_a2a_server` | **Inbound** A2A server — **`A2A_SERVER_*`**; **[README](agent_with_a2a_server/README.md)** (curl, **`a2a` CLI**, client example) |
| `agent_with_observability` | OpenTelemetry OTLP exports — **`config/`** ([`WithObservabilityConfig`](../pkg/agent/config.go)) vs **`objects/`** (pre-built tracer/metrics); **[README](agent_with_observability/README.md)** (collector endpoint) |
| `agent_with_retriever` | Vector retrievers — **`weaviate/`** or **`pgvector/`** backends; modes **`agentic`**, **`prefetch`**, **`hybrid`** via **`RETRIEVER_MODE`** — **[README](agent_with_retriever/README.md)** |

### Temporal only

These examples **always require** `AGENT_RUNTIME=temporal` and a running Temporal server. They demonstrate features that are only meaningful with durable workflow execution.

| Example | What it demonstrates |
|---------|---------------------|
| `agent_with_temporal_client` | Caller-owned Temporal client — `WithTemporalClient` + `WithTaskQueue`; create and close client yourself (TLS, API key, Cloud) |
| `agent_with_worker` | Agent and worker in **separate processes** — `DisableLocalWorker` + `NewAgentWorker`; agent uses **`Stream`** |
| `durable_agent` | Same split-process layout with durability scenarios — workflow replay, mid-run failures; **[README](durable_agent/README.md)** |

## Setup

```bash
cp env.sample .env
# Edit .env: set LLM_APIKEY, LLM_MODEL (see LLM_PROVIDER: openai, anthropic, or gemini)
# Default runtime is local — set AGENT_RUNTIME=temporal to use Temporal
```

## Run examples

### Minimal agent (no tools)

```bash
go run ./simple_agent "Hello, what can you do?"
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
go run ./agent_with_tools/basic "What's the weather in Tokyo?"
go run ./agent_with_tools/approval "What is 15 + 27?"
go run ./agent_with_tools/authorizer "Get the protected note for roadmap."
go run ./agent_with_tools/custom "Reverse 'hello world'"
```

### Streaming (partial content as tokens arrive)

```bash
go run ./agent_with_stream "What's the current time and what's 17 * 23?"
```

### AG-UI / CopilotKit (`agent_with_agui`)

Go SSE server + Next.js frontend. Two processes:

```bash
# Terminal 1: Go agent server (listens on :8787)
go run ./agent_with_agui/server

# Terminal 2: Next.js UI
cd agent_with_agui/ui && npm install && npm run dev
```

See **[agent_with_agui/README.md](agent_with_agui/README.md)** for curl testing and UI setup.

### Structured JSON response (`WithResponseFormat`)

```bash
go run ./agent_with_json_response
go run ./agent_with_json_response "What is the capital of Japan?"
```

### Reasoning / thinking (`WithLLMSampling` + `LLMReasoning`)

```bash
go run ./agent_with_reasoning
go run ./agent_with_reasoning "Why is the sky blue? One short paragraph."
```

### Streaming + conversation (event handling pattern)

```bash
go run ./agent_with_stream_conversation
go run ./agent_with_stream_conversation "What is 5 * 8?"
```

### Sub-agents (main agent + specialist)

```bash
go run ./agent_with_subagents "What is 987 times 654?"
```

### RunAsync + multiple agents

```bash
go run ./agent_with_run_async "What is 15 + 27?"
go run ./multiple_agents "What is 7 times 8?"
go run ./multiple_agents concurrent "What is 7 times 8?"
```

### MCP (`agent_with_mcp_config`, `agent_with_mcp_client`)

Same **`MCP_*`** env (see **`env.sample`**); differs only in **`WithMCPConfig`** vs **`mcpclient.NewClient`** + **`WithMCPClients`**.

```bash
go run ./agent_with_mcp_config
go run ./agent_with_mcp_config "List tools you can call."
go run ./agent_with_mcp_client
go run ./agent_with_mcp_client "List tools you can call."
```

**Configure transports, test against real MCP servers:** **[agent_with_mcp_config/README.md](agent_with_mcp_config/README.md)**.

### A2A client (`agent_with_a2a_config`, `agent_with_a2a_client`)

Outbound A2A tools — set **`A2A_URL`** (and optional **`A2A_*`** in **`env.sample`**).

```bash
go run ./agent_with_a2a_config
go run ./agent_with_a2a_config "What tools do you have available?"
go run ./agent_with_a2a_client
go run ./agent_with_a2a_client "What tools do you have available?"
```

**Run a sample remote agent, curl checks:** **[agent_with_a2a_config/README.md](agent_with_a2a_config/README.md)**.

### A2A server (`agent_with_a2a_server`)

Inbound JSON-RPC server — **`A2A_SERVER_*`**, optional bearer tokens.

```bash
go run ./agent_with_a2a_server
```

**curl, `a2a` CLI, testing with `agent_with_a2a_config`:** **[agent_with_a2a_server/README.md](agent_with_a2a_server/README.md)**.

### Observability OTLP (`agent_with_observability`)

Requires a reachable OTLP **collector** (**`OTEL_EXPORTER_OTLP_ENDPOINT`**, typically **`localhost:4317`** for gRPC or **`localhost:4318`** for HTTP).

```bash
go run ./agent_with_observability/config/
go run ./agent_with_observability/objects/
```

Details and collector notes: **[agent_with_observability/README.md](agent_with_observability/README.md)**.

### Vector retriever (`agent_with_retriever`)

Requires a running vector store (Weaviate **or** Postgres with pgvector). Set backend-specific vars in **`env.sample`**.

```bash
# Weaviate (run ./agent_with_retriever/weaviate/setup.sh; ./cleanup.sh when done)
go run ./agent_with_retriever/weaviate "What is the return policy?"

# pgvector (run ./agent_with_retriever/pgvector/setup.sh; ./cleanup.sh when done)
go run ./agent_with_retriever/pgvector "What is the return policy?"

RETRIEVER_MODE=prefetch go run ./agent_with_retriever/weaviate "What are the return and shipping rules?"
```

Setup guides: **[agent_with_retriever/README.md](agent_with_retriever/README.md)**.

---

### Temporal-only examples

> These require `AGENT_RUNTIME=temporal` and a running Temporal server.

#### Caller-owned Temporal client

Creates and manages the Temporal client directly — for TLS, Temporal Cloud API keys, or custom connection options.

```bash
AGENT_RUNTIME=temporal go run ./agent_with_temporal_client "Hello, what can you do?"
```

#### Agent + worker in separate processes (`agent_with_worker`)

```bash
AGENT_RUNTIME=temporal go run ./agent_with_worker/worker    # terminal 1: worker
AGENT_RUNTIME=temporal go run ./agent_with_worker/agent "Hello from remote agent!"   # terminal 2: agent
```

#### Durable agent — workflow replay and failure scenarios (`durable_agent`)

```bash
AGENT_RUNTIME=temporal go run ./durable_agent/worker       # terminal 1
AGENT_RUNTIME=temporal go run ./durable_agent/agent "Hello from remote agent!"   # terminal 2
```

See **[durable_agent/README.md](durable_agent/README.md)** for durability and failure scenarios.

---

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
| `AGENT_RUNTIME` | `local` (default) or `temporal` — selects the execution backend |
| `TEMPORAL_HOST`, `TEMPORAL_PORT`, `TEMPORAL_NAMESPACE`, `TEMPORAL_TASKQUEUE` | Temporal connection (used when `AGENT_RUNTIME=temporal`) |
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
| `OTEL_EXPORTER_OTLP_ENDPOINT` | **Required** for **`agent_with_observability`**: OTLP collector **`host:port`** (no `http://` scheme), e.g. **`localhost:4317`** (gRPC) or **`localhost:4318`** (HTTP) |
| `OTLP_PROTOCOL` | Optional: **`grpc`** (default) or **`http`** — must match how the collector listens |
| `OTLP_INSECURE` | Optional: **`true`** for plaintext export (typical for local collectors without TLS) |
| `RETRIEVER_MODE` | For **`agent_with_retriever`**: **`agentic`** (default), **`prefetch`**, or **`hybrid`** |
| `WEAVIATE_HOST`, `WEAVIATE_SCHEME`, `WEAVIATE_CLASS`, … | Weaviate backend — see **`env.sample`** and **[agent_with_retriever/weaviate/README.md](agent_with_retriever/weaviate/README.md)** |
| `PGVECTOR_DSN`, `PGVECTOR_TABLE`, `EMBEDDING_MODEL`, … | pgvector backend — **`PGVECTOR_DSN` required**; see **[agent_with_retriever/pgvector/README.md](agent_with_retriever/pgvector/README.md)** |
