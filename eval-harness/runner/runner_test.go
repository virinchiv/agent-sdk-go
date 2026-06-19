package main

import (
	"context"
	"os"
	"testing"

	"github.com/agenticenv/agent-sdk-go/eval-harness/runner/setup"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig_Defaults(t *testing.T) {
	cfg, err := setup.LoadConfig("config.yaml")
	require.NoError(t, err)
	require.Equal(t, "run eval check", cfg.UserPrompt)
	require.Equal(t, "local", cfg.Runtime)
	require.Equal(t, 3, cfg.Agent.ToolCount)
}

func TestRun_FromFileConfig(t *testing.T) {
	fileCfg, err := setup.LoadConfig("config.yaml")
	require.NoError(t, err)

	result, err := Run(context.Background(), fileCfg.Config())
	require.NoError(t, err)
	require.NotEmpty(t, result.Content)
	require.Equal(t, int64(3), result.Telemetry.Tools.TotalCalls)
}

func TestRun_LocalRuntime(t *testing.T) {
	result, err := Run(context.Background(), setup.Config{
		UserPrompt: "run eval check",
		Runtime:    setup.RuntimeLocal,
		ToolCount:  2,
	})
	require.NoError(t, err)
	require.NotNil(t, result)
	require.NotEmpty(t, result.Content)
	require.NotNil(t, result.Telemetry)
	require.Equal(t, int64(2), result.Telemetry.Tools.TotalCalls)
}

func TestRun_TemporalRuntime(t *testing.T) {
	if os.Getenv("EVAL_HARNESS_TEMPORAL") != "true" {
		t.Skip("set EVAL_HARNESS_TEMPORAL=true with Temporal running on localhost:7233")
	}

	result, err := Run(context.Background(), setup.Config{
		UserPrompt: "run eval check",
		Runtime:    setup.RuntimeTemporal,
		ToolCount:  2,
	})
	require.NoError(t, err)
	require.NotEmpty(t, result.Content)
	require.Equal(t, int64(2), result.Telemetry.Tools.TotalCalls)
}

func TestRun_RequiresUserPrompt(t *testing.T) {
	_, err := Run(context.Background(), setup.Config{})
	require.Error(t, err)
}

func TestRun_InvalidRuntime(t *testing.T) {
	_, err := Run(context.Background(), setup.Config{
		UserPrompt: "hello",
		Runtime:    "invalid",
	})
	require.Error(t, err)
}
