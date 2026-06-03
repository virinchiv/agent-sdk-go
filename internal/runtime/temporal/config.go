package temporal

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/contrib/opentelemetry"
	"go.temporal.io/sdk/interceptor"
)

// TemporalConfig holds the Temporal server connection parameters.
// Pass it to [WithTemporalConfig] when the runtime should dial its own client.
type TemporalConfig struct {
	Host      string
	Port      int
	Namespace string
	TaskQueue string
}

func newTemporalClient(config *TemporalConfig, sdkLog logger.Logger, tracer interfaces.Tracer) (client.Client, error) {
	ctx := context.Background()
	sdkLog.Info(ctx, "runtime connecting to temporal server", slog.String("scope", "runtime"), slog.String("host", config.Host), slog.Int("port", config.Port))

	clientOptions := client.Options{
		HostPort:                config.Host + ":" + strconv.Itoa(config.Port),
		Namespace:               config.Namespace,
		Logger:                  NewLogAdapter(sdkLog),
		WorkerHeartbeatInterval: -1, // Disable; requires Temporal server 1.29.1+ with frontend.WorkerHeartbeatsEnabled=true
	}

	tracingInterceptor, traceErr := newTemporalTracingInterceptor(tracer)
	if traceErr != nil {
		return nil, fmt.Errorf("failed to create tracing interceptor: %w", traceErr)
	}
	if tracingInterceptor != nil {
		clientOptions.Interceptors = []interceptor.ClientInterceptor{tracingInterceptor}
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

// newTemporalTracingInterceptor returns the Temporal SDK OpenTelemetry tracing [interceptor.Interceptor]
// when tracer implements [interfaces.OTelTracer]. Returns (nil, nil) when tracing should be skipped
// (including nil tracer or tracers that do not expose an OpenTelemetry [trace.Tracer]).
func newTemporalTracingInterceptor(tracer interfaces.Tracer) (interceptor.Interceptor, error) {
	otelTracer, ok := tracer.(interfaces.OTelTracer)
	if !ok {
		return nil, nil
	}
	return opentelemetry.NewTracingInterceptor(opentelemetry.TracerOptions{
		Tracer: otelTracer.OTelTracer(),
	})
}
