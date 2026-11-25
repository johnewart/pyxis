package server

import (
	"context"
	"strings"

	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/sessionmanager"
	"github.com/johnewart/pyxis/llmdlog"
)

// scope resolves the tenant/scope for the current RPC from incoming gRPC metadata.
// A tenant is always required; there is no bypass for servers without a session manager.
func (s *LlmServer) scope(ctx context.Context) (serverutil.Scope, error) {
	return serverutil.ScopeFromContext(ctx)
}

// scopeOrZero resolves the RPC scope, logging and returning the zero Scope on
// failure. A zero Scope matches no runtime, so lookups with it fail closed.
func (s *LlmServer) scopeOrZero(ctx context.Context) serverutil.Scope {
	sc, err := s.scope(ctx)
	if err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn, "resolve scope: %v", err)
		return serverutil.Scope{}
	}
	return sc
}

// runtimeFor returns the session runtime for id under the RPC's scope.
func (s *LlmServer) runtimeFor(ctx context.Context, id string) (*sessionmanager.SessionRuntime, bool) {
	if s.SessionRuntimes == nil {
		return nil, false
	}
	sc, err := s.scope(ctx)
	if err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn, "runtime lookup session=%q: resolve scope: %v", strings.TrimSpace(id), err)
		return nil, false
	}
	return s.SessionRuntimes.Get(sc, strings.TrimSpace(id))
}
