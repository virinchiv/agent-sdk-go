package runtime

import (
	"testing"
	"time"
)

func TestSDKExecutionConfigDefaults(t *testing.T) {
	def := defaultExecutionConfigs()

	tests := []struct {
		name    string
		got     ExecutionConfig
		timeout time.Duration
		retries int
	}{
		{"LLM", def.LLM, 30 * time.Minute, 3},
		{"ToolAuth", def.ToolAuth, 30 * time.Minute, 1},
		{"ToolExecute", def.ToolExecute, 30 * time.Minute, 3},
		{"MCP", def.MCP, 30 * time.Minute, 3},
		{"A2A", def.A2A, 30 * time.Minute, 3},
		{"Retriever", def.Retriever, 5 * time.Minute, 3},
		{"Memory", def.Memory, 5 * time.Minute, 3},
		{"Conversation", def.Conversation, 30 * time.Second, 1},
		{"SubAgent", def.SubAgent, 0, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got.Timeout != tt.timeout {
				t.Fatalf("Timeout = %v, want %v", tt.got.Timeout, tt.timeout)
			}
			if tt.got.MaxAttempts != tt.retries {
				t.Fatalf("Retries = %d, want %d", tt.got.MaxAttempts, tt.retries)
			}
		})
	}
}

func TestResolveExecutionConfig_agentOverridesSDK(t *testing.T) {
	sdk := ExecutionConfig{Timeout: 30 * time.Minute, MaxAttempts: 3}
	agent := ExecutionConfig{Timeout: 10 * time.Minute, MaxAttempts: 5}

	got := resolveExecutionConfig(agent, sdk)
	if got.Timeout != 10*time.Minute {
		t.Fatalf("Timeout = %v, want 10m", got.Timeout)
	}
	if got.MaxAttempts != 5 {
		t.Fatalf("Retries = %d, want 5", got.MaxAttempts)
	}
}

func TestResolveExecutionConfig_partialAgentOverride(t *testing.T) {
	sdk := ExecutionConfig{Timeout: 30 * time.Minute, MaxAttempts: 3}

	got := resolveExecutionConfig(ExecutionConfig{Timeout: 15 * time.Minute}, sdk)
	if got.Timeout != 15*time.Minute {
		t.Fatalf("Timeout = %v, want 15m", got.Timeout)
	}
	if got.MaxAttempts != 3 {
		t.Fatalf("Retries = %d, want sdk default 3", got.MaxAttempts)
	}

	got = resolveExecutionConfig(ExecutionConfig{MaxAttempts: 2}, sdk)
	if got.Timeout != 30*time.Minute {
		t.Fatalf("Timeout = %v, want sdk default 30m", got.Timeout)
	}
	if got.MaxAttempts != 2 {
		t.Fatalf("Retries = %d, want 2", got.MaxAttempts)
	}
}

func TestResolveExecutionConfig_zeroAgentUsesSDK(t *testing.T) {
	sdk := ExecutionConfig{Timeout: 5 * time.Minute, MaxAttempts: 1}
	got := resolveExecutionConfig(ExecutionConfig{}, sdk)
	if got != sdk {
		t.Fatalf("got %+v, want sdk %+v", got, sdk)
	}
}

func TestResolveExecutionPolicies(t *testing.T) {
	sdk := defaultExecutionConfigs()
	agent := ExecutionConfigs{
		LLM:         ExecutionConfig{Timeout: 45 * time.Minute},
		ToolExecute: ExecutionConfig{MaxAttempts: 7},
		MCP:         ExecutionConfig{Timeout: 20 * time.Minute, MaxAttempts: 2},
	}

	got := ResolveExecutionPolicies(agent)

	if got.LLM.Timeout != 45*time.Minute || got.LLM.MaxAttempts != sdk.LLM.MaxAttempts {
		t.Fatalf("LLM = %+v, want 45m timeout and sdk retries", got.LLM)
	}
	if got.ToolExecute.Timeout != sdk.ToolExecute.Timeout || got.ToolExecute.MaxAttempts != 7 {
		t.Fatalf("ToolExecute = %+v", got.ToolExecute)
	}
	if got.MCP.Timeout != 20*time.Minute || got.MCP.MaxAttempts != 2 {
		t.Fatalf("MCP = %+v", got.MCP)
	}
	if got.Conversation != sdk.Conversation.ToPolicy() {
		t.Fatalf("Conversation = %+v, want sdk default %+v", got.Conversation, sdk.Conversation.ToPolicy())
	}
}

func TestDefaultRetryPolicy(t *testing.T) {
	got := DefaultRetryPolicy()
	if got.InitialInterval != time.Second {
		t.Fatalf("InitialInterval = %v, want 1s", got.InitialInterval)
	}
	if got.BackoffCoefficient != 2 {
		t.Fatalf("BackoffCoefficient = %v, want 2", got.BackoffCoefficient)
	}
	if got.MaximumInterval != 10*time.Minute {
		t.Fatalf("MaximumInterval = %v, want 10m", got.MaximumInterval)
	}
}

func TestExecutionConfigToPolicy(t *testing.T) {
	got := ExecutionConfig{Timeout: 5 * time.Minute, MaxAttempts: 3}.ToPolicy()
	if got.Timeout != 5*time.Minute {
		t.Fatalf("Timeout = %v, want 5m", got.Timeout)
	}
	if got.MaxAttempts != 3 {
		t.Fatalf("MaxAttempts = %d, want 3", got.MaxAttempts)
	}
	if got.Retry != DefaultRetryPolicy() {
		t.Fatalf("Retry = %+v, want default %+v", got.Retry, DefaultRetryPolicy())
	}
}
