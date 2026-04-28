package temporal

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/agenticenv/agent-sdk-go/internal/events"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/workflow"
)

var (
	agentEventChannelPrefix = "agent_event_"

	agentEventActivityTaskTimeout time.Duration = 2 * time.Minute
	// Single attempt: dead inmem / subscriber does not recover with retries; avoids long backoff when the agent process is gone.
	agentEventActivityMaxAttempts int32 = 1

	agentEventName              = "agent-event"
	eventWorkflowCompleteSignal = "complete" // received when agent Close is called

	// maxEventsPerWorkflow: continue-as-new threshold.
	// overflowBuffer: extra events we accept while processing, to avoid losing events during continue-as-new.
	maxEventsPerWorkflow int = 100
	eventOverflowBuffer  int = 50 // accept up to 150 events; 101–150 can arrive while processing 1–100
)

// eventChannelName returns the pub/sub channel name for agent events. runID is the run workflow ID.
func eventChannelName(runID string) string {
	return agentEventChannelPrefix + runID
}

// AgentEventUpdate is the payload for agent-event updates when using one event workflow per agent.
// AgentName is the name of the agent that emitted the event (main agent or a sub-agent).
// LocalChannelName is the in-process pub/sub channel name (agent_event_<main-workflow-id>)
// all nodes in the delegation tree publish to.
type AgentEventUpdate struct {
	AgentName        string          `json:"agent_name"`
	LocalChannelName string          `json:"local_channel_name,omitempty"`
	EventJSON        json.RawMessage `json:"event_json"`
}

// AgentEventWorkflow is one per agent. Receives events and approval requests via workflow updates.
// Each update includes runID so events are published to per-run channels (agent_event_{runID}, approval_{runID}).
// Completes only when it receives the "complete" signal (on agent Close).
func (rt *TemporalRuntime) AgentEventWorkflow(ctx workflow.Context) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("workflow: event pipeline started", "scope", "workflow")

	var noOfEvents, processedCount int
	var options workflow.UpdateHandlerOptions
	options.Validator = func(ctx workflow.Context, upd *AgentEventUpdate) error {
		if noOfEvents >= maxEventsPerWorkflow+eventOverflowBuffer {
			return fmt.Errorf("max events per workflow reached (%d), continue as new", maxEventsPerWorkflow+eventOverflowBuffer)
		}
		return nil
	}

	eventCh := workflow.NewChannel(ctx)

	// Handler only hands off to eventCh; it does not wait for EventPublishActivity. Client WaitForStage (Accepted vs Completed)
	// applies when this handler returns, not when inmem publish succeeds (that runs in the goroutine below).
	err := workflow.SetUpdateHandlerWithOptions(ctx, agentEventName, func(ctx workflow.Context, upd *AgentEventUpdate) error {
		noOfEvents++
		eventType, err := events.EventTypeFromJSON(upd.EventJSON)
		if err != nil {
			return fmt.Errorf("failed to get event type from JSON: %w", err)
		}
		logger.Debug("workflow: event update received", "scope", "workflow", "agent", upd.AgentName, "eventType", eventType)
		eventCh.Send(ctx, upd)
		return nil
	}, options)
	if err != nil {
		return fmt.Errorf("failed setting update handler for events: %w", err)
	}

	actCtx := workflow.WithActivityOptions(ctx, workflow.ActivityOptions{
		StartToCloseTimeout: agentEventActivityTaskTimeout,
		RetryPolicy:         retryPolicy(agentEventActivityMaxAttempts),
	})

	workflow.Go(ctx, func(ctx workflow.Context) {
		for {
			var upd *AgentEventUpdate
			eventCh.Receive(ctx, &upd)
			if upd == nil {
				return
			}
			if err := workflow.ExecuteActivity(actCtx, rt.EventPublishActivity, upd.LocalChannelName, upd.EventJSON).Get(ctx, nil); err != nil {
				eventType, _ := events.EventTypeFromJSON(upd.EventJSON)
				logger.Warn("workflow: event publish activity failed", "scope", "workflow", "error", err, "eventType", eventType, "agent", upd.AgentName)
			}
			processedCount++
		}
	})

	// Listen for "complete" signal (run done or agent Close) to exit gracefully
	var completeReceived bool
	completeCh := workflow.GetSignalChannel(ctx, eventWorkflowCompleteSignal)
	workflow.Go(ctx, func(ctx workflow.Context) {
		var v struct{}
		completeCh.Receive(ctx, &v)
		completeReceived = true
	})

	logger.Debug("workflow: awaiting events or shutdown signal", "scope", "workflow")

	err = workflow.Await(ctx, func() bool {
		if completeReceived {
			return true
		}
		return noOfEvents >= maxEventsPerWorkflow &&
			processedCount == noOfEvents &&
			workflow.AllHandlersFinished(ctx)
	})
	if err != nil {
		return err
	}

	if completeReceived {
		logger.Debug("workflow: event pipeline shutdown signal received", "scope", "workflow")
		return nil
	}
	logger.Debug("workflow: event pipeline continue-as-new", "scope", "workflow")
	return workflow.NewContinueAsNewError(ctx, rt.AgentEventWorkflow)
}

// EventPublishActivity publishes an event to the given channel (agent_event_<main-workflow-id>).
func (rt *TemporalRuntime) EventPublishActivity(ctx context.Context, channel string, eventJSON json.RawMessage) error {
	if len(eventJSON) == 0 {
		return fmt.Errorf("agent event payload is empty")
	}
	logger := activity.GetLogger(ctx)
	evType, _ := events.EventTypeFromJSON([]byte(eventJSON))
	logger.Debug("activity: publish event", "scope", "activity", "channel", channel, "eventType", evType)
	if err := rt.eventbus.Publish(ctx, channel, []byte(eventJSON)); err != nil {
		logger.Error("activity: publish event failed", "scope", "activity", "channel", channel, "error", err)
		return fmt.Errorf("failed to publish agent event: %w", err)
	}
	return nil
}

// subscribeToAgentEvents returns a channel that receives AgentEvent from the given event channel.
func (rt *TemporalRuntime) subscribeToAgentEvents(ctx context.Context, channel string) (<-chan events.AgentEvent, func() error, error) {
	rt.logger.Debug(ctx, "runtime subscribing to event channel", slog.String("scope", "runtime"), slog.String("channel", channel))
	ch, closeFn, err := rt.eventbus.Subscribe(ctx, channel)
	if err != nil {
		rt.logger.Error(ctx, "runtime event channel subscribe failed", slog.String("scope", "runtime"), slog.String("channel", channel), slog.Any("error", err))
		return nil, nil, err
	}

	// Buffered so Decode→Forward cannot deadlock when [outCh] applies backpressure (slow consumer).
	eventCh := make(chan events.AgentEvent, 64)
	go func() {
		defer close(eventCh)
		for data := range ch {
			ev, err := events.EventFromJSON(data)
			if err != nil {
				rt.logger.Warn(ctx, "runtime event decode skipped", slog.String("scope", "runtime"), slog.Any("error", err))
				continue
			}
			eventCh <- ev
		}
	}()

	rt.logger.Debug(ctx, "runtime event channel subscribed", slog.String("scope", "runtime"), slog.String("channel", channel))
	return eventCh, closeFn, nil
}
