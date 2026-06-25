package base

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/agenticenv/agent-sdk-go/internal/hooks"
	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/logger"
	"github.com/agenticenv/agent-sdk-go/pkg/memory"
)

const memoryExtractSystemPrompt = "Extract durable long-term memories from the conversation. " +
	"Return only facts, preferences, decisions, or instructions worth recalling in future runs. " +
	"Skip greetings, transient context, and tool noise. Return an empty memories array when nothing should persist."

var errMemoryExtractUnavailable = errors.New("memory extract unavailable: StoreMode always requires custom Extract or LLM client")

// StoreMemoryRecords persists records through kind policy, dedup, TTL, and the memory backend.
func (rt *Runtime) StoreMemoryRecords(ctx context.Context, input StoreMemoryRecordsInput) error {
	if !rt.MemoryConfigured() {
		return nil
	}

	log := input.Logger
	ctx, batchSp := rt.Tracer.StartSpan(ctx, "memory.store.batch",
		interfaces.Attribute{Key: "record.count", Value: len(input.Records)},
	)
	defer batchSp.End()

	for _, rec := range input.Records {
		if err := rt.storeRecord(ctx, log, input, rec); err != nil {
			batchSp.RecordError(err)
			return err
		}
	}
	return nil
}

func (rt *Runtime) storeRecord(ctx context.Context, log logger.Logger, input StoreMemoryRecordsInput, rec interfaces.MemoryRecord) error {
	cfg := rt.AgentConfig.Memory.Config
	scope := input.Scope

	storeCall := hooks.MemoryStoreCall{Scope: scope, Record: rec}
	var err error
	storeCall, err = rt.runBeforeMemoryStoreHooks(ctx, input, storeCall)
	if err != nil {
		return err
	}

	text := strings.TrimSpace(storeCall.Record.Text)
	if text == "" {
		return nil
	}

	ctx, sp := rt.Tracer.StartSpan(ctx, "memory.store")
	defer sp.End()

	kind, err := cfg.Store.ResolveKind(storeCall.Record.Kind)
	if err != nil {
		sp.RecordError(err)
		rt.Metrics.IncrementCounter(ctx, types.MetricMemoryStoreFailed)
		log.Error(ctx, "runtime: memory store kind rejected", slog.String("scope", "runtime"), slog.Any("error", err))
		return fmt.Errorf("memory store: %w", err)
	}

	kindAttr := interfaces.Attribute{Key: types.MetricAttrMemoryKind, Value: string(kind)}
	sp.SetAttribute(string(types.MetricAttrMemoryKind), string(kind))
	log.Debug(ctx, "runtime: memory store started", slog.String("scope", "runtime"), slog.String("kind", string(kind)))

	rt.Metrics.IncrementCounter(ctx, types.MetricMemoryStoreStarted, kindAttr)
	start := time.Now()

	var storeOpts []interfaces.StoreMemoryOption
	var dedupAction string
	if hookID := strings.TrimSpace(storeCall.ID); hookID != "" {
		storeOpts = []interfaces.StoreMemoryOption{interfaces.WithMemoryID(hookID)}
		dedupAction = "upsert"
	} else {
		storeOpts, dedupAction, err = rt.dedupStoreOptions(ctx, scope, text)
		if err != nil {
			latency := float64(time.Since(start).Milliseconds())
			sp.RecordError(err)
			sp.SetAttribute("latency_ms", latency)
			rt.Metrics.IncrementCounter(ctx, types.MetricMemoryStoreFailed, kindAttr)
			rt.Metrics.RecordHistogram(ctx, types.MetricMemoryStoreLatencyMs, latency, kindAttr)
			log.Error(ctx, "runtime: memory dedup lookup failed", slog.String("scope", "runtime"), slog.Any("error", err))
			return fmt.Errorf("memory store: dedup: %w", err)
		}
	}

	dedupAttr := interfaces.Attribute{Key: types.MetricAttrMemoryDedup, Value: dedupAction}
	sp.SetAttribute(string(types.MetricAttrMemoryDedup), dedupAction)

	now := time.Now().UTC()
	record := interfaces.MemoryRecord{
		Text:      text,
		Kind:      kind,
		Metadata:  storeCall.Record.Metadata,
		ExpiresAt: cfg.ExpiresAtForKind(kind, now),
	}

	storedID, err := cfg.Memory.Store(ctx, scope, record, storeOpts...)
	if err != nil {
		latency := float64(time.Since(start).Milliseconds())
		sp.RecordError(err)
		sp.SetAttribute("latency_ms", latency)
		rt.Metrics.IncrementCounter(ctx, types.MetricMemoryStoreFailed, kindAttr)
		rt.Metrics.RecordHistogram(ctx, types.MetricMemoryStoreLatencyMs, latency, kindAttr)
		log.Error(ctx, "runtime: memory store failed", slog.String("scope", "runtime"), slog.Any("error", err))
		return fmt.Errorf("memory store: %w", err)
	}

	if err := rt.runAfterMemoryStoreHooks(ctx, input, hooks.MemoryStoreCall{
		Scope:  scope,
		Record: record,
		ID:     storedID,
	}); err != nil {
		latency := float64(time.Since(start).Milliseconds())
		sp.RecordError(err)
		sp.SetAttribute("latency_ms", latency)
		rt.Metrics.IncrementCounter(ctx, types.MetricMemoryStoreFailed, kindAttr)
		rt.Metrics.RecordHistogram(ctx, types.MetricMemoryStoreLatencyMs, latency, kindAttr)
		return err
	}

	latency := float64(time.Since(start).Milliseconds())
	sp.SetAttribute("latency_ms", latency)
	sp.SetAttribute("dedup.upsert", dedupAction == "upsert")
	sp.SetAttribute("memory.id", storedID)
	rt.Metrics.IncrementCounter(ctx, types.MetricMemoryStoreCompleted, kindAttr, dedupAttr)
	rt.Metrics.RecordHistogram(ctx, types.MetricMemoryStoreLatencyMs, latency, kindAttr)
	log.Debug(ctx, "runtime: memory store completed",
		slog.String("scope", "runtime"),
		slog.String("dedup", dedupAction),
		slog.String("id", storedID))
	return nil
}

func (rt *Runtime) dedupStoreOptions(ctx context.Context, scope interfaces.MemoryScope, text string) ([]interfaces.StoreMemoryOption, string, error) {
	cfg := rt.AgentConfig.Memory.Config
	minScore := cfg.Store.DedupMinScore
	if minScore <= 0 {
		return nil, "append", nil
	}

	rt.Metrics.IncrementCounter(ctx, types.MetricMemoryDedupStarted)
	dedupStart := time.Now()

	ctx, sp := rt.Tracer.StartSpan(ctx, "memory.dedup",
		interfaces.Attribute{Key: "min_score", Value: minScore},
	)
	defer sp.End()

	matches, err := cfg.Memory.Load(ctx, scope, text,
		interfaces.WithLoadLimit(1),
		interfaces.WithMinScore(minScore),
	)
	latency := float64(time.Since(dedupStart).Milliseconds())
	sp.SetAttribute("latency_ms", latency)

	if err != nil {
		sp.RecordError(err)
		rt.Metrics.IncrementCounter(ctx, types.MetricMemoryDedupFailed)
		rt.Metrics.RecordHistogram(ctx, types.MetricMemoryDedupLatencyMs, latency)
		return nil, "", err
	}

	rt.Metrics.IncrementCounter(ctx, types.MetricMemoryDedupCompleted)
	rt.Metrics.RecordHistogram(ctx, types.MetricMemoryDedupLatencyMs, latency)

	if len(matches) == 0 {
		sp.SetAttribute("matched", false)
		return nil, "append", nil
	}

	sp.SetAttribute("matched", true)
	sp.SetAttribute("match.id", matches[0].ID)
	return []interfaces.StoreMemoryOption{interfaces.WithMemoryID(matches[0].ID)}, "upsert", nil
}

func parseSaveMemoryToolArgs(args map[string]any) (interfaces.MemoryRecord, error) {
	rawText, ok := args[types.MemoryToolParamText].(string)
	if !ok {
		return interfaces.MemoryRecord{}, fmt.Errorf("save_memory: %q parameter required", types.MemoryToolParamText)
	}
	text := strings.TrimSpace(rawText)
	if text == "" {
		return interfaces.MemoryRecord{}, fmt.Errorf("save_memory: %q must be non-empty", types.MemoryToolParamText)
	}
	record := interfaces.MemoryRecord{
		Text: text,
		Metadata: map[string]string{
			"source": types.SaveMemoryToolName,
		},
	}
	if rawKind, ok := args[types.MemoryToolParamKind].(string); ok {
		record.Kind = interfaces.MemoryKind(strings.TrimSpace(rawKind))
	}
	return record, nil
}

func (rt *Runtime) extractMemoryRecords(
	ctx context.Context,
	log logger.Logger,
	messages []interfaces.Message,
	extract memory.ExtractFunc,
) ([]interfaces.MemoryRecord, error) {
	rt.Metrics.IncrementCounter(ctx, types.MetricMemoryExtractStarted)
	start := time.Now()

	ctx, sp := rt.Tracer.StartSpan(ctx, "memory.extract",
		interfaces.Attribute{Key: "message.count", Value: len(messages)},
	)
	defer sp.End()

	log.Debug(ctx, "runtime: memory extract started", slog.String("scope", "runtime"))

	records, err := extract(ctx, messages)
	latency := float64(time.Since(start).Milliseconds())
	sp.SetAttribute("latency_ms", latency)

	if err != nil {
		sp.RecordError(err)
		rt.Metrics.IncrementCounter(ctx, types.MetricMemoryExtractFailed)
		rt.Metrics.RecordHistogram(ctx, types.MetricMemoryExtractLatencyMs, latency)
		log.Error(ctx, "runtime: memory extract failed", slog.String("scope", "runtime"), slog.Any("error", err))
		return nil, fmt.Errorf("memory store: extract: %w", err)
	}

	sp.SetAttribute("record.count", len(records))
	rt.Metrics.IncrementCounter(ctx, types.MetricMemoryExtractCompleted)
	rt.Metrics.RecordHistogram(ctx, types.MetricMemoryExtractLatencyMs, latency)
	log.Debug(ctx, "runtime: memory extract completed",
		slog.String("scope", "runtime"),
		slog.Int("records", len(records)))

	return records, nil
}

func (rt *Runtime) recordMemoryExtractUnavailable(ctx context.Context, log logger.Logger) {
	ctx, sp := rt.Tracer.StartSpan(ctx, "memory.extract")
	defer sp.End()
	sp.RecordError(errMemoryExtractUnavailable)
	sp.SetAttribute("reason", "no_extractor")

	rt.Metrics.IncrementCounter(ctx, types.MetricMemoryExtractFailed)
	log.Warn(ctx, "runtime: memory extract unavailable",
		slog.String("scope", "runtime"),
		slog.Any("error", errMemoryExtractUnavailable))
}

// resolveMemoryExtractFunc returns the user Extract hook or the SDK default when Always store is enabled.
func (rt *Runtime) resolveMemoryExtractFunc() memory.ExtractFunc {
	if !rt.RunEndMemoryStoreEnabled() {
		return nil
	}
	if extract := rt.AgentConfig.Memory.Config.Store.Extract; extract != nil {
		return extract
	}
	if client := rt.AgentConfig.LLM.Client; client != nil {
		return defaultMemoryExtractFunc(client)
	}
	return nil
}

func defaultMemoryExtractFunc(client interfaces.LLMClient) memory.ExtractFunc {
	return func(ctx context.Context, messages []interfaces.Message) ([]interfaces.MemoryRecord, error) {
		return extractMemoriesWithLLM(ctx, client, messages)
	}
}

func extractMemoriesWithLLM(ctx context.Context, client interfaces.LLMClient, messages []interfaces.Message) ([]interfaces.MemoryRecord, error) {
	msgs := messagesForMemoryExtraction(messages)
	if len(msgs) == 0 {
		return nil, nil
	}

	resp, err := client.Generate(ctx, &interfaces.LLMRequest{
		SystemMessage:  memoryExtractSystemPrompt,
		Messages:       msgs,
		ResponseFormat: memoryExtractResponseFormat(),
	})
	if err != nil {
		return nil, fmt.Errorf("memory extract: llm: %w", err)
	}

	return parseMemoryExtractResponse(resp.Content)
}

const memoryExtractTurnPrompt = "Extract durable memories from the conversation above."

func messagesForMemoryExtraction(messages []interfaces.Message) []interfaces.Message {
	out := make([]interfaces.Message, 0, len(messages)+1)
	for _, m := range messages {
		switch m.Role {
		case interfaces.MessageRoleUser, interfaces.MessageRoleAssistant:
			if strings.TrimSpace(m.Content) != "" {
				out = append(out, m)
			}
		}
	}
	if len(out) == 0 {
		return nil
	}
	// Structured output providers (e.g. Anthropic) reject assistant as the final message.
	if out[len(out)-1].Role == interfaces.MessageRoleAssistant {
		out = append(out, interfaces.Message{
			Role:    interfaces.MessageRoleUser,
			Content: memoryExtractTurnPrompt,
		})
	}
	return out
}

func memoryExtractResponseFormat() *interfaces.ResponseFormat {
	return &interfaces.ResponseFormat{
		Type: interfaces.ResponseFormatJSON,
		Name: "MemoryExtraction",
		Schema: interfaces.JSONSchema{
			"type": "object",
			"properties": interfaces.JSONSchema{
				"memories": interfaces.JSONSchema{
					"type": "array",
					"items": interfaces.JSONSchema{
						"type": "object",
						"properties": interfaces.JSONSchema{
							"text": interfaces.JSONSchema{
								"type":        "string",
								"description": "Distilled memory text",
							},
							"kind": interfaces.JSONSchema{
								"type":        "string",
								"description": "Optional kind: preference, fact, decision, instruction, note",
							},
						},
						"required":             []any{"text"},
						"additionalProperties": false,
					},
				},
			},
			"required":             []any{"memories"},
			"additionalProperties": false,
		},
	}
}

func parseMemoryExtractResponse(content string) ([]interfaces.MemoryRecord, error) {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, nil
	}

	var parsed struct {
		Memories []struct {
			Text string `json:"text"`
			Kind string `json:"kind"`
		} `json:"memories"`
	}
	if err := json.Unmarshal([]byte(content), &parsed); err != nil {
		return nil, fmt.Errorf("memory extract: parse response: %w", err)
	}

	records := make([]interfaces.MemoryRecord, 0, len(parsed.Memories))
	for _, m := range parsed.Memories {
		text := strings.TrimSpace(m.Text)
		if text == "" {
			continue
		}
		rec := interfaces.MemoryRecord{
			Text: text,
			Metadata: map[string]string{
				"source": "extract",
			},
		}
		if kind := strings.TrimSpace(m.Kind); kind != "" {
			rec.Kind = interfaces.MemoryKind(kind)
		}
		records = append(records, rec)
	}
	return records, nil
}

func loadOptionsFromCall(call hooks.MemoryLoadCall, withMinScore bool) []interfaces.LoadMemoryOption {
	opts := []interfaces.LoadMemoryOption{
		interfaces.WithLoadLimit(call.Limit),
	}
	if withMinScore && call.MinScore > 0 {
		opts = append(opts, interfaces.WithMinScore(call.MinScore))
	}
	if len(call.Kinds) > 0 {
		opts = append(opts, interfaces.WithLoadKinds(call.Kinds...))
	}
	return opts
}
