package temporal

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/agenticenv/agent-sdk-go/internal/eventbus"
	"github.com/agenticenv/agent-sdk-go/internal/events"
	"github.com/agenticenv/agent-sdk-go/internal/runtime"
	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/google/uuid"
	enumspb "go.temporal.io/api/enums/v1"
	"go.temporal.io/sdk/activity"
	"go.temporal.io/sdk/client"
	"go.temporal.io/sdk/interceptor"
	"go.temporal.io/sdk/worker"
	"go.temporal.io/sdk/workflow"
)

var (
	_ runtime.WorkerRuntime   = (*TemporalRuntime)(nil)
	_ runtime.EventBusRuntime = (*TemporalRuntime)(nil) // embeds [runtime.Runtime]
)

const (
	// workersCheckTimeout is how long hasWorkers polls for pollers before giving up.
	workersCheckTimeout = 15 * time.Second

	// maxAgentNameWorkflowSegmentBytes caps the sanitized agent-name segment embedded in Temporal workflow IDs.
	// Shorter than typical server limits; truncation uses truncateUTF8String to avoid splitting UTF-8 code points.
	maxAgentNameWorkflowSegmentBytes = 128
)

// ErrAgentAlreadyRunning is returned when Execute, ExecuteStream, or RunAsync is called while a run is already in progress.
var ErrAgentAlreadyRunning = errors.New("agent already has an active run")

// ErrAgentFingerprintMismatch is returned when workflow input fingerprint does not match the worker.
var ErrAgentFingerprintMismatch = errors.New("temporal: agent fingerprint mismatch (caller vs worker); redeploy worker or align agent config")

type TemporalRuntime struct {
	TemporalRuntimeConfig

	// agentFingerprint is ComputeAgentFingerprint(BuildAgentFingerprintPayload(...)) at NewTemporalRuntime; immutable for this runtime.
	agentFingerprint string

	eventbus              eventbus.EventBus
	runMu                 sync.Mutex
	activeRunWorkflowID   string
	activeEventWorkflowID string
	// eventWorkflowIDOnce + suffix make agent-event-<name>-<uuid> unique per TemporalRuntime so
	// multiple agents in one process do not collide; lazy start still uses the same ID for that runtime.
	eventWorkflowIDOnce   sync.Once
	eventWorkflowIDSuffix string

	eventWorker   worker.Worker
	eventWorkerMu sync.Mutex

	agentWorker   worker.Worker
	agentWorkerMu sync.Mutex
}

func NewTemporalRuntime(opts ...Option) (*TemporalRuntime, error) {
	cfg, err := buildTemporalRuntimeConfig(opts...)
	if err != nil {
		return nil, err
	}
	cfg.logger.Info(context.Background(), "runtime created", slog.String("scope", "runtime"), slog.String("name", cfg.AgentSpec.Name), slog.String("taskQueue", cfg.taskQueue))
	if cfg.DisableFingerprintCheck {
		cfg.logger.Warn(context.Background(),
			"fingerprint verification is disabled (break-glass mode)",
			slog.String("scope", "runtime"),
			slog.String("name", cfg.AgentSpec.Name),
			slog.String("taskQueue", cfg.taskQueue))
	}
	fp := computeAgentFingerprintFromRuntimeConfig(cfg)
	return &TemporalRuntime{
		TemporalRuntimeConfig: *cfg,
		agentFingerprint:      fp,
		eventbus:              eventbus.NewInmem(cfg.logger),
	}, nil
}

// verifyAgentFingerprint returns an error when want does not equal the runtime's agent fingerprint
// (computed at [NewTemporalRuntime]).
func (rt *TemporalRuntime) verifyAgentFingerprint(want string) error {
	if rt.DisableFingerprintCheck {
		return nil
	}
	if rt.agentFingerprint != want {
		return fmt.Errorf("%w: worker=%q caller=%q", ErrAgentFingerprintMismatch, rt.agentFingerprint, want)
	}
	return nil
}

// SetEventBus replaces the in-process event bus. Sub-agent runtimes are wired to the parent
// agent's bus so delegation and approval events fan in correctly.
func (rt *TemporalRuntime) SetEventBus(eventbus eventbus.EventBus) {
	rt.eventbus = eventbus
}

// GetEventBus returns the event bus for the runtime.
func (rt *TemporalRuntime) GetEventBus() eventbus.EventBus {
	return rt.eventbus
}

// Start starts the worker (blocks until Stop is called).
func (rt *TemporalRuntime) Start(ctx context.Context) error {
	rt.logger.Info(ctx, "runtime worker starting", slog.String("scope", "runtime"), slog.String("taskQueue", rt.taskQueue))
	// createAgentWorker creates and registers a Temporal worker for the agent's run workflow and activities.

	rt.agentWorkerMu.Lock()
	defer rt.agentWorkerMu.Unlock()
	if rt.agentWorker != nil {
		rt.logger.Debug(ctx, "runtime worker already running", slog.String("scope", "runtime"), slog.String("taskQueue", rt.taskQueue))
		return nil
	}
	rt.logger.Debug(ctx, "runtime worker registering workflows and activities", slog.String("scope", "runtime"), slog.String("taskQueue", rt.taskQueue))

	workerOptions := worker.Options{}
	tracingInterceptor, err := newTemporalTracingInterceptor(rt.Tracer)
	if err != nil {
		rt.logger.Error(ctx, "failed to create tracing interceptor", slog.String("scope", "runtime"), slog.String("taskQueue", rt.taskQueue), slog.Any("error", err))
		return err
	}
	if tracingInterceptor != nil {
		workerOptions.Interceptors = []interceptor.WorkerInterceptor{tracingInterceptor}
	}

	w := worker.New(rt.temporalClient, rt.taskQueue, workerOptions)
	w.RegisterWorkflowWithOptions(rt.AgentWorkflow, workflow.RegisterOptions{Name: "AgentWorkflow"})
	w.RegisterActivityWithOptions(rt.AgentLLMActivity, activity.RegisterOptions{Name: "AgentLLMActivity"})
	w.RegisterActivityWithOptions(rt.AgentLLMStreamActivity, activity.RegisterOptions{Name: "AgentLLMStreamActivity"})
	w.RegisterActivityWithOptions(rt.AgentRetrieverActivity, activity.RegisterOptions{Name: "AgentRetrieverActivity"})
	w.RegisterActivityWithOptions(rt.AgentToolAuthorizeActivity, activity.RegisterOptions{Name: "AgentToolAuthorizeActivity"})
	w.RegisterActivityWithOptions(rt.AgentToolApprovalActivity, activity.RegisterOptions{Name: "AgentToolApprovalActivity"})
	w.RegisterActivityWithOptions(rt.AgentToolExecuteActivity, activity.RegisterOptions{Name: "AgentToolExecuteActivity"})
	w.RegisterActivityWithOptions(rt.SendAgentEventUpdateActivity, activity.RegisterOptions{Name: "SendAgentEventUpdateActivity"})
	w.RegisterActivityWithOptions(rt.AddConversationMessagesActivity, activity.RegisterOptions{Name: "AddConversationMessagesActivity"})
	rt.agentWorker = w
	if startErr := rt.agentWorker.Start(); startErr != nil {
		rt.logger.Error(ctx, "failed to start runtime worker", slog.String("scope", "runtime"), slog.String("taskQueue", rt.taskQueue), slog.Any("error", startErr))
		return startErr
	}
	rt.logger.Debug(ctx, "runtime worker started", slog.String("scope", "runtime"), slog.String("taskQueue", rt.taskQueue))
	return nil
}

// Stop stops the Temporal worker(s). Called when the agent package stops an embedded worker or closes the runtime.
func (rt *TemporalRuntime) Stop() {
	ctx := context.Background()
	if rt.remoteWorker {
		rt.logger.Debug(ctx, "runtime stopping remote worker path", slog.String("scope", "runtime"), slog.String("taskQueue", rt.taskQueue))
		if rt.agentWorker != nil {
			rt.logger.Debug(ctx, "runtime stopping remote worker", slog.String("scope", "runtime"), slog.String("taskQueue", rt.taskQueue))
			rt.agentWorker.Stop()
		}
		if rt.temporalClient != nil && rt.ownsTemporalClient {
			rt.logger.Debug(ctx, "runtime closing owned client (remote worker)", slog.String("scope", "runtime"), slog.String("taskQueue", rt.taskQueue))
			rt.temporalClient.Close()
		}
		rt.logger.Debug(ctx, "runtime remote worker stopped", slog.String("scope", "runtime"), slog.String("taskQueue", rt.taskQueue))
	} else {
		rt.logger.Debug(ctx, "runtime stop skipped (local worker embed)", slog.String("scope", "runtime"), slog.String("taskQueue", rt.taskQueue))
	}
}

func (rt *TemporalRuntime) Close() {
	rt.logger.Info(context.Background(), "runtime closing", slog.String("scope", "runtime"), slog.String("name", rt.AgentSpec.Name))

	rt.runMu.Lock()
	workflowID := rt.activeRunWorkflowID
	eventWorkflowID := rt.activeEventWorkflowID
	rt.runMu.Unlock()

	ctx := context.Background()

	if rt.temporalClient != nil {
		ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
		defer cancel()
		if workflowID != "" {
			rt.logger.Debug(ctx, "runtime terminating active run", slog.String("scope", "runtime"), slog.String("workflowID", workflowID))
			_ = rt.temporalClient.TerminateWorkflow(ctx, workflowID, "", "agent closed")
		}
		if eventWorkflowID != "" {
			rt.logger.Debug(ctx, "runtime signaling event pipeline to complete", slog.String("scope", "runtime"), slog.String("eventWorkflowID", eventWorkflowID))
			_ = rt.temporalClient.SignalWorkflow(ctx, eventWorkflowID, "", eventWorkflowCompleteSignal, nil)
			// Wait for event workflow to complete gracefully (worker must stay running to process the signal)
			run := rt.temporalClient.GetWorkflow(ctx, eventWorkflowID, "")
			_ = run.Get(ctx, nil)
		}
	}

	if rt.eventWorker != nil {
		rt.logger.Debug(ctx, "runtime stopping event worker", slog.String("scope", "runtime"))
		rt.eventWorker.Stop()
	}

	if rt.agentWorker != nil {
		rt.logger.Debug(ctx, "runtime stopping task worker", slog.String("scope", "runtime"), slog.String("taskQueue", rt.taskQueue))
		rt.agentWorker.Stop()
	}

	if rt.temporalClient != nil && rt.ownsTemporalClient {
		rt.logger.Debug(ctx, "runtime closing owned temporal client", slog.String("scope", "runtime"))
		rt.temporalClient.Close()
	}
	rt.logger.Info(ctx, "runtime closed", slog.String("scope", "runtime"), slog.String("name", rt.AgentSpec.Name))
}

func (rt *TemporalRuntime) Approve(ctx context.Context, approvalToken string, status types.ApprovalStatus) error {
	if status != types.ApprovalStatusApproved && status != types.ApprovalStatusRejected {
		return fmt.Errorf("invalid approval status: %s", status)
	}
	taskToken, err := base64.StdEncoding.DecodeString(approvalToken)
	if err != nil {
		return fmt.Errorf("invalid approval token: %w", err)
	}
	return rt.temporalClient.CompleteActivity(ctx, taskToken, status, nil)
}

func agentNameFromExecuteRequest(req *runtime.ExecuteRequest) string {
	if req == nil || req.AgentSpec == nil {
		return ""
	}
	return req.AgentSpec.Name
}

func (rt *TemporalRuntime) Execute(ctx context.Context, req *runtime.ExecuteRequest) (*types.AgentRunResult, error) {
	rt.logger.Debug(ctx, "runtime run dispatch", slog.String("scope", "runtime"), slog.String("agent", agentNameFromExecuteRequest(req)), slog.Int("inputLen", len(req.UserPrompt)))

	runCtx := ctx
	d := rt.AgentExecution.Limits.Timeout
	if _, ok := ctx.Deadline(); !ok && d > 0 {
		var cancel context.CancelFunc
		runCtx, cancel = context.WithTimeout(ctx, d)
		defer cancel()
	}

	runID := uuid.New().String()
	threadID := req.ConversationID
	if threadID != "" {
		threadID = rt.instanceId
		if threadID == "" {
			threadID = runID
		}
	}
	workflowID := rt.getWorkflowID(runID, agentNameFromExecuteRequest(req), false)

	rt.logger.Debug(runCtx, "runtime identifiers", slog.String("scope", "runtime"), slog.String("runID", runID), slog.String("threadID", threadID), slog.String("workflowID", workflowID))

	cleanup, err := rt.beginRun(workflowID)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	wfInput := AgentWorkflowInput{
		UserPrompt:       req.UserPrompt,
		StreamingEnabled: false,
		EventWorkflowID:  "",
		LocalChannelName: eventChannelName(workflowID),
		ConversationID:   req.ConversationID,
		AgentFingerprint: rt.agentFingerprint,
		EventTypes:       []events.AgentEventType{},
		SubAgentDepth:    0,
		SubAgentRoutes:   req.SubAgentRoutes,
		MaxSubAgentDepth: req.MaxSubAgentDepth,
	}

	if rt.enableRemoteWorkers {
		if err := rt.createEventWorker(); err != nil {
			rt.logger.Error(runCtx, "runtime event worker creation failed", slog.String("scope", "runtime"), slog.String("taskQueue", rt.taskQueue), slog.Any("error", err))
			return nil, err
		}
		wfInput.EventWorkflowID, wfInput.EventTaskQueue, err = rt.resolveEventPipeline(runCtx, agentNameFromExecuteRequest(req))
		if err != nil {
			rt.logger.Error(runCtx, "runtime event pipeline resolution failed", slog.String("scope", "runtime"), slog.String("agent", agentNameFromExecuteRequest(req)), slog.Any("error", err))
			return nil, err
		}
	}

	var eventCh <-chan events.AgentEvent
	var closeEvent func() error
	if req.ApprovalHandler != nil {
		wfInput.EventTypes = []events.AgentEventType{events.AgentEventTypeCustom}
		eventCh, closeEvent, err = rt.subscribeToAgentEvents(runCtx, wfInput.LocalChannelName)
		if err != nil {
			rt.logger.Error(runCtx, "runtime event subscribe failed", slog.String("scope", "runtime"), slog.String("workflowID", workflowID), slog.Any("error", err))
			return nil, err
		}
		defer func() { _ = closeEvent() }()
	}

	if !rt.skipHasWorkersPrecheck() {
		hasWorkers := rt.hasWorkers(runCtx, rt.taskQueue)
		if !hasWorkers {
			rt.logger.Warn(runCtx, "no workers on task queue", slog.String("scope", "runtime"), slog.String("taskQueue", rt.taskQueue))
			return nil, fmt.Errorf("no workers available on task queue %s", rt.taskQueue)
		}
		rt.logger.Debug(runCtx, "task queue has workers", slog.String("scope", "runtime"), slog.String("taskQueue", rt.taskQueue))
	} else {
		rt.logger.Debug(runCtx, "skipping task queue poller check", slog.String("scope", "runtime"), slog.String("taskQueue", rt.taskQueue), slog.String("reason", rt.hasWorkersPrecheckSkipReason()))
	}

	rt.logger.Debug(runCtx, "runtime workflow execute",
		slog.String("scope", "runtime"),
		slog.String("workflowID", workflowID),
		slog.Bool("streamingEnabled", wfInput.StreamingEnabled),
		slog.Bool("hasEventPipeline", wfInput.EventWorkflowID != ""))

	workfowRun, err := rt.temporalClient.ExecuteWorkflow(runCtx, client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: rt.taskQueue,
	}, rt.AgentWorkflow, wfInput)
	if err != nil {
		rt.logger.Error(runCtx, "runtime workflow start failed", slog.String("scope", "runtime"), slog.String("workflowID", workflowID), slog.Any("error", err))
		return nil, err
	}

	resultCh := make(chan *types.AgentRunResult, 1)
	wfErrCh := make(chan error, 1)
	go func() {
		var result *types.AgentRunResult
		err = workfowRun.Get(runCtx, &result)
		if err != nil {
			wfErrCh <- err
			return
		}
		resultCh <- result
	}()

	type approvalResponse struct {
		approvalToken string
		status        types.ApprovalStatus
	}
	approvalResponseCh := make(chan approvalResponse, 16)

	rt.logger.Debug(runCtx, "runtime waiting for run result", slog.String("scope", "runtime"), slog.String("workflowID", workflowID))

	for {
		select {
		case r := <-resultCh:
			rt.logger.Debug(runCtx, "runtime run completed",
				slog.String("scope", "runtime"),
				slog.String("agentName", r.AgentName),
				slog.String("model", r.Model),
				slog.Int("contentLen", len(r.Content)))
			return r, nil
		case err := <-wfErrCh:
			rt.logger.Error(runCtx, "runtime run failed", slog.String("scope", "runtime"), slog.String("workflowID", workflowID), slog.Any("error", err))
			return nil, err
		case <-runCtx.Done():
			rt.logger.Debug(runCtx, "runtime run cancelled", slog.String("scope", "runtime"), slog.String("workflowID", workflowID), slog.Any("error", runCtx.Err()))
			termCtx, termCancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer termCancel()
			if rt.temporalClient != nil {
				_ = rt.temporalClient.TerminateWorkflow(termCtx, workflowID, "", "run timeout")
			}
			return nil, runCtx.Err()
		case ev := <-eventCh:
			if ev == nil || ev.Type() != events.AgentEventTypeCustom {
				continue
			}
			approvalEv, ok := ev.(*events.AgentCustomEvent)
			if !ok {
				continue
			}
			apprReq, token, err := types.PrepareApprovalFromCustomEvent(approvalEv)
			if err != nil {
				if errors.Is(err, types.ErrNotApprovalCustomEvent) {
					continue
				}
				rt.logger.Error(runCtx, "runtime approval custom event decode failed", slog.String("scope", "runtime"), slog.Any("error", err))
				continue
			}
			apprReq.Respond = func(status types.ApprovalStatus) error {
				if status != types.ApprovalStatusRejected && status != types.ApprovalStatusApproved {
					return errors.New("invalid approval status")
				}
				approvalResponseCh <- approvalResponse{approvalToken: token, status: status}
				return nil
			}
			approvalCtx, cancel := context.WithTimeout(runCtx, rt.AgentExecution.Limits.ApprovalTimeout)
			req.ApprovalHandler(approvalCtx, apprReq)
			cancel()
		case resp := <-approvalResponseCh:
			if err := rt.OnApproval(runCtx, resp.approvalToken, resp.status); err != nil {
				rt.logger.Error(runCtx, "runtime approval completion failed", slog.String("scope", "runtime"), slog.Any("error", err))
				return nil, err
			}
		}
	}
}

func (rt *TemporalRuntime) ExecuteStream(ctx context.Context, req *runtime.ExecuteRequest) (<-chan events.AgentEvent, error) {
	rt.logger.Debug(ctx, "runtime stream run dispatch", slog.String("scope", "runtime"), slog.String("agent", agentNameFromExecuteRequest(req)), slog.Int("inputLen", len(req.UserPrompt)))

	runID := uuid.New().String()
	threadID := req.ConversationID
	if threadID == "" {
		threadID = rt.instanceId
		if threadID == "" {
			threadID = runID
		}
	}
	workflowID := rt.getWorkflowID(runID, agentNameFromExecuteRequest(req), true)

	rt.logger.Debug(ctx, "runtime identifiers", slog.String("scope", "runtime"), slog.String("runID", runID), slog.String("threadID", threadID), slog.String("workflowID", workflowID))

	cleanup, err := rt.beginRun(workflowID)
	if err != nil {
		return nil, err
	}
	streamStarted := false
	defer func() {
		if !streamStarted {
			cleanup()
		}
	}()

	var eventWorkflowID, eventTaskQueue string
	if rt.enableRemoteWorkers {
		if err := rt.createEventWorker(); err != nil {
			rt.logger.Error(ctx, "runtime event worker creation failed", slog.String("scope", "runtime"), slog.String("taskQueue", rt.taskQueue), slog.Any("error", err))
			return nil, err
		}
		eventWorkflowID, eventTaskQueue, err = rt.resolveEventPipeline(ctx, agentNameFromExecuteRequest(req))
		if err != nil {
			rt.logger.Error(ctx, "runtime event pipeline resolution failed", slog.String("scope", "runtime"), slog.String("agent", agentNameFromExecuteRequest(req)), slog.Any("error", err))
			return nil, err
		}
	}

	streamEventTypes := []events.AgentEventType{events.AgentEventAll}
	if len(req.EventTypes) > 0 {
		streamEventTypes = req.EventTypes
	}
	wfInput := AgentWorkflowInput{
		UserPrompt:       req.UserPrompt,
		EventWorkflowID:  eventWorkflowID,
		EventTaskQueue:   eventTaskQueue,
		LocalChannelName: eventChannelName(workflowID),
		StreamingEnabled: req.StreamingEnabled,
		ConversationID:   req.ConversationID,
		AgentFingerprint: rt.agentFingerprint,
		EventTypes:       streamEventTypes,
		SubAgentDepth:    0,
		SubAgentRoutes:   req.SubAgentRoutes,
		MaxSubAgentDepth: req.MaxSubAgentDepth,
	}

	runCtx := ctx
	var runCancel context.CancelFunc
	d := rt.AgentExecution.Limits.Timeout
	if _, ok := ctx.Deadline(); !ok && d > 0 {
		runCtx, runCancel = context.WithTimeout(ctx, d)
	}
	defer func() {
		if !streamStarted && runCancel != nil {
			runCancel()
		}
	}()

	eventCh, closeEvent, err := rt.subscribeToAgentEvents(runCtx, wfInput.LocalChannelName)
	if err != nil {
		rt.logger.Error(runCtx, "runtime event subscribe failed", slog.String("scope", "runtime"), slog.String("channel", wfInput.LocalChannelName), slog.Any("error", err))
		return nil, err
	}
	rt.logger.Debug(runCtx, "runtime subscribed to event stream", slog.String("scope", "runtime"), slog.String("channel", wfInput.LocalChannelName))
	defer func() {
		if !streamStarted && closeEvent != nil {
			_ = closeEvent()
		}
	}()

	if !rt.skipHasWorkersPrecheck() {
		hasWorkers := rt.hasWorkers(ctx, rt.taskQueue)
		if !hasWorkers {
			rt.logger.Warn(runCtx, "no workers on task queue", slog.String("scope", "runtime"), slog.String("taskQueue", rt.taskQueue))
			return nil, fmt.Errorf("no workers available on task queue %s", rt.taskQueue)
		}
		rt.logger.Debug(runCtx, "task queue has workers (stream)", slog.String("scope", "runtime"), slog.String("taskQueue", rt.taskQueue))
	} else {
		rt.logger.Debug(runCtx, "skipping task queue poller check", slog.String("scope", "runtime"), slog.String("taskQueue", rt.taskQueue), slog.String("reason", rt.hasWorkersPrecheckSkipReason()))
	}

	rt.logger.Debug(runCtx, "runtime workflow execute (stream)", slog.String("scope", "runtime"), slog.String("workflowID", workflowID))

	workflowRun, err := rt.temporalClient.ExecuteWorkflow(ctx, client.StartWorkflowOptions{
		ID:        workflowID,
		TaskQueue: rt.taskQueue,
	}, rt.AgentWorkflow, wfInput)
	if err != nil {
		rt.logger.Error(runCtx, "runtime workflow start failed", slog.String("scope", "runtime"), slog.String("workflowID", workflowID), slog.Any("error", err))
		return nil, err
	}

	rt.logger.Debug(runCtx, "runtime workflow started (stream)", slog.String("scope", "runtime"), slog.String("workflowID", workflowID))

	streamStarted = true
	outCh := make(chan events.AgentEvent, 64)
	wfErrCh := make(chan error, 1)
	workflowResultCh := make(chan *types.AgentRunResult, 1)
	localChannel := wfInput.LocalChannelName
	rootName := agentNameFromExecuteRequest(req)

	// eventCh → outCh only: all RUN_* and workflow events pass through the local bus (publish then forward).
	go func() {
		defer close(outCh)
		for ev := range eventCh {
			if ev == nil {
				continue
			}
			outCh <- ev
		}
	}()

	rt.publishRunEvent(localChannel, events.NewAgentRunStartedEvent(threadID, runID))

	go func() {
		// cleanup/endRun only after Get returns. runCtx must stay valid until then so the workflow
		// can finish (after root complete, work remains: e.g. SendAgentEventUpdateActivity—longer
		// when a sub-agent child workflow just ran).
		defer cleanup()
		var result *types.AgentRunResult
		if err := workflowRun.Get(runCtx, &result); err != nil {
			// Cancel the run timeout context on failure so the control goroutine and subscriber unwind.
			if runCancel != nil {
				runCancel()
			}
			wfErrCh <- err
			return
		}
		// On success, do not cancel runCtx here. Get can return before in-flight streaming events are
		// published (async pipeline: UpdateWorkflow → event workflow → publish activity → inmem bus).
		// Cancelling runCtx immediately races the eventCh→outCh forwarder and can surface a spurious
		// RUN_ERROR. The control goroutine calls runCancel only after publishing root RUN_FINISHED.
		// Non-blocking send: if workflowResultCh (buffer 1) is full, the control path has not read yet—drop.
		select {
		case workflowResultCh <- result:
		default:
		}
	}()

	go func() {
		select {
		case <-runCtx.Done():
			termCtx, termCancel := context.WithTimeout(context.Background(), 15*time.Second)
			if rt.temporalClient != nil {
				_ = rt.temporalClient.TerminateWorkflow(termCtx, workflowID, "", "run timeout")
			}
			termCancel()
			rt.publishRunEvent(localChannel, events.NewAgentRunErrorEvent("request timed out (approval expired or deadline exceeded)"))
			_ = closeEvent()
		case wfErr := <-wfErrCh:
			rt.logger.Error(runCtx, "runtime stream run failed", slog.String("scope", "runtime"), slog.String("workflowID", workflowID), slog.Any("error", wfErr))
			if errors.Is(wfErr, context.DeadlineExceeded) || errors.Is(wfErr, context.Canceled) {
				termCtx, termCancel := context.WithTimeout(context.Background(), 15*time.Second)
				if rt.temporalClient != nil {
					_ = rt.temporalClient.TerminateWorkflow(termCtx, workflowID, "", "run timeout")
				}
				termCancel()
			}
			rt.publishRunEvent(localChannel, events.NewAgentRunErrorEvent(wfErr.Error()))
			_ = closeEvent()
		case result := <-workflowResultCh:
			finEv := syntheticStreamCompleteEvent(result, threadID, runID, rootName)
			rt.publishRunEvent(localChannel, finEv)
			if runCancel != nil {
				runCancel()
			}
			_ = closeEvent()
		}
	}()

	return outCh, nil
}

// OnApproval completes a tool approval when using ExecuteStream. Pass the string from ev.Approval
// (see the streaming examples) along with the chosen status.
func (rt *TemporalRuntime) OnApproval(ctx context.Context, approvalToken string, status types.ApprovalStatus) error {
	if status != types.ApprovalStatusApproved && status != types.ApprovalStatusRejected {
		return fmt.Errorf("invalid approval status: %s", status)
	}
	taskToken, err := base64.StdEncoding.DecodeString(approvalToken)
	if err != nil {
		return fmt.Errorf("invalid approval token: %w", err)
	}
	return rt.temporalClient.CompleteActivity(ctx, taskToken, status, nil)
}

// resolveEventPipeline returns the deterministic event workflow ID and event task queue when remote
// workers are enabled. The AgentEventWorkflow is started lazily on the first UpdateWithStart from an activity.
func (rt *TemporalRuntime) resolveEventPipeline(ctx context.Context, agentName string) (eventWorkflowID string, eventTaskQueue string, err error) {
	eventWorkflowID = rt.getEventWorkflowID(agentName)
	eventTaskQueue = getEventTaskQueue(rt.taskQueue)
	rt.runMu.Lock()
	if rt.activeEventWorkflowID == "" {
		rt.activeEventWorkflowID = eventWorkflowID
		rt.logger.Info(ctx, "runtime event pipeline (lazy start on first update)",
			slog.String("scope", "runtime"),
			slog.String("eventWorkflowID", eventWorkflowID),
			slog.String("eventTaskQueue", eventTaskQueue))
	}
	rt.runMu.Unlock()
	return eventWorkflowID, eventTaskQueue, nil
}

// skipHasWorkersPrecheck is true when Execute/ExecuteStream should not poll DescribeTaskQueue for pollers
// before starting the workflow. Only these paths call Execute/ExecuteStream (client [Agent]; remoteWorker is always false).
// Skip when mode is autonomous, or when an embedded worker polls in-process ([DisableLocalWorker] false).
func (rt *TemporalRuntime) skipHasWorkersPrecheck() bool {
	if rt.AgentMode == string(types.AgentModeAutonomous) {
		return true
	}
	if !rt.DisableLocalWorker {
		return true
	}
	return false
}

func (rt *TemporalRuntime) hasWorkersPrecheckSkipReason() string {
	if rt.AgentMode == string(types.AgentModeAutonomous) {
		return "autonomous_mode"
	}
	if !rt.DisableLocalWorker {
		return "embedded_local_worker"
	}
	return ""
}

// hasWorkers returns true if there are pollers on the given task queue.
// If taskQueue is empty, uses a.taskQueue.
// Polls DescribeTaskQueue for up to workersCheckTimeout (default 15s) before returning false.
func (rt *TemporalRuntime) hasWorkers(ctx context.Context, taskQueue string) bool {
	q := taskQueue
	if q == "" {
		q = rt.taskQueue
	}
	timeout := workersCheckTimeout
	deadline, ok := ctx.Deadline()
	if ok && time.Until(deadline) < timeout {
		timeout = time.Until(deadline)
	}
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	deadlineTime := time.Now().Add(timeout)
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	for {
		res, err := rt.temporalClient.DescribeTaskQueue(ctx, q, enumspb.TASK_QUEUE_TYPE_WORKFLOW)
		if err == nil && len(res.GetPollers()) != 0 {
			rt.logger.Debug(ctx, "task queue pollers seen", slog.String("scope", "runtime"), slog.String("taskQueue", q), slog.Int("pollers", len(res.GetPollers())))
			return true
		}
		if time.Now().After(deadlineTime) {
			rt.logger.Debug(ctx, "task queue worker wait timed out", slog.String("scope", "runtime"), slog.String("taskQueue", q))
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-ticker.C:
			// retry
		}
	}
}

// createEventWorker starts the event worker if not already running. Called when ExecuteStream or
// approval handling is needed. Per-agent mutex allows parallel creation across different agents
// while preventing double-creation when Execute and ExecuteStream are invoked concurrently on the same agent.
func (rt *TemporalRuntime) createEventWorker() error {
	rt.eventWorkerMu.Lock()
	defer rt.eventWorkerMu.Unlock()
	if rt.eventWorker != nil {
		rt.logger.Debug(context.Background(), "runtime event worker already running", slog.String("scope", "runtime"), slog.String("taskQueue", rt.taskQueue))
		return nil
	}
	eventQueue := getEventTaskQueue(rt.taskQueue)
	rt.logger.Info(context.Background(), "runtime event worker starting", slog.String("scope", "runtime"), slog.String("taskQueue", eventQueue))

	workerOptions := worker.Options{}
	tracingInterceptor, err := newTemporalTracingInterceptor(rt.Tracer)
	if err != nil {
		rt.logger.Error(context.Background(), "failed to create tracing interceptor", slog.String("scope", "runtime"), slog.String("taskQueue", rt.taskQueue), slog.Any("error", err))
		return err
	}
	if tracingInterceptor != nil {
		workerOptions.Interceptors = []interceptor.WorkerInterceptor{tracingInterceptor}
	}

	w := worker.New(rt.temporalClient, eventQueue, workerOptions)
	w.RegisterWorkflowWithOptions(rt.AgentEventWorkflow, workflow.RegisterOptions{Name: "AgentEventWorkflow"})
	w.RegisterActivityWithOptions(rt.EventPublishActivity, activity.RegisterOptions{Name: "EventPublishActivity"})
	rt.eventWorker = w
	go func() { _ = rt.eventWorker.Start() }()
	return nil
}

// publishRunEvent puts a run lifecycle or stream event on the local agent_event_* bus; the
// stream reader forwards from [eventCh] to [outCh]. Publish uses [context.Background]: the run
// [runCtx] may already be cancelled and [eventbus.Inmem.Publish] aborts when ctx is done.
func (rt *TemporalRuntime) publishRunEvent(channel string, ev events.AgentEvent) {
	if rt.eventbus == nil || channel == "" || ev == nil {
		return
	}
	data, err := ev.ToJSON()
	if err != nil {
		return
	}
	pubCtx := context.Background()
	if err := rt.eventbus.Publish(pubCtx, channel, data); err != nil {
		rt.logger.Warn(pubCtx, "runtime run event publish failed", slog.String("scope", "runtime"), slog.String("channel", channel), slog.String("type", string(ev.Type())), slog.Any("error", err))
	}
}

// syntheticStreamCompleteEvent builds a root [RUN_FINISHED] ([*events.AgentRunFinishedEvent]) from
// workflow.Get. ExecuteStream publishes it after Get returns so the client gets a terminal result without
// waiting only on the async event pipeline.
func syntheticStreamCompleteEvent(result *types.AgentRunResult, threadID, runID, rootName string) events.AgentEvent {
	if result != nil {
		if strings.TrimSpace(result.AgentName) != "" {
			result.AgentName = strings.TrimSpace(result.AgentName)
		} else if strings.TrimSpace(rootName) != "" {
			result.AgentName = strings.TrimSpace(rootName)
		}
	} else if strings.TrimSpace(rootName) != "" {
		result = &types.AgentRunResult{
			AgentName: strings.TrimSpace(rootName),
		}
	}
	return events.NewAgentRunFinishedEvent(threadID, runID, result)
}

func getEventTaskQueue(taskQueue string) string {
	return taskQueue + "-events"
}

// beginRun marks the start of a run. Returns (cleanup, nil) on success; call cleanup (or defer) when the run ends.
// Returns (nil, ErrAgentAlreadyRunning) if a run is already active.
func (rt *TemporalRuntime) beginRun(workflowID string) (func(), error) {
	rt.runMu.Lock()
	defer rt.runMu.Unlock()
	if rt.activeRunWorkflowID != "" {
		rt.logger.Debug(context.Background(), "runtime run rejected: already active", slog.String("scope", "runtime"), slog.String("active", rt.activeRunWorkflowID), slog.String("requested", workflowID))
		return nil, ErrAgentAlreadyRunning
	}
	rt.activeRunWorkflowID = workflowID
	rt.logger.Debug(context.Background(), "runtime run started", slog.String("scope", "runtime"), slog.String("workflowID", workflowID))
	return func() { rt.endRun() }, nil
}

// endRun clears the active run state when a run completes. Does not clear activeEventWorkflowID (per-agent).
func (rt *TemporalRuntime) endRun() {
	rt.runMu.Lock()
	defer rt.runMu.Unlock()
	if rt.activeRunWorkflowID != "" {
		rt.logger.Debug(context.Background(), "runtime run finished", slog.String("scope", "runtime"), slog.String("workflowID", rt.activeRunWorkflowID))
	}
	rt.activeRunWorkflowID = ""
}

func (rt *TemporalRuntime) getWorkflowID(runID, agentName string, isStream bool) string {
	name := sanitizeTemporalWorkflowIDSegment(agentName)
	if isStream {
		return fmt.Sprintf("agent-stream-%s-%s", name, runID)
	}
	return fmt.Sprintf("agent-run-%s-%s", name, runID)
}

func (rt *TemporalRuntime) getEventWorkflowID(agentName string) string {
	rt.eventWorkflowIDOnce.Do(func() {
		rt.eventWorkflowIDSuffix = uuid.New().String()
	})
	return fmt.Sprintf("agent-event-%s-%s", sanitizeTemporalWorkflowIDSegment(agentName), rt.eventWorkflowIDSuffix)
}

// sanitizeTemporalWorkflowIDSegment maps a human-readable label (e.g. [runtime.AgentSpec] Name) to a safe
// workflow ID segment: alphanumeric, hyphen, underscore, dot; spaces and other runes become hyphens.
// The result is capped at [maxAgentNameWorkflowSegmentBytes] using UTF-8-safe truncation.
func sanitizeTemporalWorkflowIDSegment(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "agent"
	}
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == ' ' || r == '\t':
			b.WriteByte('-')
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	out := strings.Trim(b.String(), "-")
	if out == "" {
		return "agent"
	}
	return truncateUTF8String(out, maxAgentNameWorkflowSegmentBytes)
}

// truncateUTF8String returns s if len(s) <= maxBytes; otherwise returns a prefix of at most maxBytes bytes
// that is valid UTF-8 (does not split a multibyte code point).
func truncateUTF8String(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	s = s[:maxBytes]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}
