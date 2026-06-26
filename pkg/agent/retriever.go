package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
	"github.com/agenticenv/agent-sdk-go/pkg/tools"
)

var _ interfaces.Tool = (*RetrieverTool)(nil)
var _ types.ToolKindProvider = (*RetrieverTool)(nil)

// RetrieverTool implements [interfaces.Tool] for [RetrieverModeAgentic] and [RetrieverModeHybrid].
type RetrieverTool struct {
	// RetrieverName is the stable key from [interfaces.Retriever.Name] (used in tool name / display name).
	RetrieverName string
	Retriever     interfaces.Retriever
}

// NewRetrieverTool builds a RetrieverTool. Returns nil when retriever is nil or [interfaces.Retriever.Name] is empty.
func NewRetrieverTool(retriever interfaces.Retriever) interfaces.Tool {
	if retriever == nil {
		return nil
	}
	rn := strings.TrimSpace(retriever.Name())
	if rn == "" {
		return nil
	}
	return &RetrieverTool{RetrieverName: rn, Retriever: retriever}
}

// ToolKind implements [types.ToolKindProvider].
func (t *RetrieverTool) ToolKind() types.ToolKind { return types.ToolKindRetriever }

// Name implements [interfaces.Tool].
func (t *RetrieverTool) Name() string {
	if t == nil {
		return ""
	}
	return types.RetrieverToolName(t.RetrieverName)
}

// DisplayName implements [interfaces.Tool].
func (t *RetrieverTool) DisplayName() string {
	if t == nil {
		return ""
	}
	return types.RetrieverToolDisplayName(t.RetrieverName)
}

// Description implements [interfaces.Tool].
func (t *RetrieverTool) Description() string {
	if t == nil {
		return ""
	}
	return fmt.Sprintf(
		"Search the %s knowledge base for relevant context. "+
			"Call this when you need external knowledge to answer the user query.",
		t.RetrieverName,
	)
}

// Parameters implements [interfaces.Tool]. Requires [types.RetrieverToolParamQuery].
func (t *RetrieverTool) Parameters() interfaces.JSONSchema {
	if t == nil {
		return interfaces.JSONSchema{"type": "object"}
	}
	return tools.Params(map[string]interfaces.JSONSchema{
		types.RetrieverToolParamQuery: tools.ParamString(
			fmt.Sprintf("Search query to find relevant knowledge in %s", t.RetrieverName),
		),
	}, types.RetrieverToolParamQuery)
}

// Execute implements [interfaces.Tool]: reads the query argument, calls [interfaces.Retriever.Search],
// and returns matching documents. Formatting for the LLM is done by the runtime.
func (t *RetrieverTool) Execute(ctx context.Context, args map[string]any) (any, error) {
	if t.Retriever == nil {
		return nil, fmt.Errorf("retriever tool: nil retriever")
	}
	query, err := types.RetrieverToolParamQueryValue(args)
	if err != nil {
		return nil, err
	}
	return t.Retriever.Search(ctx, query)
}

// ---------------------------------------------------------------------------
// Fingerprint
// ---------------------------------------------------------------------------

// retrieverConfigFingerprint returns a stable SHA-256 digest of retriever mode and retriever names
// for [agentConfig.agentConfigFingerprint]. Names are deduplicated, whitespace-trimmed, and sorted
// for stability. Returns "" for [RetrieverModeAgentic] with no retrievers (no fingerprint contribution).
func retrieverConfigFingerprint(mode types.RetrieverMode, retrievers []interfaces.Retriever) string {
	rm := mode
	if rm == "" {
		rm = types.RetrieverModeAgentic
	}
	seen := make(map[string]struct{}, len(retrievers))
	var names []string
	for _, r := range retrievers {
		if r == nil {
			continue
		}
		n := strings.TrimSpace(r.Name())
		if n == "" {
			continue
		}
		if _, dup := seen[n]; dup {
			continue
		}
		seen[n] = struct{}{}
		names = append(names, n)
	}
	sort.Strings(names)
	if len(names) == 0 && rm == types.RetrieverModeAgentic {
		return ""
	}
	b, err := json.Marshal(struct {
		Mode  string   `json:"mode"`
		Names []string `json:"names,omitempty"`
	}{Mode: string(rm), Names: names})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
