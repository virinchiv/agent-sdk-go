package temporal

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	sdkruntime "github.com/agenticenv/agent-sdk-go/internal/runtime"
	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
	"github.com/agenticenv/agent-sdk-go/pkg/observability"
	"go.temporal.io/sdk/client"
)

type TemporalConfig struct {
	Host      string
	Port      int
	Namespace string
	TaskQueue string
}

// TemporalRuntimeConfig holds connection settings plus the same [sdkruntime.AgentSpec] /
// [sdkruntime.AgentExecution] shape as [sdkruntime.ExecuteRequest], so workers and pkg/agent share one layout.
type TemporalRuntimeConfig struct {
	temporalConfig     *TemporalConfig
	temporalClient     client.Client
	taskQueue          string
	instanceId         string
	ownsTemporalClient bool
	// enableRemoteWorkers: start event worker + event workflow in Execute/ExecuteStream (client agent runtime).
	enableRemoteWorkers bool
	// remoteWorker: true for NewAgentWorker (polls activities); false for client Agent runtime.
	remoteWorker bool

	logger logger.Logger

	AgentSpec         sdkruntime.AgentSpec
	AgentExecution    sdkruntime.AgentExecution
	PolicyFingerprint string // from pkg/agent toolPolicyFingerprint; must match caller temporal.ComputeAgentFingerprint inputs
	MCPFingerprint    string // from pkg/agent mcpConfigFingerprint; must match caller temporal.ComputeAgentFingerprint inputs
	A2AFingerprint    string // from pkg/agent a2aConfigFingerprint; must match caller temporal.ComputeAgentFingerprint inputs
	// ObservabilityFingerprint is from pkg/agent observabilityConfigFingerprint; must match caller temporal.ComputeAgentFingerprint inputs.
	ObservabilityFingerprint string
	// AgentMode is the string form of [types.AgentMode] (e.g. "interactive", "autonomous"); must match pkg/agent WithAgentMode.
	AgentMode string
	// AgentToolExecutionMode is the [types.AgentToolExecutionMode] (e.g. "sequential", "parallel"); must match pkg/agent WithAgentToolExecutionMode.
	AgentToolExecutionMode types.AgentToolExecutionMode
	// DisableLocalWorker mirrors pkg/agent [DisableLocalWorker]: when false, the client embeds a worker
	// so Execute/ExecuteStream skip DescribeTaskQueue poller checks. ([NewAgentWorker] never calls those methods.)
	DisableLocalWorker bool
	// DisableFingerprintCheck disables caller-vs-worker agent fingerprint verification at activity entry.
	// Break-glass only: keep false in production for rollout/config safety.
	DisableFingerprintCheck bool

	// Tracer and Metrics are optional clients from pkg/agent (WithObservabilityConfig / WithTracer / WithMetrics).
	// They are stored for future instrumentation; nothing in the runtime uses them yet.
	Tracer  interfaces.Tracer
	Metrics interfaces.Metrics
}

// Option configures a TemporalRuntime.
type Option func(*TemporalRuntimeConfig)

// WithTemporalConfig sets the Temporal config.
func WithTemporalConfig(config *TemporalConfig) Option {
	return func(c *TemporalRuntimeConfig) {
		c.temporalConfig = config
		c.taskQueue = config.TaskQueue
		c.ownsTemporalClient = true
	}
}

// WithTemporalClient sets the Temporal client.
func WithTemporalClient(client client.Client, taskQueue string) Option {
	return func(c *TemporalRuntimeConfig) {
		c.temporalClient = client
		c.taskQueue = taskQueue
		c.ownsTemporalClient = false
	}
}

func WithInstanceId(instanceId string) Option {
	return func(c *TemporalRuntimeConfig) {
		c.instanceId = instanceId
	}
}

func WithEnableRemoteWorkers(enableRemoteWorkers bool) Option {
	return func(c *TemporalRuntimeConfig) {
		c.enableRemoteWorkers = enableRemoteWorkers
	}
}

func WithRemoteWorker(remoteWorker bool) Option {
	return func(c *TemporalRuntimeConfig) {
		c.remoteWorker = remoteWorker
	}
}

func WithLogger(logger logger.Logger) Option {
	return func(c *TemporalRuntimeConfig) {
		c.logger = logger
	}
}

// WithAgentSpec sets identity and response format (same as [sdkruntime.ExecuteRequest.AgentSpec]).
func WithAgentSpec(spec sdkruntime.AgentSpec) Option {
	return func(c *TemporalRuntimeConfig) {
		c.AgentSpec = spec
	}
}

// WithAgentExecution sets LLM, tools, session, and limits (same as [sdkruntime.ExecuteRequest.AgentExecution]).
func WithAgentExecution(exec sdkruntime.AgentExecution) Option {
	return func(c *TemporalRuntimeConfig) {
		c.AgentExecution = exec
	}
}

// WithPolicyFingerprint sets the opaque policy digest used with [ComputeAgentFingerprint].
// Must match pkg/agent's toolPolicyFingerprint for the same agent options.
func WithPolicyFingerprint(fp string) Option {
	return func(c *TemporalRuntimeConfig) {
		c.PolicyFingerprint = fp
	}
}

// WithMCPFingerprint sets the MCP wiring digest used with [ComputeAgentFingerprint].
// Must match pkg/agent's mcpConfigFingerprint for the same WithMCPConfig / WithMCPClients wiring.
func WithMCPFingerprint(fp string) Option {
	return func(c *TemporalRuntimeConfig) {
		c.MCPFingerprint = fp
	}
}

// WithA2AFingerprint sets the A2A wiring digest used with [ComputeAgentFingerprint].
// Must match pkg/agent's a2aConfigFingerprint for the same WithA2AConfig / WithA2AClients wiring.
func WithA2AFingerprint(fp string) Option {
	return func(c *TemporalRuntimeConfig) {
		c.A2AFingerprint = fp
	}
}

// WithObservabilityFingerprint sets the OTLP observability digest used with [ComputeAgentFingerprint].
// Must match pkg/agent observabilityConfigFingerprint for the same WithObservabilityConfig wiring.
func WithObservabilityFingerprint(fp string) Option {
	return func(c *TemporalRuntimeConfig) {
		c.ObservabilityFingerprint = fp
	}
}

// WithAgentMode sets the agent mode string used with [ComputeAgentFingerprint].
// Must match pkg/agent [WithAgentMode] for the same agent (caller process and worker process).
func WithAgentMode(mode string) Option {
	return func(c *TemporalRuntimeConfig) {
		c.AgentMode = mode
	}
}

// WithAgentToolExecutionMode sets the agent tool execution mode string used with [ComputeAgentFingerprint].
// Must match pkg/agent [WithAgentToolExecutionMode] for the same agent (caller process and worker process).
func WithAgentToolExecutionMode(mode types.AgentToolExecutionMode) Option {
	return func(c *TemporalRuntimeConfig) {
		c.AgentToolExecutionMode = mode
	}
}

// WithDisableLocalWorker mirrors pkg/agent [DisableLocalWorker]. When false, the client embeds a worker
// and the runtime skips DescribeTaskQueue poller checks before starting workflows.
func WithDisableLocalWorker(disable bool) Option {
	return func(c *TemporalRuntimeConfig) {
		c.DisableLocalWorker = disable
	}
}

// WithDisableFingerprintCheck disables activity-time caller-vs-worker fingerprint verification.
// Break-glass only: use temporarily during rollout incidents; default is strict verification.
func WithDisableFingerprintCheck(disable bool) Option {
	return func(c *TemporalRuntimeConfig) {
		c.DisableFingerprintCheck = disable
	}
}

// WithTracer sets the optional [interfaces.Tracer] for this runtime (from pkg/agent build).
// Reserved for future instrumentation; the runtime does not call it yet.
func WithTracer(t interfaces.Tracer) Option {
	return func(c *TemporalRuntimeConfig) {
		c.Tracer = t
	}
}

// WithMetrics sets the optional [interfaces.Metrics] for this runtime (from pkg/agent build).
// Reserved for future instrumentation; the runtime does not call it yet.
func WithMetrics(m interfaces.Metrics) Option {
	return func(c *TemporalRuntimeConfig) {
		c.Metrics = m
	}
}

func buildTemporalRuntimeConfig(opts ...Option) (*TemporalRuntimeConfig, error) {
	c := &TemporalRuntimeConfig{logger: logger.NoopLogger()}
	for _, opt := range opts {
		opt(c)
	}

	if c.temporalConfig == nil && c.temporalClient == nil {
		return nil, fmt.Errorf("temporal config or client is required")
	}

	if c.temporalConfig != nil {
		tc, err := newTemporalClient(c.temporalConfig, c.logger)
		if err != nil {
			return nil, err
		}
		c.temporalClient = tc
	}

	if c.instanceId != "" {
		c.taskQueue = c.taskQueue + "-" + c.instanceId
	}

	if c.AgentExecution.LLM.Client == nil {
		return nil, fmt.Errorf("llm client is required")
	}

	if c.Tracer == nil {
		c.Tracer = observability.DefaultNoopTracer
	}
	if c.Metrics == nil {
		c.Metrics = observability.DefaultNoopMetrics
	}

	c.logger.Debug(context.Background(), "runtime config resolved",
		slog.String("scope", "runtime"),
		slog.String("agentName", c.AgentSpec.Name),
		slog.String("taskQueue", c.taskQueue),
		slog.String("instanceId", c.instanceId),
		slog.Int("maxIterations", c.AgentExecution.Limits.MaxIterations),
		slog.Bool("remoteWorker", c.remoteWorker),
		slog.String("agentMode", c.AgentMode),
		slog.String("agentToolExecutionMode", string(c.AgentToolExecutionMode)),
		slog.Bool("enableRemoteWorkers", c.enableRemoteWorkers),
		slog.Bool("disableFingerprintCheck", c.DisableFingerprintCheck),
		slog.Duration("timeout", c.AgentExecution.Limits.Timeout),
		slog.Duration("approvalTimeout", c.AgentExecution.Limits.ApprovalTimeout),
		slog.Bool("hasConversation", c.AgentExecution.Session.Conversation != nil),
		slog.Bool("hasTracer", c.Tracer != nil),
		slog.Bool("hasMetrics", c.Metrics != nil))

	return c, nil
}

func newTemporalClient(config *TemporalConfig, sdkLog logger.Logger) (client.Client, error) {
	ctx := context.Background()
	sdkLog.Info(ctx, "runtime connecting to temporal server", slog.String("scope", "runtime"), slog.String("host", config.Host), slog.Int("port", config.Port))

	clientOptions := client.Options{
		HostPort:                config.Host + ":" + strconv.Itoa(config.Port),
		Namespace:               config.Namespace,
		Logger:                  NewLogAdapter(sdkLog),
		WorkerHeartbeatInterval: -1, // Disable; requires Temporal server 1.29.1+ with frontend.WorkerHeartbeatsEnabled=true
	}

	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	connectionTimeout := 10 * time.Second

	timeoutExceeded := time.After(connectionTimeout)

	var c client.Client
	var err error
	clientReady := false

	ctx, cancelFunc := context.WithCancel(context.Background())
	defer cancelFunc()

	for {
		select {
		case <-timeoutExceeded:
			if !clientReady {
				return nil, fmt.Errorf("%w: could not reach Temporal at %s (namespace %q) within %v",
					types.ErrTemporalDialTimeout, clientOptions.HostPort, config.Namespace, connectionTimeout)
			}
			c.Close()
			return nil, fmt.Errorf("%w: namespace %q at %s could not be verified within %v",
				types.ErrTemporalNamespaceCheckTimeout, config.Namespace, clientOptions.HostPort, connectionTimeout)
		case <-ticker.C:
			if !clientReady {
				c, err = client.Dial(clientOptions)
				if err == nil {
					sdkLog.Debug(ctx, "runtime temporal client dialed, verifying namespace", slog.String("scope", "runtime"))
					clientReady = true
				} else {
					sdkLog.Debug(ctx, "runtime temporal dial retry", slog.String("scope", "runtime"), slog.Any("error", err))
				}
			} else {
				nsClient, err := client.NewNamespaceClient(clientOptions)
				if err == nil {
					_, err = nsClient.Describe(ctx, config.Namespace)
					nsClient.Close()
					if err == nil {
						sdkLog.Info(ctx, "runtime ready (temporal connected)", slog.String("scope", "runtime"), slog.String("namespace", config.Namespace), slog.String("host", config.Host))
						return c, nil
					}
				}
				sdkLog.Debug(ctx, "runtime namespace check retry", slog.String("scope", "runtime"), slog.String("namespace", config.Namespace), slog.Any("error", err))
			}
		}
	}
}
