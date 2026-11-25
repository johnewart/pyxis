package server

import "context"

type toolContextKey struct{}

// resolveToolContext returns the tool_context for a turn: the request's map
// when non-empty (it overrides the session's wholesale), otherwise the
// session's. The result is always a fresh copy so request or session state is
// never aliased.
func resolveToolContext(req, sess map[string]string) map[string]string {
	src := sess
	if len(req) > 0 {
		src = req
	}
	return cloneToolContext(src)
}

func cloneToolContext(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// withToolContext stores the turn's resolved tool_context on ctx for
// executeToolForStream.
func withToolContext(ctx context.Context, m map[string]string) context.Context {
	if len(m) == 0 {
		return ctx
	}
	return context.WithValue(ctx, toolContextKey{}, m)
}

// toolContextFrom returns a copy of the turn's resolved tool_context, or nil.
func toolContextFrom(ctx context.Context) map[string]string {
	m, _ := ctx.Value(toolContextKey{}).(map[string]string)
	return cloneToolContext(m)
}
