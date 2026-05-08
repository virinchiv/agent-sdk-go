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

	// maxEventsPerWorkflow limits how many valid agent-event updates are accepted per workflow run
	// (validator only). It is burst/backpressure: not used to decide ContinueAsNew. Callers that hit
	// the limit get a synchronous validation error and should retry with backoff.
	maxEventsPerWorkflow int = 500

	// eventWorkflowHistory* are Temporal execution history limits (GetInfo). When either bound is
	// reached, the workflow sets continueAsNewPending, drains pending publishes, then returns
	// NewContinueAsNewError (same workflow ID, new run) so history stays bounded.
	eventWorkflowHistoryLength    = 10_000
	eventWorkflowHistorySizeBytes = 40_000_000
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
// It completes with nil when it receives the "complete" signal (e.g. agent Close). It may
// ContinueAsNew when workflow history (length or size) exceeds the configured caps, after draining
// in-flight EventPublishActivity work; the burst validator (maxEventsPerWorkflow) is independent of that decision.
func (rt *TemporalRuntime) AgentEventWorkflow(ctx workflow.Context) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("workflow: event pipeline started", "scope", "workflow")

	var noOfEvents, pendingCount int

	// continueAsNewPending: history budget exceeded; drain pendingCount then ContinueAsNew.
	// Shared with the update validator so new updates are rejected until the new run starts.
	var continueAsNewPending bool

	var options workflow.UpdateHandlerOptions
	options.Validator = func(ctx workflow.Context, upd *AgentEventUpdate) error {
		// ContinueAsNew already decided (history budget hit) — drain in progress.
		// Client should retry on the new workflow run.
		if continueAsNewPending {
			return fmt.Errorf("workflow continue-as-new pending, rejecting new events")
		}
		// Burst protection (counts accepted valid updates only). Independent of ContinueAsNew (history-driven).
		if noOfEvents >= maxEventsPerWorkflow {
			return fmt.Errorf("event burst limit reached (%d), retry shortly", maxEventsPerWorkflow)
		}
		return nil
	}

	eventCh := workflow.NewChannel(ctx)
	doneCh := workflow.NewChannel(ctx)

	// Handler only hands off to eventCh; it does not wait for EventPublishActivity. Client WaitForStage (Accepted vs Completed)
	// applies when this handler returns, not when inmem publish succeeds (that runs in the goroutine below).
	err := workflow.SetUpdateHandlerWithOptions(ctx, agentEventName, func(ctx workflow.Context, upd *AgentEventUpdate) error {
		eventType, err := events.EventTypeFromJSON(upd.EventJSON)
		if err != nil {
			return fmt.Errorf("failed to get event type from JSON: %w", err)
		}
		noOfEvents++
		pendingCount++
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
			var done bool

			sel := workflow.NewSelector(ctx)
			sel.AddReceive(eventCh, func(c workflow.ReceiveChannel, _ bool) {
				c.Receive(ctx, &upd)
			})
			sel.AddReceive(doneCh, func(c workflow.ReceiveChannel, _ bool) {
				done = true
			})
			sel.Select(ctx)

			if done {
				return
			}

			if err := workflow.ExecuteActivity(actCtx, rt.EventPublishActivity, upd.LocalChannelName, upd.EventJSON).Get(ctx, nil); err != nil {
				eventType, _ := events.EventTypeFromJSON(upd.EventJSON)
				logger.Warn("workflow: event publish activity failed", "scope", "workflow", "error", err, "eventType", eventType, "agent", upd.AgentName)
			}
			pendingCount--
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

		// History threshold crossed (length OR size): flag once, log once; validator rejects further updates while draining.
		if !continueAsNewPending {
			info := workflow.GetInfo(ctx)
			if info.GetCurrentHistoryLength() >= eventWorkflowHistoryLength || info.GetCurrentHistorySize() >= eventWorkflowHistorySizeBytes {
				continueAsNewPending = true
				logger.Info("workflow: history budget exceeded, draining before continue-as-new", "scope", "workflow",
					"historyLength", info.GetCurrentHistoryLength(),
					"historyLengthLimit", eventWorkflowHistoryLength,
					"historySizeBytes", info.GetCurrentHistorySize(),
					"historySizeBytesLimit", eventWorkflowHistorySizeBytes,
				)
			}
		}

		// Wait until all in-flight events are drained and no handlers are running.
		if pendingCount > 0 || !workflow.AllHandlersFinished(ctx) {
			return false
		}

		return continueAsNewPending
	})
	if err != nil {
		return err
	}

	// Stop the consumer goroutine cleanly before exiting.
	doneCh.Send(ctx, struct{}{})

	if completeReceived {
		logger.Debug("workflow: event pipeline shutdown signal received", "scope", "workflow")
		return nil
	}
	if pendingCount != 0 {
		logger.Warn("workflow: event pipeline continue-as-new with non-zero pendingCount (logic bug)", "scope", "workflow", "pendingCount", pendingCount)
	}
	info := workflow.GetInfo(ctx)
	logger.Debug("workflow: event pipeline continue-as-new", "scope", "workflow",
		"noOfEvents", noOfEvents,
		"historyLength", info.GetCurrentHistoryLength(),
		"historySizeBytes", info.GetCurrentHistorySize(),
	)
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
