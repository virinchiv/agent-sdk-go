---
layout: default
---

Build **durable, production-grade AI agents in Go** — tools, MCP, human approvals, and sub-agent delegation — on **[Temporal](https://temporal.io)** workflows end to end.

[![CI](https://github.com/agenticenv/agent-sdk-go/actions/workflows/ci.yml/badge.svg?branch=main)](https://github.com/agenticenv/agent-sdk-go/actions)
[![Release](https://img.shields.io/github/v/release/agenticenv/agent-sdk-go?label=Release)](https://github.com/agenticenv/agent-sdk-go/releases)
[![Go Reference](https://pkg.go.dev/badge/github.com/agenticenv/agent-sdk-go.svg)](https://pkg.go.dev/github.com/agenticenv/agent-sdk-go)
[![License](https://img.shields.io/github/license/agenticenv/agent-sdk-go?label=License)](https://github.com/agenticenv/agent-sdk-go/blob/main/LICENSE)

Independent community library — **not** affiliated with Temporal Technologies.

## Why this SDK

Most agent frameworks lose the run when the process restarts. Here, **every agent run is a Temporal workflow**: survives crashes and deploys, respects timeouts and retries, and is observable like any other service operation.

## Documentation

| Resource | Description |
| -------- | ----------- |
| **[README](https://github.com/agenticenv/agent-sdk-go#readme)** | Full guide: getting started, Temporal runtime, streaming, tools, MCP, approvals, sub-agents |
| **[pkg.go.dev](https://pkg.go.dev/github.com/agenticenv/agent-sdk-go)** | Generated API reference |
| **[Releases](https://github.com/agenticenv/agent-sdk-go/releases)** | Versioned tags and changelog |
| **[Examples](https://github.com/agenticenv/agent-sdk-go/tree/main/examples)** | Runnable samples (`simple_agent`, streaming, MCP, CopilotKit, durable agent, …) |

## Install

**Go 1.24+** and a running Temporal server. Module path:

```bash
go get github.com/agenticenv/agent-sdk-go@latest
```

## Quick start

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
    agent.WithTemporalConfig(&agent.TemporalConfig{
        Host: "localhost", Port: 7233,
        Namespace: "default", TaskQueue: "my-app",
    }),
    agent.WithSystemPrompt("You are a helpful assistant."),
    agent.WithLLMClient(llmClient),
)
defer a.Close()

result, err := a.Run(ctx, "Hello", "")
// result.Content, result.AgentName, result.Model
```

See the [Getting Started](https://github.com/agenticenv/agent-sdk-go#getting-started) section in the README for Temporal connection options, streaming, tools, and more.

## Community

- [Contributing](https://github.com/agenticenv/agent-sdk-go/blob/main/CONTRIBUTING.md)
- [Security](https://github.com/agenticenv/agent-sdk-go/blob/main/SECURITY.md)
- [Code of Conduct](https://github.com/agenticenv/agent-sdk-go/blob/main/CODE_OF_CONDUCT.md)

---

*This site is published from the repository `docs/` folder via [GitHub Pages](https://docs.github.com/pages).*
