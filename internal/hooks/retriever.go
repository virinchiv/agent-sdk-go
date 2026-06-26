package hooks

import (
	"context"

	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

// RetrieveCall is the resolved retrieval invocation used by the runtime hook runner.
type RetrieveCall struct {
	Query         string
	Mode          types.RetrieverMode
	RetrieverName string
}

// BeforeRetrieveHookInput is the payload passed to [BeforeRetrieveHook] before a retrieval runs.
type BeforeRetrieveHookInput struct {
	RunMeta RunMeta
	Query   string
	Mode    types.RetrieverMode

	// RetrieverName is the target retriever when agentic; empty when prefetch runs all configured retrievers.
	RetrieverName string
}

// BeforeRetrieveHookOutput is the mutable result returned from [BeforeRetrieveHook].
// Only Query may be changed; Mode and RetrieverName on input are read-only context.
type BeforeRetrieveHookOutput struct {
	Query string
}

// AfterRetrieveHookInput is the payload passed to [AfterRetrieveHook] after documents are retrieved.
type AfterRetrieveHookInput struct {
	RunMeta       RunMeta
	Query         string
	Mode          types.RetrieverMode
	RetrieverName string
	Documents     []interfaces.Document
}

// AfterRetrieveHookOutput is the mutable result returned from [AfterRetrieveHook].
type AfterRetrieveHookOutput struct {
	Documents []interfaces.Document
}

// BeforeRetrieveHook runs before retrieval. Return a modified query or an error to abort the run.
type BeforeRetrieveHook func(ctx context.Context, input BeforeRetrieveHookInput) (BeforeRetrieveHookOutput, error)

// AfterRetrieveHook runs after retrieval. Return filtered or re-ranked documents or an error to abort the run.
type AfterRetrieveHook func(ctx context.Context, input AfterRetrieveHookInput) (AfterRetrieveHookOutput, error)

// RunBeforeRetrieve runs all BeforeRetrieve hooks in hook group registration order. Hooks within a
// group run in declaration order; each hook receives the output of the previous hook. The first
// error aborts the remaining chain. Returns call unchanged when groups is empty or no
// BeforeRetrieve hooks are registered.
func RunBeforeRetrieve(ctx context.Context, groups []HookGroup, meta RunMeta, call RetrieveCall) (RetrieveCall, error) {
	current := call
	for _, g := range groups {
		if len(g.Hooks.BeforeRetrieve) == 0 {
			continue
		}
		groupMeta := meta
		groupMeta.HooksGroup = g.Name
		for _, hook := range g.Hooks.BeforeRetrieve {
			if hook == nil {
				continue
			}
			out, err := hook(ctx, BeforeRetrieveHookInput{
				RunMeta:       groupMeta,
				Query:         current.Query,
				Mode:          current.Mode,
				RetrieverName: current.RetrieverName,
			})
			if err != nil {
				return RetrieveCall{}, err
			}
			current.Query = out.Query
		}
	}
	return current, nil
}

// RunAfterRetrieve runs all AfterRetrieve hooks in hook group registration order. Hooks within a
// group run in declaration order; each hook receives the output of the previous hook. The first
// error aborts the remaining chain. Returns documents unchanged when groups is empty or no
// AfterRetrieve hooks are registered.
func RunAfterRetrieve(ctx context.Context, groups []HookGroup, meta RunMeta, call RetrieveCall, documents []interfaces.Document) ([]interfaces.Document, error) {
	current := documents
	for _, g := range groups {
		if len(g.Hooks.AfterRetrieve) == 0 {
			continue
		}
		groupMeta := meta
		groupMeta.HooksGroup = g.Name
		for _, hook := range g.Hooks.AfterRetrieve {
			if hook == nil {
				continue
			}
			out, err := hook(ctx, AfterRetrieveHookInput{
				RunMeta:       groupMeta,
				Query:         call.Query,
				Mode:          call.Mode,
				RetrieverName: call.RetrieverName,
				Documents:     cloneDocuments(current),
			})
			if err != nil {
				return nil, err
			}
			current = cloneDocuments(out.Documents)
		}
	}
	return current, nil
}

func cloneDocuments(docs []interfaces.Document) []interfaces.Document {
	if len(docs) == 0 {
		return nil
	}
	out := make([]interfaces.Document, len(docs))
	copy(out, docs)
	return out
}
