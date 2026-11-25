package serverutil

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// Scope identifies who a request acts for: a tenant and, optionally, a scope
// within that tenant. An empty ScopeID means the request is tenant-wide.
type Scope struct {
	TenantID string
	ScopeID  string
	// PrincipalID optionally identifies the acting principal; empty when not sent.
	PrincipalID string
}

// Validate returns a codes.Unauthenticated status error when TenantID is empty.
func (s Scope) Validate() error {
	if strings.TrimSpace(s.TenantID) == "" {
		return status.Error(codes.Unauthenticated, "set grpc metadata "+scopeTenantMetadataKey)
	}
	return nil
}

// IsTenantWide reports whether the scope covers the whole tenant.
func (s Scope) IsTenantWide() bool { return s.ScopeID == "" }

// String renders the scope for logs.
func (s Scope) String() string {
	out := "tenant=" + s.TenantID
	if !s.IsTenantWide() {
		out += " scope=" + s.ScopeID
	}
	if s.PrincipalID != "" {
		out += " principal=" + s.PrincipalID
	}
	return out
}

// OutgoingContext returns ctx with the scope appended to outgoing gRPC metadata.
// The scope key is sent only when ScopeID is non-empty.
func (s Scope) OutgoingContext(ctx context.Context) context.Context {
	kv := []string{scopeTenantMetadataKey, s.TenantID}
	if s.ScopeID != "" {
		kv = append(kv, scopeScopeMetadataKey, s.ScopeID)
	}
	if s.PrincipalID != "" {
		kv = append(kv, scopePrincipalMetadataKey, s.PrincipalID)
	}
	return metadata.AppendToOutgoingContext(ctx, kv...)
}

// IncomingContext returns ctx with the scope merged into incoming gRPC metadata
// (useful for in-process calls and tests).
func (s Scope) IncomingContext(ctx context.Context) context.Context {
	md, _ := metadata.FromIncomingContext(ctx)
	md = md.Copy()
	if md == nil {
		md = metadata.MD{}
	}
	md.Set(scopeTenantMetadataKey, s.TenantID)
	if s.ScopeID != "" {
		md.Set(scopeScopeMetadataKey, s.ScopeID)
	} else {
		md.Delete(scopeScopeMetadataKey)
	}
	if s.PrincipalID != "" {
		md.Set(scopePrincipalMetadataKey, s.PrincipalID)
	} else {
		md.Delete(scopePrincipalMetadataKey)
	}
	return metadata.NewIncomingContext(ctx, md)
}

// ApplyHTTPHeader sets the scope on an outgoing HTTP header.
func (s Scope) ApplyHTTPHeader(h http.Header) {
	h.Set(scopeTenantHTTPHeader, s.TenantID)
	if s.ScopeID != "" {
		h.Set(scopeScopeHTTPHeader, s.ScopeID)
	} else {
		h.Del(scopeScopeHTTPHeader)
	}
	if s.PrincipalID != "" {
		h.Set(scopePrincipalHTTPHeader, s.PrincipalID)
	} else {
		h.Del(scopePrincipalHTTPHeader)
	}
}

// ApplyQuery sets the scope on outgoing URL query parameters.
func (s Scope) ApplyQuery(q url.Values) {
	q.Set(scopeTenantQueryParam, s.TenantID)
	if s.ScopeID != "" {
		q.Set(scopeScopeQueryParam, s.ScopeID)
	} else {
		q.Del(scopeScopeQueryParam)
	}
	if s.PrincipalID != "" {
		q.Set(scopePrincipalQueryParam, s.PrincipalID)
	} else {
		q.Del(scopePrincipalQueryParam)
	}
}

func resolveScope(tenant, scope, principal string) (Scope, error) {
	s := Scope{TenantID: strings.TrimSpace(tenant), ScopeID: strings.TrimSpace(scope), PrincipalID: strings.TrimSpace(principal)}
	if err := s.Validate(); err != nil {
		return Scope{}, err
	}
	return s, nil
}

// ScopeFromContext resolves the Scope from incoming gRPC metadata.
func ScopeFromContext(ctx context.Context) (Scope, error) {
	var tenant, scope, principal string
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if v := md.Get(scopeTenantMetadataKey); len(v) > 0 {
			tenant = v[0]
		}
		if v := md.Get(scopeScopeMetadataKey); len(v) > 0 {
			scope = v[0]
		}
		if v := md.Get(scopePrincipalMetadataKey); len(v) > 0 {
			principal = v[0]
		}
	}
	return resolveScope(tenant, scope, principal)
}

// ScopeFromHTTPHeader resolves the Scope from HTTP request headers.
func ScopeFromHTTPHeader(h http.Header) (Scope, error) {
	return resolveScope(h.Get(scopeTenantHTTPHeader), h.Get(scopeScopeHTTPHeader), h.Get(scopePrincipalHTTPHeader))
}

// ScopeFromQuery resolves the Scope from URL query parameters.
func ScopeFromQuery(q url.Values) (Scope, error) {
	return resolveScope(q.Get(scopeTenantQueryParam), q.Get(scopeScopeQueryParam), q.Get(scopePrincipalQueryParam))
}
