// Package temporal contains helpers for integrating this SDK with go.temporal.io (client, worker,
// etc.). This file adapts pkg/logger.Logger to go.temporal.io/sdk/log.Logger for client.Options.
// It is internal to this module; callers who build their own Temporal client should wire
// sdk/log.Logger themselves (or copy this small bridge).
//
// If a file imports both this package and go.temporal.io/sdk/temporal, use an import alias on one
// of them (e.g. sdktrace "go.temporal.io/sdk/temporal") — import paths differ, but package names
// would both default to "temporal".
package temporal

import (
	"context"

	"github.com/agenticenv/agent-sdk-go/pkg/logger"
	tlog "go.temporal.io/sdk/log"
)

// NewLogAdapter returns a Temporal sdk/log.Logger that delegates to the SDK logger.Logger.
// Calls use context.Background() because Temporal's logger API is not context-aware.
func NewLogAdapter(l logger.Logger) tlog.Logger {
	if l == nil {
		l = logger.NoopLogger()
	}
	return logAdapter{l: l}
}

type logAdapter struct {
	l logger.Logger
}

func keyvalsToAny(keyvals []interface{}) []any {
	out := make([]any, 0, len(keyvals))
	out = append(out, keyvals...)
	return out
}

func (a logAdapter) Debug(msg string, keyvals ...interface{}) {
	a.l.Debug(context.Background(), msg, keyvalsToAny(keyvals)...)
}

func (a logAdapter) Info(msg string, keyvals ...interface{}) {
	a.l.Info(context.Background(), msg, keyvalsToAny(keyvals)...)
}

func (a logAdapter) Warn(msg string, keyvals ...interface{}) {
	a.l.Warn(context.Background(), msg, keyvalsToAny(keyvals)...)
}

func (a logAdapter) Error(msg string, keyvals ...interface{}) {
	a.l.Error(context.Background(), msg, keyvalsToAny(keyvals)...)
}

var _ tlog.Logger = logAdapter{}

// activityLogAdapter wraps a Temporal activity/workflow logger (tlog.Logger) so it satisfies
// pkg/logger.Logger. Activities obtain their logger via activity.GetLogger(ctx), which attaches
// workflow and activity metadata automatically; this bridge lets base-package methods reuse it.
type activityLogAdapter struct{ l tlog.Logger }

// newActivityLogger returns a logger.Logger backed by the Temporal tlog.Logger l.
func newActivityLogger(l tlog.Logger) logger.Logger {
	return activityLogAdapter{l: l}
}

func (a activityLogAdapter) Debug(ctx context.Context, msg string, args ...any) {
	a.l.Debug(msg, args...)
}
func (a activityLogAdapter) Info(ctx context.Context, msg string, args ...any) {
	a.l.Info(msg, args...)
}
func (a activityLogAdapter) Warn(ctx context.Context, msg string, args ...any) {
	a.l.Warn(msg, args...)
}
func (a activityLogAdapter) Error(ctx context.Context, msg string, args ...any) {
	a.l.Error(msg, args...)
}

var _ logger.Logger = activityLogAdapter{}
