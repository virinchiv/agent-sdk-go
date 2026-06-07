package main

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

type externalWorkerManager struct {
	cmds []*exec.Cmd
}

func startExternalWorkers(ctx context.Context, cfgPath, repoRoot string, count int) (*externalWorkerManager, error) {
	if count <= 0 {
		return &externalWorkerManager{}, nil
	}

	absConfig, err := filepath.Abs(cfgPath)
	if err != nil {
		return nil, fmt.Errorf("resolve config path: %w", err)
	}

	mgr := &externalWorkerManager{cmds: make([]*exec.Cmd, 0, count)}
	for i := 1; i <= count; i++ {
		cmd := exec.CommandContext(ctx, "go", "run", "./benchmarks/worker",
			"-config", absConfig,
			"-worker-id", fmt.Sprintf("%d", i),
		)
		cmd.Dir = repoRoot
		cmd.Env = os.Environ()
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

		if err := cmd.Start(); err != nil {
			_ = mgr.stop()
			return nil, fmt.Errorf("start worker %d: %w", i, err)
		}
		mgr.cmds = append(mgr.cmds, cmd)
	}

	time.Sleep(2 * time.Second)
	return mgr, nil
}

func (m *externalWorkerManager) stop() error {
	if m == nil {
		return nil
	}
	var firstErr error
	for _, cmd := range m.cmds {
		if cmd == nil || cmd.Process == nil {
			continue
		}
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM)
	}
	deadline := time.Now().Add(10 * time.Second)
	for _, cmd := range m.cmds {
		if cmd == nil || cmd.Process == nil {
			continue
		}
		done := make(chan error, 1)
		go func(c *exec.Cmd) { done <- c.Wait() }(cmd)
		select {
		case err := <-done:
			if err != nil && firstErr == nil {
				firstErr = err
			}
		case <-time.After(time.Until(deadline)):
			_ = cmd.Process.Kill()
		}
	}
	return firstErr
}
