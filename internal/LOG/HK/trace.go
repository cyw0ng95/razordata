package hk

import (
	"log/slog"
)

type traceHook struct{}

var _ Hook = (*traceHook)(nil)

func (t traceHook) OnLog(level slog.Level, msg string, args []any) {}

func (t traceHook) Close() error {
	return nil
}
