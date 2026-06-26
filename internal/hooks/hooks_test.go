package hooks

import (
	"context"
	"errors"
	"testing"

	"github.com/agenticenv/agent-sdk-go/internal/types"
	"github.com/agenticenv/agent-sdk-go/pkg/interfaces"
)

func TestAgentHooks_Merge_Empty(t *testing.T) {
	got := (AgentHooks{}).Merge(AgentHooks{})
	if !hooksEmpty(got) {
		t.Fatal("expected empty merged hooks")
	}
}

func TestAgentHooks_Merge_AppendsInOrder(t *testing.T) {
	h1 := func(context.Context, BeforeLLMHookInput) (BeforeLLMHookOutput, error) {
		return BeforeLLMHookOutput{}, nil
	}
	h2 := func(context.Context, BeforeLLMHookInput) (BeforeLLMHookOutput, error) {
		return BeforeLLMHookOutput{}, nil
	}
	h3 := func(context.Context, AfterToolHookInput) (AfterToolHookOutput, error) {
		return AfterToolHookOutput{}, nil
	}

	base := AgentHooks{BeforeLLM: []BeforeLLMHook{h1}}
	other := AgentHooks{
		BeforeLLM: []BeforeLLMHook{h2},
		AfterTool: []AfterToolHook{h3},
	}
	got := base.Merge(other)

	if len(got.BeforeLLM) != 2 {
		t.Fatalf("BeforeLLM len = %d, want 2", len(got.BeforeLLM))
	}
	if got.BeforeLLM[0] == nil || got.BeforeLLM[1] == nil {
		t.Fatal("expected non-nil hook funcs")
	}
	if len(got.AfterTool) != 1 {
		t.Fatalf("AfterTool len = %d, want 1", len(got.AfterTool))
	}
	if len(got.BeforeTool) != 0 {
		t.Fatal("expected unrelated hook slices to stay empty")
	}
}

func TestAgentHooks_Merge_NilSlices(t *testing.T) {
	h := func(context.Context, BeforeMemoryLoadHookInput) (BeforeMemoryLoadHookOutput, error) {
		return BeforeMemoryLoadHookOutput{}, nil
	}
	got := AgentHooks{BeforeMemoryLoad: []BeforeMemoryLoadHook{h}}.Merge(AgentHooks{})
	if len(got.BeforeMemoryLoad) != 1 {
		t.Fatalf("BeforeMemoryLoad len = %d, want 1", len(got.BeforeMemoryLoad))
	}
}

func hooksEmpty(h AgentHooks) bool {
	return len(h.BeforeLLM) == 0 &&
		len(h.AfterLLM) == 0 &&
		len(h.BeforeTool) == 0 &&
		len(h.AfterTool) == 0 &&
		len(h.BeforeRetrieve) == 0 &&
		len(h.AfterRetrieve) == 0 &&
		len(h.BeforeMemoryLoad) == 0 &&
		len(h.AfterMemoryLoad) == 0 &&
		len(h.BeforeMemoryStore) == 0 &&
		len(h.AfterMemoryStore) == 0
}

func TestRunBeforeLLM_chainAndGroupOrder(t *testing.T) {
	var order []string
	groups := []HookGroup{
		{
			Name: "guardrails",
			Hooks: AgentHooks{BeforeLLM: []BeforeLLMHook{
				func(_ context.Context, in BeforeLLMHookInput) (BeforeLLMHookOutput, error) {
					order = append(order, "g1:"+in.RunMeta.HooksGroup)
					out := in.Request
					out.SystemMessage = "step1"
					return BeforeLLMHookOutput{Request: out}, nil
				},
			}},
		},
		{
			Name: "audit",
			Hooks: AgentHooks{BeforeLLM: []BeforeLLMHook{
				func(_ context.Context, in BeforeLLMHookInput) (BeforeLLMHookOutput, error) {
					order = append(order, "g2:"+in.RunMeta.HooksGroup+":"+in.Request.SystemMessage)
					out := in.Request
					out.SystemMessage = "step2"
					return BeforeLLMHookOutput{Request: out}, nil
				},
			}},
		},
	}
	meta := RunMeta{RunID: "run-1", Iteration: 3}
	got, err := RunBeforeLLM(context.Background(), groups, meta, interfaces.LLMRequest{SystemMessage: "orig"})
	if err != nil {
		t.Fatal(err)
	}
	if got.SystemMessage != "step2" {
		t.Fatalf("SystemMessage = %q, want step2", got.SystemMessage)
	}
	if len(order) != 2 || order[0] != "g1:guardrails" || order[1] != "g2:audit:step1" {
		t.Fatalf("order = %v", order)
	}
}

func TestRunBeforeLLM_errorAborts(t *testing.T) {
	boom := errors.New("blocked")
	groups := []HookGroup{{
		Name: "guardrails",
		Hooks: AgentHooks{BeforeLLM: []BeforeLLMHook{
			func(context.Context, BeforeLLMHookInput) (BeforeLLMHookOutput, error) {
				return BeforeLLMHookOutput{}, boom
			},
			func(context.Context, BeforeLLMHookInput) (BeforeLLMHookOutput, error) {
				t.Fatal("second hook should not run after error")
				return BeforeLLMHookOutput{}, nil
			},
		}},
	}}
	_, err := RunBeforeLLM(context.Background(), groups, RunMeta{}, interfaces.LLMRequest{})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}

func TestRunAfterLLM_modifiesResponse(t *testing.T) {
	groups := []HookGroup{{
		Name: "scrub",
		Hooks: AgentHooks{AfterLLM: []AfterLLMHook{
			func(_ context.Context, in AfterLLMHookInput) (AfterLLMHookOutput, error) {
				out := in.Response
				out.Content = "scrubbed"
				return AfterLLMHookOutput{Response: out}, nil
			},
		}},
	}}
	got, err := RunAfterLLM(context.Background(), groups, RunMeta{HooksGroup: "unused"}, interfaces.LLMResponse{Content: "raw"})
	if err != nil {
		t.Fatal(err)
	}
	if got.Content != "scrubbed" {
		t.Fatalf("Content = %q", got.Content)
	}
}

func TestRunBeforeTool_skipsNonEligibleKind(t *testing.T) {
	var called bool
	groups := []HookGroup{{
		Name: "guard",
		Hooks: AgentHooks{BeforeTool: []BeforeToolHook{
			func(context.Context, BeforeToolHookInput) (BeforeToolHookOutput, error) {
				called = true
				return BeforeToolHookOutput{}, nil
			},
		}},
	}}
	call := ToolCall{ID: "tc", Name: "retriever", DisplayName: "R", Kind: types.ToolKindRetriever}
	got, err := RunBeforeTool(context.Background(), groups, RunMeta{}, call)
	if err != nil {
		t.Fatal(err)
	}
	if called {
		t.Fatal("hooks should not run for retriever kind")
	}
	if got.Name != "retriever" {
		t.Fatalf("call = %#v", got)
	}
}

func TestRunBeforeTool_chainOrder(t *testing.T) {
	var order []string
	groups := []HookGroup{
		{Name: "a", Hooks: AgentHooks{BeforeTool: []BeforeToolHook{
			func(_ context.Context, in BeforeToolHookInput) (BeforeToolHookOutput, error) {
				order = append(order, "a:"+in.RunMeta.HooksGroup)
				return BeforeToolHookOutput{Args: map[string]any{"step": "1"}}, nil
			},
		}}},
		{Name: "b", Hooks: AgentHooks{BeforeTool: []BeforeToolHook{
			func(_ context.Context, in BeforeToolHookInput) (BeforeToolHookOutput, error) {
				order = append(order, "b:"+in.RunMeta.HooksGroup)
				return BeforeToolHookOutput{Args: in.Call.Args}, nil
			},
		}}},
	}
	call := ToolCall{ID: "tc", Name: "tool", DisplayName: "Tool", Kind: types.ToolKindNative, Args: map[string]any{"step": "0"}}
	got, err := RunBeforeTool(context.Background(), groups, RunMeta{}, call)
	if err != nil {
		t.Fatal(err)
	}
	if got.Args["step"] != "1" {
		t.Fatalf("args = %#v", got.Args)
	}
	if got.Name != "tool" || got.Kind != types.ToolKindNative {
		t.Fatalf("read-only fields changed: %#v", got)
	}
	if len(order) != 2 || order[0] != "a:a" || order[1] != "b:b" {
		t.Fatalf("order = %v", order)
	}
}

func TestRunBeforeRetrieve_chainOrder(t *testing.T) {
	var order []string
	groups := []HookGroup{
		{Name: "a", Hooks: AgentHooks{BeforeRetrieve: []BeforeRetrieveHook{
			func(_ context.Context, in BeforeRetrieveHookInput) (BeforeRetrieveHookOutput, error) {
				order = append(order, "a:"+in.RunMeta.HooksGroup)
				return BeforeRetrieveHookOutput{Query: "step1"}, nil
			},
		}}},
		{Name: "b", Hooks: AgentHooks{BeforeRetrieve: []BeforeRetrieveHook{
			func(_ context.Context, in BeforeRetrieveHookInput) (BeforeRetrieveHookOutput, error) {
				order = append(order, "b:"+in.RunMeta.HooksGroup)
				return BeforeRetrieveHookOutput{Query: "step2"}, nil
			},
		}}},
	}
	q, err := RunBeforeRetrieve(context.Background(), groups, RunMeta{}, RetrieveCall{Query: "start", Mode: types.RetrieverModePrefetch, RetrieverName: "kb"})
	if err != nil {
		t.Fatal(err)
	}
	if q.Query != "step2" {
		t.Fatalf("query = %q", q.Query)
	}
	if q.Mode != types.RetrieverModePrefetch || q.RetrieverName != "kb" {
		t.Fatalf("read-only fields changed: %#v", q)
	}
	if len(order) != 2 || order[0] != "a:a" || order[1] != "b:b" {
		t.Fatalf("order = %v", order)
	}
}

func TestRunBeforeRetrieve_errorAborts(t *testing.T) {
	groups := []HookGroup{{
		Name: "block",
		Hooks: AgentHooks{BeforeRetrieve: []BeforeRetrieveHook{
			func(context.Context, BeforeRetrieveHookInput) (BeforeRetrieveHookOutput, error) {
				return BeforeRetrieveHookOutput{}, errors.New("blocked")
			},
		}},
	}}
	_, err := RunBeforeRetrieve(context.Background(), groups, RunMeta{}, RetrieveCall{Query: "q", Mode: types.RetrieverModePrefetch})
	if err == nil || err.Error() != "blocked" {
		t.Fatalf("err = %v", err)
	}
}

func TestRunAfterRetrieve_modifiesDocuments(t *testing.T) {
	groups := []HookGroup{{
		Name: "filter",
		Hooks: AgentHooks{AfterRetrieve: []AfterRetrieveHook{
			func(context.Context, AfterRetrieveHookInput) (AfterRetrieveHookOutput, error) {
				return AfterRetrieveHookOutput{Documents: []interfaces.Document{{Content: "kept"}}}, nil
			},
		}},
	}}
	docs, err := RunAfterRetrieve(context.Background(), groups, RunMeta{}, RetrieveCall{Query: "q", Mode: types.RetrieverModeAgentic, RetrieverName: "kb"},
		[]interfaces.Document{{Content: "drop"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(docs) != 1 || docs[0].Content != "kept" {
		t.Fatalf("docs = %#v", docs)
	}
}

func TestRunAfterRetrieve_errorAborts(t *testing.T) {
	groups := []HookGroup{{
		Name: "block",
		Hooks: AgentHooks{AfterRetrieve: []AfterRetrieveHook{
			func(context.Context, AfterRetrieveHookInput) (AfterRetrieveHookOutput, error) {
				return AfterRetrieveHookOutput{}, errors.New("blocked")
			},
		}},
	}}
	_, err := RunAfterRetrieve(context.Background(), groups, RunMeta{}, RetrieveCall{Query: "q", Mode: types.RetrieverModePrefetch, RetrieverName: "kb"},
		[]interfaces.Document{{Content: "x"}})
	if err == nil || err.Error() != "blocked" {
		t.Fatalf("err = %v", err)
	}
}

func TestRunBeforeMemoryLoad_chainOrder(t *testing.T) {
	var order []string
	scope := interfaces.MemoryScope{UserID: "u1"}
	groups := []HookGroup{
		{Name: "a", Hooks: AgentHooks{BeforeMemoryLoad: []BeforeMemoryLoadHook{
			func(_ context.Context, in BeforeMemoryLoadHookInput) (BeforeMemoryLoadHookOutput, error) {
				order = append(order, "a:"+in.RunMeta.HooksGroup)
				return BeforeMemoryLoadHookOutput{Query: "step1", Limit: 3}, nil
			},
		}}},
		{Name: "b", Hooks: AgentHooks{BeforeMemoryLoad: []BeforeMemoryLoadHook{
			func(_ context.Context, in BeforeMemoryLoadHookInput) (BeforeMemoryLoadHookOutput, error) {
				order = append(order, "b:"+in.RunMeta.HooksGroup+":"+in.Query)
				return BeforeMemoryLoadHookOutput{Query: "step2", Limit: in.Limit, MinScore: 0.9}, nil
			},
		}}},
	}
	got, err := RunBeforeMemoryLoad(context.Background(), groups, RunMeta{}, MemoryLoadCall{
		Scope: scope, Query: "start", Limit: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Query != "step2" || got.Limit != 3 || got.MinScore != 0.9 {
		t.Fatalf("call = %#v", got)
	}
	if got.Scope.UserID != "u1" {
		t.Fatal("scope should be read-only")
	}
	if len(order) != 2 || order[0] != "a:a" || order[1] != "b:b:step1" {
		t.Fatalf("order = %v", order)
	}
}

func TestRunBeforeMemoryLoad_errorAborts(t *testing.T) {
	groups := []HookGroup{{
		Name: "block",
		Hooks: AgentHooks{BeforeMemoryLoad: []BeforeMemoryLoadHook{
			func(context.Context, BeforeMemoryLoadHookInput) (BeforeMemoryLoadHookOutput, error) {
				return BeforeMemoryLoadHookOutput{}, errors.New("blocked")
			},
		}},
	}}
	_, err := RunBeforeMemoryLoad(context.Background(), groups, RunMeta{}, MemoryLoadCall{Query: "q"})
	if err == nil || err.Error() != "blocked" {
		t.Fatalf("err = %v", err)
	}
}

func TestRunAfterMemoryLoad_modifiesPromptContext(t *testing.T) {
	groups := []HookGroup{{
		Name: "filter",
		Hooks: AgentHooks{AfterMemoryLoad: []AfterMemoryLoadHook{
			func(context.Context, AfterMemoryLoadHookInput) (AfterMemoryLoadHookOutput, error) {
				return AfterMemoryLoadHookOutput{PromptContext: "filtered"}, nil
			},
		}},
	}}
	ctx, err := RunAfterMemoryLoad(context.Background(), groups, RunMeta{}, MemoryLoadCall{Query: "q"}, "raw")
	if err != nil {
		t.Fatal(err)
	}
	if ctx != "filtered" {
		t.Fatalf("PromptContext = %q", ctx)
	}
}

func TestRunAfterMemoryLoad_errorAborts(t *testing.T) {
	groups := []HookGroup{{
		Name: "block",
		Hooks: AgentHooks{AfterMemoryLoad: []AfterMemoryLoadHook{
			func(context.Context, AfterMemoryLoadHookInput) (AfterMemoryLoadHookOutput, error) {
				return AfterMemoryLoadHookOutput{}, errors.New("blocked")
			},
		}},
	}}
	_, err := RunAfterMemoryLoad(context.Background(), groups, RunMeta{}, MemoryLoadCall{Query: "q"}, "ctx")
	if err == nil || err.Error() != "blocked" {
		t.Fatalf("err = %v", err)
	}
}

func TestRunBeforeMemoryStore_chainOrder(t *testing.T) {
	var order []string
	scope := interfaces.MemoryScope{AgentID: "a1"}
	groups := []HookGroup{
		{Name: "scrub", Hooks: AgentHooks{BeforeMemoryStore: []BeforeMemoryStoreHook{
			func(_ context.Context, in BeforeMemoryStoreHookInput) (BeforeMemoryStoreHookOutput, error) {
				order = append(order, in.RunMeta.HooksGroup)
				return BeforeMemoryStoreHookOutput{
					Record: interfaces.MemoryRecord{Text: "scrubbed"},
					ID:     "id-1",
				}, nil
			},
		}}},
		{Name: "audit", Hooks: AgentHooks{BeforeMemoryStore: []BeforeMemoryStoreHook{
			func(_ context.Context, in BeforeMemoryStoreHookInput) (BeforeMemoryStoreHookOutput, error) {
				order = append(order, in.RunMeta.HooksGroup+":"+in.Record.Text)
				return BeforeMemoryStoreHookOutput{
					Record: interfaces.MemoryRecord{Text: in.Record.Text + "-final"},
					ID:     "id-2",
				}, nil
			},
		}}},
	}
	got, err := RunBeforeMemoryStore(context.Background(), groups, RunMeta{}, MemoryStoreCall{
		Scope: scope, Record: interfaces.MemoryRecord{Text: "raw"}, ID: "orig",
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Record.Text != "scrubbed-final" || got.ID != "id-2" {
		t.Fatalf("call = %#v", got)
	}
	if got.Scope.AgentID != "a1" {
		t.Fatal("scope should be read-only")
	}
	if len(order) != 2 || order[0] != "scrub" || order[1] != "audit:scrubbed" {
		t.Fatalf("order = %v", order)
	}
}

func TestRunBeforeMemoryStore_errorAborts(t *testing.T) {
	groups := []HookGroup{{
		Name: "block",
		Hooks: AgentHooks{BeforeMemoryStore: []BeforeMemoryStoreHook{
			func(context.Context, BeforeMemoryStoreHookInput) (BeforeMemoryStoreHookOutput, error) {
				return BeforeMemoryStoreHookOutput{}, errors.New("blocked")
			},
		}},
	}}
	_, err := RunBeforeMemoryStore(context.Background(), groups, RunMeta{}, MemoryStoreCall{
		Record: interfaces.MemoryRecord{Text: "x"},
	})
	if err == nil || err.Error() != "blocked" {
		t.Fatalf("err = %v", err)
	}
}

func TestRunAfterMemoryStore_runsInOrder(t *testing.T) {
	var order []string
	groups := []HookGroup{
		{Name: "a", Hooks: AgentHooks{AfterMemoryStore: []AfterMemoryStoreHook{
			func(_ context.Context, in AfterMemoryStoreHookInput) (AfterMemoryStoreHookOutput, error) {
				order = append(order, "a:"+in.RunMeta.HooksGroup)
				return AfterMemoryStoreHookOutput{}, nil
			},
		}}},
		{Name: "b", Hooks: AgentHooks{AfterMemoryStore: []AfterMemoryStoreHook{
			func(_ context.Context, in AfterMemoryStoreHookInput) (AfterMemoryStoreHookOutput, error) {
				order = append(order, "b:"+in.ID)
				return AfterMemoryStoreHookOutput{}, nil
			},
		}}},
	}
	call := MemoryStoreCall{
		Scope:  interfaces.MemoryScope{UserID: "u1"},
		Record: interfaces.MemoryRecord{Text: "stored"},
		ID:     "mem-1",
	}
	if err := RunAfterMemoryStore(context.Background(), groups, RunMeta{}, call); err != nil {
		t.Fatal(err)
	}
	if len(order) != 2 || order[0] != "a:a" || order[1] != "b:mem-1" {
		t.Fatalf("order = %v", order)
	}
}

func TestRunAfterMemoryStore_errorAborts(t *testing.T) {
	groups := []HookGroup{{
		Name: "block",
		Hooks: AgentHooks{AfterMemoryStore: []AfterMemoryStoreHook{
			func(context.Context, AfterMemoryStoreHookInput) (AfterMemoryStoreHookOutput, error) {
				return AfterMemoryStoreHookOutput{}, errors.New("blocked")
			},
		}},
	}}
	err := RunAfterMemoryStore(context.Background(), groups, RunMeta{}, MemoryStoreCall{
		Record: interfaces.MemoryRecord{Text: "x"}, ID: "id",
	})
	if err == nil || err.Error() != "blocked" {
		t.Fatalf("err = %v", err)
	}
}
