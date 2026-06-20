package agent

import (
	"context"
	"fmt"
	"strings"

	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/tools"
)

var _ interfaces.Tool = (*MemoryTool)(nil)
var _ types.ToolKindProvider = (*MemoryTool)(nil)

// ErrMemoryToolNotExecutable is returned when save_memory runs outside a managed agent runtime.
var ErrMemoryToolNotExecutable = fmt.Errorf("save_memory must be executed via runtime")

// MemoryStoreFunc persists extracted memory records for the current run scope.
type MemoryStoreFunc func(ctx context.Context, records []interfaces.MemoryRecord) error

// MemoryTool implements [interfaces.Tool] for on-demand long-term memory store ([memory.StoreModeOnDemand]).
type MemoryTool struct {
	Store MemoryStoreFunc
}

// NewMemoryTool returns a save_memory tool. Returns nil when store is nil.
func NewMemoryTool(store MemoryStoreFunc) interfaces.Tool {
	if store == nil {
		return nil
	}
	return &MemoryTool{Store: store}
}

// NewRegisteredMemoryTool returns save_memory for agent tool registration ([memory.StoreModeOnDemand]).
func NewRegisteredMemoryTool() interfaces.Tool {
	return &MemoryTool{}
}

// ToolKind implements [types.ToolKindProvider].
func (t *MemoryTool) ToolKind() types.ToolKind { return types.ToolKindMemory }

// Name implements [interfaces.Tool].
func (t *MemoryTool) Name() string { return types.SaveMemoryToolName }

// DisplayName implements [interfaces.Tool].
func (t *MemoryTool) DisplayName() string { return "Save Memory" }

// Description implements [interfaces.Tool].
func (t *MemoryTool) Description() string {
	return "Save a fact, preference, or decision to long-term memory for future runs. " +
		"Required when the user asks to remember, save, or persist something for later — " +
		"call this tool before acknowledging; a text reply alone does not store memory."
}

// Parameters implements [interfaces.Tool].
func (t *MemoryTool) Parameters() interfaces.JSONSchema {
	return tools.Params(map[string]interfaces.JSONSchema{
		types.MemoryToolParamText: tools.ParamString(
			"The memory text to store (fact, preference, or decision distilled from the conversation)",
		),
		types.MemoryToolParamKind: tools.ParamString(
			"Optional memory kind (e.g. preference, fact, decision, instruction, note)",
		),
	}, types.MemoryToolParamText)
}

// Execute implements [interfaces.Tool].
func (t *MemoryTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	if t.Store == nil {
		return nil, ErrMemoryToolNotExecutable
	}
	rawText, ok := args[types.MemoryToolParamText].(string)
	if !ok {
		return nil, fmt.Errorf("save_memory: %q parameter required", types.MemoryToolParamText)
	}
	text := strings.TrimSpace(rawText)
	if text == "" {
		return nil, fmt.Errorf("save_memory: %q must be non-empty", types.MemoryToolParamText)
	}

	record := interfaces.MemoryRecord{Text: text}
	if rawKind, ok := args[types.MemoryToolParamKind].(string); ok {
		record.Kind = interfaces.MemoryKind(strings.TrimSpace(rawKind))
	}

	if err := t.Store(ctx, []interfaces.MemoryRecord{record}); err != nil {
		return nil, err
	}
	return "memory saved", nil
}
