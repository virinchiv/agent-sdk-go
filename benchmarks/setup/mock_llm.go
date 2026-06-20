package setup

import (
	"context"
	"fmt"
	"math/rand"
	"strings"
	"sync"
	"time"

	"github.com/agenticenv/agent-sdk-go/internal/runtime"
	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

const MockLLMModel = "benchmark-mock"

const mockMemoryExtractText = "User prefers concise answers"

type LLMStats struct {
	mu                sync.Mutex
	TotalInputTokens  int
	TotalOutputTokens int
}

func NewLLMStats() *LLMStats { return &LLMStats{} }

func (s *LLMStats) add(input, output int) {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.TotalInputTokens += input
	s.TotalOutputTokens += output
	s.mu.Unlock()
}

func (s *LLMStats) Snapshot() (input, output int) {
	if s == nil {
		return 0, 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.TotalInputTokens, s.TotalOutputTokens
}

type MockLLMClient struct {
	cfg   LLMConfig
	stats *LLMStats
	rng   *rand.Rand
}

func NewMockLLMClient(cfg LLMConfig, stats *LLMStats, rng *rand.Rand) *MockLLMClient {
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return &MockLLMClient{cfg: cfg, stats: stats, rng: rng}
}

func (m *MockLLMClient) Stats() *LLMStats { return m.stats }

func (m *MockLLMClient) Generate(ctx context.Context, request *interfaces.LLMRequest) (*interfaces.LLMResponse, error) {
	if err := sleepWithJitter(ctx, m.cfg.LatencyMs, m.cfg.JitterMs, m.rng); err != nil {
		return nil, err
	}

	promptTokens, completionTokens := splitMockTokens(m.cfg.MockTokens)
	m.stats.add(promptTokens, completionTokens)

	if isMemoryExtractRequest(request) {
		return &interfaces.LLMResponse{
			Content: fmt.Sprintf(`{"memories":[{"text":%q,"kind":"preference"}]}`, mockMemoryExtractText),
			Usage: &interfaces.LLMUsage{
				PromptTokens:     int64(promptTokens),
				CompletionTokens: int64(completionTokens),
				TotalTokens:      int64(promptTokens + completionTokens),
			},
		}, nil
	}

	if hasToolResultMessages(request) {
		return &interfaces.LLMResponse{
			Content: "benchmark complete",
			Usage: &interfaces.LLMUsage{
				PromptTokens:     int64(promptTokens),
				CompletionTokens: int64(completionTokens),
				TotalTokens:      int64(promptTokens + completionTokens),
			},
		}, nil
	}

	toolCalls := make([]*interfaces.ToolCall, 0, len(request.Tools))
	for i, spec := range request.Tools {
		toolCalls = append(toolCalls, &interfaces.ToolCall{
			ToolCallID: fmt.Sprintf("tc-%d", i+1),
			ToolName:   spec.Name,
			Args:       mockToolArgs(spec.Name),
		})
	}

	return &interfaces.LLMResponse{
		Content:   "executing tools",
		ToolCalls: toolCalls,
		Usage: &interfaces.LLMUsage{
			PromptTokens:     int64(promptTokens),
			CompletionTokens: int64(completionTokens),
			TotalTokens:      int64(promptTokens + completionTokens),
		},
	}, nil
}

func (m *MockLLMClient) GenerateStream(ctx context.Context, request *interfaces.LLMRequest) (interfaces.LLMStream, error) {
	resp, err := m.Generate(ctx, request)
	if err != nil {
		return nil, err
	}
	return &mockLLMStream{resp: resp}, nil
}

func (m *MockLLMClient) GetModel() string { return MockLLMModel }

func (m *MockLLMClient) GetProvider() interfaces.LLMProvider {
	return interfaces.LLMProviderOpenAI
}

func (m *MockLLMClient) IsStreamSupported() bool { return false }

type mockLLMStream struct {
	resp *interfaces.LLMResponse
	done bool
	err  error
}

func (s *mockLLMStream) Next() bool {
	if s.done {
		return false
	}
	s.done = true
	return true
}

func (s *mockLLMStream) Current() *interfaces.LLMStreamChunk {
	if s.resp == nil {
		return nil
	}
	return &interfaces.LLMStreamChunk{ContentDelta: s.resp.Content, ToolCalls: s.resp.ToolCalls}
}

func (s *mockLLMStream) Err() error { return s.err }

func (s *mockLLMStream) GetResult() *interfaces.LLMResponse { return s.resp }

func hasToolResultMessages(request *interfaces.LLMRequest) bool {
	if request == nil {
		return false
	}
	for _, msg := range request.Messages {
		if msg.Role == interfaces.MessageRoleTool {
			return true
		}
	}
	return false
}

func mockToolArgs(toolName string) map[string]any {
	if toolName == types.SaveMemoryToolName {
		return map[string]any{
			types.MemoryToolParamText: mockMemoryExtractText,
			types.MemoryToolParamKind: "preference",
		}
	}
	if strings.HasPrefix(toolName, "subagent_") {
		return map[string]any{runtime.SubAgentToolParamQuery: "benchmark subtask"}
	}
	return map[string]any{"input": "benchmark"}
}

func isMemoryExtractRequest(request *interfaces.LLMRequest) bool {
	if request == nil || request.ResponseFormat == nil {
		return false
	}
	return request.ResponseFormat.Type == interfaces.ResponseFormatJSON &&
		request.ResponseFormat.Name == "MemoryExtraction"
}

func splitMockTokens(total int) (prompt, completion int) {
	if total <= 0 {
		return 0, 0
	}
	prompt = total * 3 / 5
	completion = total - prompt
	return prompt, completion
}

func sleepWithJitter(ctx context.Context, baseMs, jitterMs int, rng *rand.Rand) error {
	delay := time.Duration(baseMs) * time.Millisecond
	if jitterMs > 0 && rng != nil {
		delay += time.Duration(rng.Intn(jitterMs+1)) * time.Millisecond
	}
	select {
	case <-time.After(delay):
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
