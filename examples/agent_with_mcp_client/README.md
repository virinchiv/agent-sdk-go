# `agent_with_mcp_client`

Same MCP transports and **`examples/.env`** variables as **`agent_with_mcp_config`**, but wires **`mcpclient.NewClient`** + **`WithMCPClients`** instead of **`WithMCPConfig`**.

**Testing steps, worked examples, and troubleshooting:** see **[../agent_with_mcp_config/README.md](../agent_with_mcp_config/README.md)**.

From **`examples/`**:

```bash
go run ./agent_with_mcp_client
go run ./agent_with_mcp_client "List tools you can call."
```
