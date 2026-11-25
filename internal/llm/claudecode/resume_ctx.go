package claudecodeclillm

import (
	"context"
	"strings"
)

type resumeSessionCtxKey struct{}

// WithResumeSessionID attaches the Claude Code session id for CLI --resume (from llmd provider sync state).
func WithResumeSessionID(ctx context.Context, sessionID string) context.Context {
	return context.WithValue(ctx, resumeSessionCtxKey{}, strings.TrimSpace(sessionID))
}

// ResumeSessionIDFromContext returns the value from [WithResumeSessionID], or "".
func ResumeSessionIDFromContext(ctx context.Context) string {
	v, _ := ctx.Value(resumeSessionCtxKey{}).(string)
	return strings.TrimSpace(v)
}

type backendSessionSinkCtxKey struct{}

// WithBackendSessionSink registers a callback invoked when the CLI JSON result includes session_id.
func WithBackendSessionSink(ctx context.Context, fn func(string)) context.Context {
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, backendSessionSinkCtxKey{}, fn)
}

func backendSessionSinkFromContext(ctx context.Context) func(string) {
	fn, _ := ctx.Value(backendSessionSinkCtxKey{}).(func(string))
	return fn
}

// BackendSessionSinkFromContext returns the callback from [WithBackendSessionSink], or nil.
func BackendSessionSinkFromContext(ctx context.Context) func(string) {
	return backendSessionSinkFromContext(ctx)
}
