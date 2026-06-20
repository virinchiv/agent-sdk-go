package main

import (
	"context"
	"os"
	"testing"

	"github.com/agenticenv/agent-sdk-go/eval-harness/runner/setup"
	"github.com/agenticenv/agent-sdk-go/pkg/memory"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig_Defaults(t *testing.T) {
	cfg, err := setup.LoadConfig("config.yaml")
	require.NoError(t, err)
	require.Equal(t, "run eval check", cfg.UserPrompt)
	require.Equal(t, "local", cfg.Runtime)
	require.Equal(t, 3, cfg.Agent.ToolCount)
	require.False(t, cfg.Memory.Enabled)
}

func TestLoadConfig_MemoryFields(t *testing.T) {
	cfg, err := setup.LoadConfig("config.yaml")
	require.NoError(t, err)
	require.False(t, cfg.Memory.Enabled)
	require.Equal(t, setup.MemoryScenarioStoreRecall, cfg.Memory.Scenario)
	require.NotEmpty(t, cfg.Memory.StorePrompt)
	require.NotEmpty(t, cfg.Memory.RecallPrompt)
}

func TestRun_FromFileConfig(t *testing.T) {
	fileCfg, err := setup.LoadConfig("config.yaml")
	require.NoError(t, err)

	outcome, err := Run(context.Background(), fileCfg.Config())
	require.NoError(t, err)
	require.NotNil(t, outcome.Result)
	require.NotEmpty(t, outcome.Result.Content)
	require.Equal(t, int64(3), outcome.Result.Telemetry.Tools.TotalCalls)
}

func TestRun_LocalRuntime(t *testing.T) {
	outcome, err := Run(context.Background(), setup.Config{
		UserPrompt: "run eval check",
		Runtime:    setup.RuntimeLocal,
		ToolCount:  2,
	})
	require.NoError(t, err)
	require.NotNil(t, outcome.Result)
	require.NotEmpty(t, outcome.Result.Content)
	require.NotNil(t, outcome.Result.Telemetry)
	require.Equal(t, int64(2), outcome.Result.Telemetry.Tools.TotalCalls)
}

func TestRun_MemoryStoreRecall_OnDemand(t *testing.T) {
	fileCfg, err := setup.LoadConfig("config.yaml")
	require.NoError(t, err)

	runCfg := fileCfg.Config()
	runCfg.Memory.Enabled = true
	runCfg.ToolCount = 0

	outcome, err := Run(context.Background(), runCfg)
	require.NoError(t, err)
	require.NotNil(t, outcome.MemoryScenario)
	require.GreaterOrEqual(t, outcome.MemoryScenario.Store.Telemetry.Storage.TotalMemoryStores, int64(1))
	require.GreaterOrEqual(t, outcome.MemoryScenario.Recall.Telemetry.Storage.TotalMemoryRecalls, int64(1))
}

func TestRun_MemoryStoreRecall_Always(t *testing.T) {
	fileCfg, err := setup.LoadConfig("config.yaml")
	require.NoError(t, err)

	runCfg := fileCfg.Config()
	runCfg.Memory.Enabled = true
	runCfg.Memory.StoreMode = memory.StoreModeAlways
	runCfg.ToolCount = 0

	outcome, err := Run(context.Background(), runCfg)
	require.NoError(t, err)
	require.NotNil(t, outcome.MemoryScenario)
	require.GreaterOrEqual(t, outcome.MemoryScenario.Store.Telemetry.Storage.TotalMemoryStores, int64(1))
	require.GreaterOrEqual(t, outcome.MemoryScenario.Recall.Telemetry.Storage.TotalMemoryRecalls, int64(1))
}

func TestRun_TemporalRuntime(t *testing.T) {
	if os.Getenv("EVAL_HARNESS_TEMPORAL") != "true" {
		t.Skip("set EVAL_HARNESS_TEMPORAL=true with Temporal running on localhost:7233")
	}

	outcome, err := Run(context.Background(), setup.Config{
		UserPrompt: "run eval check",
		Runtime:    setup.RuntimeTemporal,
		ToolCount:  2,
	})
	require.NoError(t, err)
	require.NotEmpty(t, outcome.Result.Content)
	require.Equal(t, int64(2), outcome.Result.Telemetry.Tools.TotalCalls)
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
