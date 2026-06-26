package base

import (
	"context"
	"testing"

	"github.com/agenticenv/agent-sdk-go/internal/hooks"
	sdkruntime "github.com/agenticenv/agent-sdk-go/internal/runtime"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

func TestRunBeforeMemoryLoadHooks_noHooksPassthrough(t *testing.T) {
	rt := newTestRuntime(sdkruntime.AgentConfig{})
	call := hooks.MemoryLoadCall{Query: "q", Limit: 5}
	got, err := rt.runBeforeMemoryLoadHooks(context.Background(), ExecuteMemoryRecallInput{
		RunID: "run-1", Iteration: 0,
	}, call)
	if err != nil {
		t.Fatal(err)
	}
	if got.Query != call.Query || got.Limit != call.Limit {
		t.Fatalf("got %#v", got)
	}
}

func TestRunBeforeMemoryLoadHooks_delegates(t *testing.T) {
	rt := newTestRuntime(sdkruntime.AgentConfig{
		Hooks: []hooks.HookGroup{{
			Name: "rewrite",
			Hooks: hooks.AgentHooks{BeforeMemoryLoad: []hooks.BeforeMemoryLoadHook{
				func(_ context.Context, in hooks.BeforeMemoryLoadHookInput) (hooks.BeforeMemoryLoadHookOutput, error) {
					if in.RunMeta.RunID != "run-1" || in.RunMeta.Iteration != 2 || in.RunMeta.HooksGroup != "rewrite" {
						t.Fatalf("RunMeta = %#v", in.RunMeta)
					}
					return hooks.BeforeMemoryLoadHookOutput{Query: "hooked"}, nil
				},
			}},
		}},
	})
	got, err := rt.runBeforeMemoryLoadHooks(context.Background(), ExecuteMemoryRecallInput{
		RunID: "run-1", Iteration: 2,
	}, hooks.MemoryLoadCall{Query: "orig"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Query != "hooked" {
		t.Fatalf("query = %q", got.Query)
	}
}

func TestRunAfterMemoryLoadHooks_delegates(t *testing.T) {
	rt := newTestRuntime(sdkruntime.AgentConfig{
		Hooks: []hooks.HookGroup{{
			Name: "filter",
			Hooks: hooks.AgentHooks{AfterMemoryLoad: []hooks.AfterMemoryLoadHook{
				func(_ context.Context, in hooks.AfterMemoryLoadHookInput) (hooks.AfterMemoryLoadHookOutput, error) {
					return hooks.AfterMemoryLoadHookOutput{PromptContext: "filtered"}, nil
				},
			}},
		}},
	})
	ctx, err := rt.runAfterMemoryLoadHooks(context.Background(), ExecuteMemoryRecallInput{
		RunID: "run-1", Iteration: 0,
	}, hooks.MemoryLoadCall{Query: "q"}, "raw")
	if err != nil {
		t.Fatal(err)
	}
	if ctx != "filtered" {
		t.Fatalf("ctx = %q", ctx)
	}
}

func TestRunBeforeMemoryStoreHooks_delegates(t *testing.T) {
	rt := newTestRuntime(sdkruntime.AgentConfig{
		Hooks: []hooks.HookGroup{{
			Name: "scrub",
			Hooks: hooks.AgentHooks{BeforeMemoryStore: []hooks.BeforeMemoryStoreHook{
				func(_ context.Context, in hooks.BeforeMemoryStoreHookInput) (hooks.BeforeMemoryStoreHookOutput, error) {
					return hooks.BeforeMemoryStoreHookOutput{
						Record: interfaces.MemoryRecord{Text: "scrubbed"},
						ID:     "id-1",
					}, nil
				},
			}},
		}},
	})
	got, err := rt.runBeforeMemoryStoreHooks(context.Background(), StoreMemoryRecordsInput{
		RunID: "run-1", Iteration: 1,
	}, hooks.MemoryStoreCall{
		Record: interfaces.MemoryRecord{Text: "raw"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Record.Text != "scrubbed" || got.ID != "id-1" {
		t.Fatalf("call = %#v", got)
	}
}

func TestRunAfterMemoryStoreHooks_delegates(t *testing.T) {
	var seen bool
	rt := newTestRuntime(sdkruntime.AgentConfig{
		Hooks: []hooks.HookGroup{{
			Name: "audit",
			Hooks: hooks.AgentHooks{AfterMemoryStore: []hooks.AfterMemoryStoreHook{
				func(_ context.Context, in hooks.AfterMemoryStoreHookInput) (hooks.AfterMemoryStoreHookOutput, error) {
					seen = in.ID == "mem-id"
					return hooks.AfterMemoryStoreHookOutput{}, nil
				},
			}},
		}},
	})
	call := hooks.MemoryStoreCall{
		Record: interfaces.MemoryRecord{Text: "stored"},
		ID:     "mem-id",
	}
	if err := rt.runAfterMemoryStoreHooks(context.Background(), StoreMemoryRecordsInput{
		RunID: "run-1", Iteration: 0,
	}, call); err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Fatal("after store hook was not called")
	}
}
