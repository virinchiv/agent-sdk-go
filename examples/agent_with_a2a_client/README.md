# `agent_with_a2a_client`

Same **`A2A_*`** settings as **`agent_with_a2a_config`**, but registers the client with **`a2aclient.NewClient`** + **`WithA2AClients`**.

**How to run a remote A2A server for testing, env vars, and curl checks:** see **[../agent_with_a2a_config/README.md](../agent_with_a2a_config/README.md)**.

From **`examples/`**:

```bash
go run ./agent_with_a2a_client
go run ./agent_with_a2a_client "What tools do you have available?"
```
