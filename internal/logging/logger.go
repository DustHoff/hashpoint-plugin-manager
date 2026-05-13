// Package logging adapts sdk.HostAPI.Log into a level-method Logger
// surface so internal packages can take a narrow dependency. The host
// stamps the plugin name onto every line, so callers must not prepend
// "plugin-manager:" themselves.
package logging

import (
	"context"

	sdk "github.com/dusthoff/hashpoint/plugin/sdk"
)

// Logger emits structured log lines. Implementations forward to whatever
// transport the surrounding process exposes — for the plugin runtime
// that's sdk.HostAPI.Log; for tests, a buffer or Nop.
type Logger interface {
	Debug(ctx context.Context, msg string, fields map[string]string)
	Info(ctx context.Context, msg string, fields map[string]string)
	Warn(ctx context.Context, msg string, fields map[string]string)
	Error(ctx context.Context, msg string, fields map[string]string)
}

// FromHost wraps a sdk.HostAPI so subsystems can log without depending
// on the full HostAPI surface (which they don't need for any other call).
func FromHost(h sdk.HostAPI) Logger {
	return &hostLogger{h: h}
}

type hostLogger struct{ h sdk.HostAPI }

func (l *hostLogger) Debug(ctx context.Context, msg string, fields map[string]string) {
	_ = l.h.Log(ctx, "debug", msg, fields)
}
func (l *hostLogger) Info(ctx context.Context, msg string, fields map[string]string) {
	_ = l.h.Log(ctx, "info", msg, fields)
}
func (l *hostLogger) Warn(ctx context.Context, msg string, fields map[string]string) {
	_ = l.h.Log(ctx, "warn", msg, fields)
}
func (l *hostLogger) Error(ctx context.Context, msg string, fields map[string]string) {
	_ = l.h.Log(ctx, "error", msg, fields)
}

// Nop discards every log line. Useful in tests where the noise is irrelevant.
type Nop struct{}

func (Nop) Debug(context.Context, string, map[string]string) {}
func (Nop) Info(context.Context, string, map[string]string)  {}
func (Nop) Warn(context.Context, string, map[string]string)  {}
func (Nop) Error(context.Context, string, map[string]string) {}
