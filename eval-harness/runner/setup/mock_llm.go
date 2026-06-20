package setup

import (
	"context"
	"fmt"
	"math/rand"
	"time"

	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

const mockLLMModel = "eval-mock"

const (
	mockMemoryExtractText = "User prefers concise answers"
	mockRecallContent     = "You prefer concise answers."
)

// MockLLMClient is a deterministic mock LLM for eval harness runs.
type MockLLMClient struct {
	cfg LLMConfig
	rng *rand.Rand
}

// NewMockLLMClient builds a mock LLM client from cfg.
func NewMockLLMClient(cfg LLMConfig, rng *rand.Rand) *MockLLMClient {
	if rng == nil {
		rng = rand.New(rand.NewSource(time.Now().UnixNano()))
	}
	return &MockLLMClient{cfg: cfg, rng: rng}
}

func (m *MockLLMClient) Generate(ctx context.Context, request *interfaces.LLMRequest) (*interfaces.LLMResponse, error) {
	promptTokens, completionTokens := splitMockTokens(m.cfg.MockTokens)
	usage := &interfaces.LLMUsage{
		PromptTokens:     int64(promptTokens),
		CompletionTokens: int64(completionTokens),
		TotalTokens:      int64(promptTokens + completionTokens),
	}

	if isMemoryExtractRequest(request) {
		return &interfaces.LLMResponse{
			Content: fmt.Sprintf(`{"memories":[{"text":%q,"kind":"preference"}]}`, mockMemoryExtractText),
			Usage:   usage,
		}, nil
	}

	if hasToolResultMessages(request) {
		return &interfaces.LLMResponse{
			Content: "eval complete",
			Usage:   usage,
		}, nil
	}

	if len(request.Tools) == 0 {
		return &interfaces.LLMResponse{
			Content: mockRecallContent,
			Usage:   usage,
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
		Usage:     usage,
	}, nil
}

func mockToolArgs(toolName string) map[string]any {
	if toolName == types.SaveMemoryToolName {
		return map[string]any{
			types.MemoryToolParamText: mockMemoryExtractText,
			types.MemoryToolParamKind: "preference",
		}
	}
	return map[string]any{"input": "eval"}
}

func isMemoryExtractRequest(request *interfaces.LLMRequest) bool {
	if request == nil || request.ResponseFormat == nil {
		return false
	}
	return request.ResponseFormat.Type == interfaces.ResponseFormatJSON &&
		request.ResponseFormat.Name == "MemoryExtraction"
}

func (m *MockLLMClient) GenerateStream(ctx context.Context, request *interfaces.LLMRequest) (interfaces.LLMStream, error) {
	resp, err := m.Generate(ctx, request)
	if err != nil {
		return nil, err
	}
	return &mockLLMStream{resp: resp}, nil
}

func (m *MockLLMClient) GetModel() string { return mockLLMModel }

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

func splitMockTokens(total int) (prompt, completion int) {
	if total <= 0 {
		return 0, 0
	}
	prompt = total * 3 / 5
	completion = total - prompt
	return prompt, completion
}
