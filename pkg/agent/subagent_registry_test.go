package agent

import "testing"

func TestSubAgentRegistry_RegisterGet(t *testing.T) {
	r := NewSubAgentRegistry()
	sub := &Agent{agentConfig: agentConfig{Name: "Math"}}
	if err := r.Register(sub); err != nil {
		t.Fatal(err)
	}

	got, err := r.Get("Math")
	if err != nil || got != sub {
		t.Fatalf("Get(Math) = %v, %v", got, err)
	}
	if len(r.List()) != 1 {
		t.Fatalf("List len = %d, want 1", len(r.List()))
	}
	if err := r.Unregister("Math"); err != nil {
		t.Fatal(err)
	}
}

func TestSubAgentRegistry_RegisterNilAndEmptyName(t *testing.T) {
	r := NewSubAgentRegistry()
	if err := r.Register(nil); err != ErrRegistryNilEntry {
		t.Errorf("Register(nil) err = %v", err)
	}
	if err := r.Register(&Agent{agentConfig: agentConfig{Name: "   "}}); err != ErrRegistryInvalidName {
		t.Errorf("empty name err = %v", err)
	}
	if len(r.List()) != 0 {
		t.Fatalf("List len = %d, want 0", len(r.List()))
	}
}

func TestNormalizeSubAgentRegistry_fromWithSubAgents(t *testing.T) {
	sub := &Agent{agentConfig: agentConfig{Name: "Helper"}}
	c := &agentConfig{subAgents: []*Agent{sub}, maxSubAgentDepth: 3}
	if err := c.buildSubAgentRegistry(); err != nil {
		t.Fatal(err)
	}
	if len(c.subAgents) != 0 {
		t.Fatal("WithSubAgents entries should be cleared after buildSubAgentRegistry")
	}
	if len(c.subAgentRegistry.List()) != 1 {
		t.Fatalf("registry len = %d, want 1", len(c.subAgentRegistry.List()))
	}
}
