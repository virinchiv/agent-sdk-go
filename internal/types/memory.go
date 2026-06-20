package types

// MemoryEntryFormat is the printf format used to render a single [interfaces.MemoryEntry] for LLM context.
// Arguments: 1-based index (int), text (string), kind (string), score (float32).
const MemoryEntryFormat = "[%d] %s\n(kind: %s, score: %.2f)\n\n"

// SaveMemoryToolName is the LLM-facing tool name for on-demand long-term memory store.
const SaveMemoryToolName = "save_memory"

// Memory tool JSON parameter names.
const (
	MemoryToolParamText = "text"
	MemoryToolParamKind = "kind"
)
