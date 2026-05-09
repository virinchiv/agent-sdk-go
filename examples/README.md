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
| `agent_with_mcp_config` | MCP via `WithMCPConfig` — transport from env: `mcp.MCPStdio` (command, JSON args/env) or `mcp.MCPStreamableHTTP` (URL, optional bearer/OAuth); see `examples/env.sample` |
| `agent_with_mcp_client` | Same transports via `mcpclient.NewClient` + `WithMCPClients`; same env vars as `agent_with_mcp_config` |
| `agent_with_a2a_config` | A2A via `WithA2AConfig` — SDK builds the default A2A client from **`A2A_URL`** (and optional auth/filter); see **`env.sample`** |
| `agent_with_a2a_client` | Same env as `agent_with_a2a_config`, but wires **`pkg/a2a/client.NewClient`** + `WithA2AClients` |

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

### MCP (`WithMCPConfig` vs `WithMCPClients`)

Two examples use the **same env-driven transport** but wire the agent differently:

- **`agent_with_mcp_config`** — `agent.WithMCPConfig(agent.MCPServers{<serverName>: mcpCfg})`. The SDK builds the default MCP client per server.
- **`agent_with_mcp_client`** — `mcpclient.NewClient(<serverName>, transport, opts...)` then `agent.WithMCPClients(client)`.

**Transport** must be set explicitly with **`MCP_TRANSPORT`**: `stdio` or `streamable_http` (see aliases in **`env.sample`**). See **`env.sample`** for every variable.

- **Remote — `streamable_http`:** set **`MCP_STREAMABLE_HTTP_URL`**. Auth optional: **`MCP_BEARER_TOKEN`**, or OAuth trio **`MCP_CLIENT_ID`** + **`MCP_CLIENT_SECRET`** + **`MCP_TOKEN_URL`** (OAuth wins over bearer when all three are set). **`MCP_SKIP_TLS_VERIFY=true`** for dev TLS only.
- **Local — `stdio`:** set **`MCP_STDIO_COMMAND`** and optional **`MCP_STDIO_ARGS`** (JSON string array) and **`MCP_STDIO_ENV`** (JSON string→string object).

Shared optional knobs: **`MCP_SERVER_NAME`**, **`MCP_TIMEOUT_SECONDS`**, **`MCP_RETRY_ATTEMPTS`**, **`MCP_ALLOW_TOOLS`** / **`MCP_BLOCK_TOOLS`** (comma-separated; only one list type).

```bash
go run ./agent_with_mcp_config
go run ./agent_with_mcp_config "List tools you can call."

go run ./agent_with_mcp_client
go run ./agent_with_mcp_client "List tools you can call."
```

**Testing against real MCP servers:** This repo does **not** start an MCP server for you—you configure **`examples/.env`** so the examples connect to **your** server(s). Pick **stdio** (local subprocess) or **streamable_http** (remote URL), set **`MCP_TRANSPORT`** accordingly, then fill in **`env.sample`** under **MCP**.

**Worked example — TypeScript streamable HTTP (`mcp-streamable-http`):** This flow works with **`agent_with_mcp_*`** when **`examples/.env`** uses **`streamable_http`** and points at the server’s MCP endpoint (Temporal + LLM as usual):

```bash
git clone https://github.com/invariantlabs-ai/mcp-streamable-http
cd mcp-streamable-http/typescript-example/server
npm install && npm run build
node build/index.js
```

The upstream server listens on **port 8123** by default and serves MCP at **`/mcp`** (override with **`node build/index.js --port=XXXX`**). Set:

- **`MCP_TRANSPORT=streamable_http`**
- **`MCP_STREAMABLE_HTTP_URL=http://localhost:8123/mcp`**

Then run e.g. **`go run ./agent_with_mcp_config`** or **`go run ./agent_with_mcp_client`**.

Quick check (while the server is running):

```bash
curl -sS -o /dev/null -w "%{http_code}\n" "http://localhost:8123/mcp"
```

**Modes:**

| Mode | What you need |
|------|----------------|
| **`stdio`** | A runnable MCP server binary or script (often **Node**, **Python**, or **Go**) launched as a subprocess. Set **`MCP_STDIO_COMMAND`** and, if needed, **`MCP_STDIO_ARGS`** (JSON array) and **`MCP_STDIO_ENV`**. The SDK spawns the process and speaks MCP over stdin/stdout. |
| **`streamable_http`** | An MCP server already listening with the **streamable HTTP** transport. Set **`MCP_STREAMABLE_HTTP_URL`** to the MCP endpoint URL that server documents (often includes a path such as **`/mcp`**). Optional **`MCP_BEARER_TOKEN`** or OAuth (**`MCP_CLIENT_ID`** / **`MCP_CLIENT_SECRET`** / **`MCP_TOKEN_URL`**). Use **`MCP_SKIP_TLS_VERIFY=true`** only for local/dev HTTPS. |

Where to find servers and docs:

| Source | Notes |
|--------|--------|
| **[invariantlabs-ai/mcp-streamable-http](https://github.com/invariantlabs-ai/mcp-streamable-http)** | Reference **streamable HTTP** server (TypeScript example above). Default **8123**, path **`/mcp`**; set **`MCP_STREAMABLE_HTTP_URL`** to the full endpoint URL. |
| **[modelcontextprotocol/servers](https://github.com/modelcontextprotocol/servers)** | Reference implementations (filesystem, git, fetch, etc.). Often run with **`npx`** / **`uvx`** / **`docker`** per each server’s README; map that into **`MCP_STDIO_COMMAND`** + **`MCP_STDIO_ARGS`**. |
| **[Model Context Protocol](https://modelcontextprotocol.io)** | Protocol docs; third-party hosts and catalogs list streamable-HTTP endpoints you can point **`MCP_STREAMABLE_HTTP_URL`** at (verify TLS and auth with your provider). |
| **Your own MCP server** | Any compliant MCP implementation—the examples only need a working **`stdio`** or **`streamable_http`** transport as wired in **`env.sample`**. |

Quick checks before running **`agent_with_mcp_*`**:

- **`streamable_http`:** From the machine running the example, confirm the URL is reachable (TLS, VPN, firewall). Example: `curl -sS -o /dev/null -w "%{http_code}\n" "$MCP_STREAMABLE_HTTP_URL"` — expect a plausible HTTP response from **your** server (exact status depends on the implementation).
- **`stdio`:** Run the same command line you put in **`MCP_STDIO_COMMAND`** / **`MCP_STDIO_ARGS`** in a terminal once to ensure the binary starts and does not exit immediately.

You still need **Temporal** and **LLM** credentials in **`examples/.env`** like other examples.

### A2A (`WithA2AConfig` vs `WithA2AClients`)

Two examples use the **same env-driven settings** but register A2A tools differently:

- **`agent_with_a2a_config`** — `agent.WithA2AConfig(agent.A2AServers{<serverName>: cfg})`. The SDK constructs the default **[pkg/a2a/client](https://pkg.go.dev/github.com/agenticenv/agent-sdk-go/pkg/a2a/client)** client per server entry.
- **`agent_with_a2a_client`** — `a2aclient.NewClient(<serverName>, url, opts...)` then `agent.WithA2AClients(client)` for the same URL and options.

**Required:** **`A2A_URL`** — base URL of the remote A2A agent (well-known agent card + protocol endpoint per the card). See **`env.sample`** for optional **`A2A_SERVER_NAME`**, **`A2A_TOKEN`**, **`A2A_HEADERS`** (JSON object), **`A2A_TIMEOUT_SECONDS`**, **`A2A_SKIP_TLS_VERIFY`** (dev only), and **`A2A_ALLOW_SKILLS`** / **`A2A_BLOCK_SKILLS`** (comma-separated; mutually exclusive lists).

```bash
go run ./agent_with_a2a_config
go run ./agent_with_a2a_config "What tools do you have available?"

go run ./agent_with_a2a_client
go run ./agent_with_a2a_client "What tools do you have available?"
```

**Testing against a real A2A server:** There is no fixed “demo URL” shipped with this repo—you run an A2A-compatible HTTP server locally (or use your own deployment), point **`A2A_URL`** at its **base URL** (scheme + host + port, no path). The SDK resolves the agent card from the well-known path (same as [a2aproject/a2a-go](https://github.com/a2aproject/a2a-go) `a2asrv.WellKnownAgentCardPath`) and uses the JSON-RPC endpoint advertised on the card.

**Worked example — Python helloworld (`a2a-samples`):** This flow is known to work with **`agent_with_a2a_*`** when **`examples/.env`** sets **`A2A_URL=http://localhost:9999`** (and Temporal + LLM as usual):

```bash
git clone https://github.com/a2aproject/a2a-samples
cd a2a-samples/samples/python/agents/helloworld
uv run .   # starts the server on port 9999
```

In another terminal, confirm the agent card is served:

```bash
curl -sS "http://localhost:9999/.well-known/agent-card.json" | head
```

Then in **`examples/`**, ensure **`A2A_URL=http://localhost:9999`** (or **`http://127.0.0.1:9999`**) and run e.g. **`go run ./agent_with_a2a_config`** or **`go run ./agent_with_a2a_client`**.

Other ways to get a sample server:

| Source | Notes |
|--------|--------|
| **[a2aproject/a2a-samples](https://github.com/a2aproject/a2a-samples)** | Official protocol samples (several languages). The Python **`agents/helloworld`** sample above listens on **9999** by default; other samples may use different ports—set **`A2A_URL`** to match. |
| **[a2aproject/a2a-go](https://github.com/a2aproject/a2a-go)** | The same **`a2asrv`** stack used in **`pkg/a2a/client`** tests: static card handler + JSON-RPC handler. Use their README / examples to run an HTTP server; our examples treat **`A2A_URL`** like **`testA2AServer`** in **`client_test.go`** (base URL where both card and RPC are mounted). |
| **Your own agent** | Any deployment that exposes a valid agent card and the protocol endpoint the card references. |

Quick sanity check for any local server (replace the host/port if yours differs):

```bash
curl -sS "http://localhost:9999/.well-known/agent-card.json" | head
```

You still need **Temporal** and **LLM** credentials in **`examples/.env`** as for other examples.

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
