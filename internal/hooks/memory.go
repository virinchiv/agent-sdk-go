package hooks

import (
	"context"

	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

// BeforeMemoryLoadHookInput is the payload passed to [BeforeMemoryLoadHook] before memories are loaded.
type BeforeMemoryLoadHookInput struct {
	RunMeta RunMeta
	Scope   interfaces.MemoryScope
	Query   string

	// Limit is the maximum number of memories to return. Zero means backend default.
	Limit int

	// MinScore filters out entries below the given relevance score when Score is applicable.
	MinScore float32

	// Kinds restricts recall to the given memory kinds. Empty means all kinds.
	Kinds []interfaces.MemoryKind
}

// BeforeMemoryLoadHookOutput is the mutable result returned from [BeforeMemoryLoadHook].
// Only Query and load options may be changed; Scope on input is read-only context.
type BeforeMemoryLoadHookOutput struct {
	Query    string
	Limit    int
	MinScore float32
	Kinds    []interfaces.MemoryKind
}

// AfterMemoryLoadHookInput is the payload passed to [AfterMemoryLoadHook] after memories are loaded.
type AfterMemoryLoadHookInput struct {
	RunMeta RunMeta
	Scope   interfaces.MemoryScope
	Query   string

	// PromptContext is the formatted memory block injected into the LLM system prompt.
	PromptContext string
}

// AfterMemoryLoadHookOutput is the mutable result returned from [AfterMemoryLoadHook].
type AfterMemoryLoadHookOutput struct {
	PromptContext string
}

// BeforeMemoryStoreHookInput is the payload passed to [BeforeMemoryStoreHook] before a memory is stored.
type BeforeMemoryStoreHookInput struct {
	RunMeta RunMeta
	Scope   interfaces.MemoryScope
	Record  interfaces.MemoryRecord

	// ID upserts the record when non-empty.
	ID string
}

// BeforeMemoryStoreHookOutput is the mutable result returned from [BeforeMemoryStoreHook].
// Only Record and ID may be changed; Scope on input is read-only context.
type BeforeMemoryStoreHookOutput struct {
	Record interfaces.MemoryRecord
	ID     string
}

// AfterMemoryStoreHookInput is the payload passed to [AfterMemoryStoreHook] after a memory is stored.
type AfterMemoryStoreHookInput struct {
	RunMeta RunMeta
	Scope   interfaces.MemoryScope
	Record  interfaces.MemoryRecord

	// ID is the record identifier assigned by the backend after a successful store.
	ID string
}

// AfterMemoryStoreHookOutput is the mutable result returned from [AfterMemoryStoreHook].
// Store has already completed; hooks use input for audit and may abort via error only.
type AfterMemoryStoreHookOutput struct{}

// BeforeMemoryLoadHook runs before memory load. Return modified query or load options, or an error to abort the run.
type BeforeMemoryLoadHook func(ctx context.Context, input BeforeMemoryLoadHookInput) (BeforeMemoryLoadHookOutput, error)

// AfterMemoryLoadHook runs after memory load. Return a modified prompt context or an error to abort the run.
type AfterMemoryLoadHook func(ctx context.Context, input AfterMemoryLoadHookInput) (AfterMemoryLoadHookOutput, error)

// BeforeMemoryStoreHook runs before memory store. Return modified record or upsert ID, or an error to abort the run.
type BeforeMemoryStoreHook func(ctx context.Context, input BeforeMemoryStoreHookInput) (BeforeMemoryStoreHookOutput, error)

// AfterMemoryStoreHook runs after memory store. Return an error to abort the run.
type AfterMemoryStoreHook func(ctx context.Context, input AfterMemoryStoreHookInput) (AfterMemoryStoreHookOutput, error)

// MemoryLoadCall is the resolved memory load invocation used by the runtime hook runner.
type MemoryLoadCall struct {
	Scope    interfaces.MemoryScope
	Query    string
	Limit    int
	MinScore float32
	Kinds    []interfaces.MemoryKind
}

// MemoryStoreCall is the resolved memory store invocation used by the runtime hook runner.
type MemoryStoreCall struct {
	Scope  interfaces.MemoryScope
	Record interfaces.MemoryRecord
	ID     string
}

// RunBeforeMemoryLoad runs all BeforeMemoryLoad hooks in hook group registration order.
// Only Query and load options may be changed; Scope on call is read-only context.
// Returns call unchanged when groups is empty or no BeforeMemoryLoad hooks are registered.
func RunBeforeMemoryLoad(ctx context.Context, groups []HookGroup, meta RunMeta, call MemoryLoadCall) (MemoryLoadCall, error) {
	current := call
	for _, g := range groups {
		if len(g.Hooks.BeforeMemoryLoad) == 0 {
			continue
		}
		groupMeta := meta
		groupMeta.HooksGroup = g.Name
		for _, hook := range g.Hooks.BeforeMemoryLoad {
			if hook == nil {
				continue
			}
			out, err := hook(ctx, BeforeMemoryLoadHookInput{
				RunMeta:  groupMeta,
				Scope:    current.Scope,
				Query:    current.Query,
				Limit:    current.Limit,
				MinScore: current.MinScore,
				Kinds:    cloneKinds(current.Kinds),
			})
			if err != nil {
				return MemoryLoadCall{}, err
			}
			current.Query = out.Query
			current.Limit = out.Limit
			current.MinScore = out.MinScore
			current.Kinds = cloneKinds(out.Kinds)
		}
	}
	return current, nil
}

// RunAfterMemoryLoad runs all AfterMemoryLoad hooks in hook group registration order.
// Scope and Query on input are read-only context; only PromptContext may change.
// Returns promptContext unchanged when groups is empty or no AfterMemoryLoad hooks are registered.
func RunAfterMemoryLoad(ctx context.Context, groups []HookGroup, meta RunMeta, call MemoryLoadCall, promptContext string) (string, error) {
	currentContext := promptContext
	for _, g := range groups {
		if len(g.Hooks.AfterMemoryLoad) == 0 {
			continue
		}
		groupMeta := meta
		groupMeta.HooksGroup = g.Name
		for _, hook := range g.Hooks.AfterMemoryLoad {
			if hook == nil {
				continue
			}
			out, err := hook(ctx, AfterMemoryLoadHookInput{
				RunMeta:       groupMeta,
				Scope:         call.Scope,
				Query:         call.Query,
				PromptContext: currentContext,
			})
			if err != nil {
				return "", err
			}
			currentContext = out.PromptContext
		}
	}
	return currentContext, nil
}

// RunBeforeMemoryStore runs all BeforeMemoryStore hooks in hook group registration order.
// Only Record and ID may be changed; Scope on call is read-only context.
// Returns call unchanged when groups is empty or no BeforeMemoryStore hooks are registered.
func RunBeforeMemoryStore(ctx context.Context, groups []HookGroup, meta RunMeta, call MemoryStoreCall) (MemoryStoreCall, error) {
	current := call
	for _, g := range groups {
		if len(g.Hooks.BeforeMemoryStore) == 0 {
			continue
		}
		groupMeta := meta
		groupMeta.HooksGroup = g.Name
		for _, hook := range g.Hooks.BeforeMemoryStore {
			if hook == nil {
				continue
			}
			out, err := hook(ctx, BeforeMemoryStoreHookInput{
				RunMeta: groupMeta,
				Scope:   current.Scope,
				Record:  current.Record,
				ID:      current.ID,
			})
			if err != nil {
				return MemoryStoreCall{}, err
			}
			current.Record, current.ID = out.Record, out.ID
		}
	}
	return current, nil
}

// RunAfterMemoryStore runs all AfterMemoryStore hooks in hook group registration order.
// Returns nil when groups is empty or no AfterMemoryStore hooks are registered.
func RunAfterMemoryStore(ctx context.Context, groups []HookGroup, meta RunMeta, call MemoryStoreCall) error {
	for _, g := range groups {
		if len(g.Hooks.AfterMemoryStore) == 0 {
			continue
		}
		groupMeta := meta
		groupMeta.HooksGroup = g.Name
		for _, hook := range g.Hooks.AfterMemoryStore {
			if hook == nil {
				continue
			}
			if _, err := hook(ctx, AfterMemoryStoreHookInput{
				RunMeta: groupMeta,
				Scope:   call.Scope,
				Record:  call.Record,
				ID:      call.ID,
			}); err != nil {
				return err
			}
		}
	}
	return nil
}

func cloneKinds(kinds []interfaces.MemoryKind) []interfaces.MemoryKind {
	if len(kinds) == 0 {
		return nil
	}
	out := make([]interfaces.MemoryKind, len(kinds))
	copy(out, kinds)
	return out
}
