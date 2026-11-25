// Package tenantscope exports the request Scope (tenant + optional scope) and its
// resolvers so modules outside this repo's internal tree can resolve the caller's
// scope. It replaces workspacescope.
package tenantscope

import (
	"context"
	"net/http"
	"net/url"

	"github.com/johnewart/pyxis/internal/serverutil"
)

// Scope identifies the tenant and optional scope a request acts for.
type Scope = serverutil.Scope

// FromContext resolves the Scope from incoming gRPC metadata.
func FromContext(ctx context.Context) (Scope, error) { return serverutil.ScopeFromContext(ctx) }

// FromHTTPHeader resolves the Scope from HTTP request headers.
func FromHTTPHeader(h http.Header) (Scope, error) { return serverutil.ScopeFromHTTPHeader(h) }

// FromQuery resolves the Scope from URL query parameters.
func FromQuery(q url.Values) (Scope, error) { return serverutil.ScopeFromQuery(q) }
