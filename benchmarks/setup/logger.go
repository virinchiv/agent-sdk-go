package setup

import (
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"time"

	"github.com/agenticenv/agent-sdk-go/pkg/logger"
)

func SetupAgentLogger(cfg *Config, repoRoot string) (logger.Logger, func(), error) {
	return setupFileLogger(cfg, repoRoot, "agent")
}

func SetupWorkerLogger(cfg *Config, repoRoot string, workerID int) (logger.Logger, func(), error) {
	return setupFileLogger(cfg, repoRoot, fmt.Sprintf("worker_%d", workerID))
}

func setupFileLogger(cfg *Config, repoRoot, prefix string) (logger.Logger, func(), error) {
	if cfg == nil || !cfg.Logger.Enabled {
		return logger.NoopLogger(), func() {}, nil
	}

	dir := cfg.LogDir(repoRoot)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, nil, fmt.Errorf("create log dir: %w", err)
	}

	filename := fmt.Sprintf("%s_%s.log", prefix, time.Now().Format("2006-01-02_15-04-05"))
	path := filepath.Join(dir, filename)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, nil, fmt.Errorf("open log file: %w", err)
	}

	lgr := logger.NewWriterLogger(f, cfg.Logger.Level, "json", true)
	cleanup := func() { _ = f.Close() }
	return lgr, cleanup, nil
}

func TreeRNG() *rand.Rand {
	return rand.New(rand.NewSource(BenchmarkTreeSeed))
}

func RunRNG() *rand.Rand {
	return rand.New(rand.NewSource(time.Now().UnixNano()))
}
