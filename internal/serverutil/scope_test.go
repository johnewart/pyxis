package serverutil

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

func TestScopeMissingTenant(t *testing.T) {
	for _, v := range []string{"", "   "} {
		ctx := context.Background()
		if v != "" {
			ctx = metadata.NewIncomingContext(ctx, metadata.Pairs(scopeTenantMetadataKey, v))
		}
		if _, err := ScopeFromContext(ctx); status.Code(err) != codes.Unauthenticated {
			t.Errorf("ScopeFromContext(%q): got %v", v, err)
		}
		h := http.Header{}
		if v != "" {
			h.Set(scopeTenantHTTPHeader, v)
		}
		if _, err := ScopeFromHTTPHeader(h); status.Code(err) != codes.Unauthenticated {
			t.Errorf("ScopeFromHTTPHeader(%q): got %v", v, err)
		}
		q := url.Values{}
		if v != "" {
			q.Set(scopeTenantQueryParam, v)
		}
		if _, err := ScopeFromQuery(q); status.Code(err) != codes.Unauthenticated {
			t.Errorf("ScopeFromQuery(%q): got %v", v, err)
		}
	}
}

func TestScopeTrimmed(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(scopeTenantMetadataKey, "  t1 "))
	if s, err := ScopeFromContext(ctx); err != nil || s.TenantID != "t1" {
		t.Errorf("ctx: %+v %v", s, err)
	}
	h := http.Header{}
	h.Set(scopeTenantHTTPHeader, " t1\t")
	if s, err := ScopeFromHTTPHeader(h); err != nil || s.TenantID != "t1" {
		t.Errorf("header: %+v %v", s, err)
	}
	q := url.Values{scopeTenantQueryParam: {" t1 "}}
	if s, err := ScopeFromQuery(q); err != nil || s.TenantID != "t1" {
		t.Errorf("query: %+v %v", s, err)
	}
}

func TestScopeRoundTrip(t *testing.T) {
	want := Scope{TenantID: "tenant-a"}

	out := want.OutgoingContext(context.Background())
	md, _ := metadata.FromOutgoingContext(out)
	got, err := ScopeFromContext(metadata.NewIncomingContext(context.Background(), md))
	if err != nil || got != want {
		t.Errorf("outgoing round trip: %+v %v", got, err)
	}

	got, err = ScopeFromContext(want.IncomingContext(context.Background()))
	if err != nil || got != want {
		t.Errorf("incoming round trip: %+v %v", got, err)
	}

	h := http.Header{}
	want.ApplyHTTPHeader(h)
	got, err = ScopeFromHTTPHeader(h)
	if err != nil || got != want {
		t.Errorf("header round trip: %+v %v", got, err)
	}
	if !got.IsTenantWide() {
		t.Error("expected tenant-wide scope")
	}
}

func TestScopeWithScopeIDRoundTrip(t *testing.T) {
	want := Scope{TenantID: "tenant-a", ScopeID: "s1"}

	out := want.OutgoingContext(context.Background())
	md, _ := metadata.FromOutgoingContext(out)
	if got, err := ScopeFromContext(metadata.NewIncomingContext(context.Background(), md)); err != nil || got != want {
		t.Errorf("grpc round trip: %+v %v", got, err)
	}
	if got, err := ScopeFromContext(want.IncomingContext(context.Background())); err != nil || got != want {
		t.Errorf("incoming round trip: %+v %v", got, err)
	}
	h := http.Header{}
	want.ApplyHTTPHeader(h)
	if got, err := ScopeFromHTTPHeader(h); err != nil || got != want {
		t.Errorf("header round trip: %+v %v", got, err)
	}
	q := url.Values{}
	want.ApplyQuery(q)
	if got, err := ScopeFromQuery(q); err != nil || got != want {
		t.Errorf("query round trip: %+v %v", got, err)
	}

	trimmed := metadata.NewIncomingContext(context.Background(), metadata.Pairs(scopeTenantMetadataKey, "t", scopeScopeMetadataKey, " s1 "))
	if got, err := ScopeFromContext(trimmed); err != nil || got.ScopeID != "s1" {
		t.Errorf("trimmed scope: %+v %v", got, err)
	}
}

func TestScopeEmptyScopeSendsNoKey(t *testing.T) {
	s := Scope{TenantID: "t"}
	md, _ := metadata.FromOutgoingContext(s.OutgoingContext(context.Background()))
	if _, ok := md[scopeScopeMetadataKey]; ok {
		t.Errorf("outgoing md has scope key: %v", md)
	}
	in, _ := metadata.FromIncomingContext(s.IncomingContext(context.Background()))
	if _, ok := in[scopeScopeMetadataKey]; ok {
		t.Errorf("incoming md has scope key: %v", in)
	}
	h := http.Header{}
	s.ApplyHTTPHeader(h)
	if _, ok := h[http.CanonicalHeaderKey(scopeScopeHTTPHeader)]; ok {
		t.Errorf("header has scope key: %v", h)
	}
	q := url.Values{}
	s.ApplyQuery(q)
	if _, ok := q[scopeScopeQueryParam]; ok {
		t.Errorf("query has scope key: %v", q)
	}
}

func TestScopePrincipalRead(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(scopeTenantMetadataKey, "t", scopePrincipalMetadataKey, " p1 "))
	if s, err := ScopeFromContext(ctx); err != nil || s.PrincipalID != "p1" {
		t.Errorf("ctx: %+v %v", s, err)
	}
	h := http.Header{}
	h.Set(scopeTenantHTTPHeader, "t")
	h.Set(scopePrincipalHTTPHeader, " p1 ")
	if s, err := ScopeFromHTTPHeader(h); err != nil || s.PrincipalID != "p1" {
		t.Errorf("header: %+v %v", s, err)
	}
	q := url.Values{scopeTenantQueryParam: {"t"}, scopePrincipalQueryParam: {" p1 "}}
	if s, err := ScopeFromQuery(q); err != nil || s.PrincipalID != "p1" {
		t.Errorf("query: %+v %v", s, err)
	}
}

func TestScopePrincipalEmptyWhenNotSent(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs(scopeTenantMetadataKey, "t"))
	if s, err := ScopeFromContext(ctx); err != nil || s.PrincipalID != "" {
		t.Errorf("ctx: %+v %v", s, err)
	}
	s := Scope{TenantID: "t"}
	md, _ := metadata.FromOutgoingContext(s.OutgoingContext(context.Background()))
	if _, ok := md[scopePrincipalMetadataKey]; ok {
		t.Errorf("outgoing md has principal key: %v", md)
	}
	h := http.Header{}
	s.ApplyHTTPHeader(h)
	if _, ok := h[http.CanonicalHeaderKey(scopePrincipalHTTPHeader)]; ok {
		t.Errorf("header has principal: %v", h)
	}
	q := url.Values{}
	s.ApplyQuery(q)
	if _, ok := q[scopePrincipalQueryParam]; ok {
		t.Errorf("query has principal: %v", q)
	}
	if got := s.String(); got != "tenant=t" {
		t.Errorf("String: %q", got)
	}
}

func TestScopePrincipalRoundTrip(t *testing.T) {
	want := Scope{TenantID: "t", ScopeID: "s", PrincipalID: "p"}
	md, _ := metadata.FromOutgoingContext(want.OutgoingContext(context.Background()))
	if got, err := ScopeFromContext(metadata.NewIncomingContext(context.Background(), md)); err != nil || got != want {
		t.Errorf("outgoing: %+v %v", got, err)
	}
	if got, err := ScopeFromContext(want.IncomingContext(context.Background())); err != nil || got != want {
		t.Errorf("incoming: %+v %v", got, err)
	}
	// Incoming without principal must delete a previously set key.
	prev := want.IncomingContext(context.Background())
	if got, err := ScopeFromContext(Scope{TenantID: "t"}.IncomingContext(prev)); err != nil || got.PrincipalID != "" {
		t.Errorf("incoming delete: %+v %v", got, err)
	}
	h := http.Header{}
	want.ApplyHTTPHeader(h)
	if got, err := ScopeFromHTTPHeader(h); err != nil || got != want {
		t.Errorf("header: %+v %v", got, err)
	}
	q := url.Values{}
	want.ApplyQuery(q)
	if got, err := ScopeFromQuery(q); err != nil || got != want {
		t.Errorf("query: %+v %v", got, err)
	}
	if got := want.String(); got != "tenant=t scope=s principal=p" {
		t.Errorf("String: %q", got)
	}
}

func TestScopeOldKeyRejected(t *testing.T) {
	ctx := metadata.NewIncomingContext(context.Background(), metadata.Pairs("llmd-workspace-id", "t1"))
	if _, err := ScopeFromContext(ctx); status.Code(err) != codes.Unauthenticated {
		t.Errorf("old key: got %v, want Unauthenticated", err)
	}
}
