// Package setup shares OTLP env parsing and base agent options for the config/ and objects/ examples.
package setup

import (
	"log"
	"os"
	"strings"

	excfg "github.com/agenticenv/agent-sdk-go/examples"
	"github.com/agenticenv/agent-sdk-go/pkg/agent"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/observability"
	"github.com/agenticenv/agent-sdk-go/pkg/tools"
	"github.com/agenticenv/agent-sdk-go/pkg/tools/calculator"
)

// OTLP holds values from OTEL_EXPORTER_OTLP_ENDPOINT, OTLP_PROTOCOL, OTLP_INSECURE.
type OTLP struct {
	Endpoint string
	// AgentProto is used with [agent.ObservabilityConfig].
	AgentProto agent.OTLPProtocol
	// ObsProto is used with [observability.Option] when building tracer, metrics, and logs manually.
	ObsProto observability.Protocol
	Insecure bool
}

// MustParseOTLP reads OTLP-related environment variables and exits on missing endpoint or bad protocol.
func MustParseOTLP() OTLP {
	endpoint := strings.TrimSpace(os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"))
	if endpoint == "" {
		log.Fatal("set OTEL_EXPORTER_OTLP_ENDPOINT (e.g. localhost:4317 for gRPC OTLP)")
	}

	var o OTLP
	o.Endpoint = endpoint
	switch strings.ToLower(strings.TrimSpace(os.Getenv("OTLP_PROTOCOL"))) {
	case "", "grpc":
		o.AgentProto = agent.OTLPProtocolGRPC
		o.ObsProto = observability.ProtocolGRPC
	case "http":
		o.AgentProto = agent.OTLPProtocolHTTP
		o.ObsProto = observability.ProtocolHTTP
	default:
		log.Fatal("OTLP_PROTOCOL must be grpc or http")
	}
	o.Insecure = strings.EqualFold(os.Getenv("OTLP_INSECURE"), "true")
	return o
}

// BaseAgentOptions returns shared [agent.Option]s for both examples (identity, Temporal, LLM, logger).
func BaseAgentOptions(cfg *excfg.Config, llm interfaces.LLMClient) []agent.Option {
	reg := tools.NewRegistry()
	reg.Register(calculator.New())

	return []agent.Option{
		agent.WithName("observability-example-agent"),
		agent.WithDescription("Agent demonstrating OTLP wiring (see examples/agent_with_observability)."),
		agent.WithSystemPrompt("You are a concise assistant."),
		agent.WithTemporalConfig(&agent.TemporalConfig{
			Host:      cfg.Host,
			Port:      cfg.Port,
			Namespace: cfg.Namespace,
			TaskQueue: cfg.TaskQueue,
		}),
		agent.WithLLMClient(llm),
		agent.WithToolRegistry(reg),
		agent.WithToolApprovalPolicy(agent.AutoToolApprovalPolicy()),
		agent.WithLogLevel(cfg.LogLevel),
		//agent.WithLogger(excfg.NewLoggerFromLogConfig(cfg)),
	}
}

// UserPrompt returns command-line text after the program name, or a default line if empty.
func UserPrompt() string {
	p := strings.Join(os.Args[1:], " ")
	if strings.TrimSpace(p) == "" {
		return "Say hello in one short sentence."
	}
	return p
}
