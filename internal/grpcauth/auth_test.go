package grpcauth

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"os"
	"path/filepath"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

func testCfg() Config {
	return Config{Store: NewCredentialStore([]Credential{
		{ClientID: "alice", TokenHash: HashToken("secret"), Tenants: []string{"t1"}},
		{ClientID: "scoped", TokenHash: HashToken("scopedtok"), Tenants: []string{"*"}, Scopes: []string{"s1"}},
		{ClientID: "svc", MTLSIdentity: "svc.example", Tenants: []string{"*"}},
	})}
}

func mdCtx(kv ...string) context.Context {
	return metadata.NewIncomingContext(context.Background(), metadata.Pairs(kv...))
}

type fakeStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (f fakeStream) Context() context.Context { return f.ctx }

func runUnary(ctx context.Context) (string, error) {
	u, _ := NewInterceptors(testCfg())
	var got string
	_, err := u(ctx, nil, &grpc.UnaryServerInfo{FullMethod: "/x/Y"}, func(c context.Context, _ any) (any, error) {
		got = ClientID(c)
		return nil, nil
	})
	return got, err
}

func runStream(ctx context.Context) (string, error) {
	_, s := NewInterceptors(testCfg())
	var got string
	err := s(nil, fakeStream{ctx: ctx}, &grpc.StreamServerInfo{FullMethod: "/x/Y"}, func(_ any, ss grpc.ServerStream) error {
		got = ClientID(ss.Context())
		return nil
	})
	return got, err
}

func TestScopesAndNoTenant(t *testing.T) {
	for name, run := range map[string]func(context.Context) (string, error){"unary": runUnary, "stream": runStream} {
		t.Run(name, func(t *testing.T) {
			id, err := run(mdCtx("authorization", "Bearer scopedtok", scopeMetadataKey, "s1"))
			if err != nil || id != "scoped" {
				t.Fatalf("allowed scope: id=%q err=%v", id, err)
			}
			if _, err := run(mdCtx("authorization", "Bearer scopedtok", scopeMetadataKey, "s2")); status.Code(err) != codes.PermissionDenied {
				t.Fatalf("denied scope: %v", err)
			}
			// Empty scope list: unrestricted.
			if id, err := run(mdCtx("authorization", "Bearer secret", scopeMetadataKey, "any")); err != nil || id != "alice" {
				t.Fatalf("unrestricted scope: id=%q err=%v", id, err)
			}
			// No tenant header passes through.
			if id, err := run(mdCtx("authorization", "Bearer secret")); err != nil || id != "alice" {
				t.Fatalf("no tenant: id=%q err=%v", id, err)
			}
		})
	}
}

func TestLoadCredentialStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "creds.json")
	data := `[{"client_id":"bob","token_hash":"` + HashToken("bobtok") + `","tenants":["t9"]}]`
	if err := os.WriteFile(path, []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := LoadCredentialStore(path)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := NewInterceptors(Config{Store: store})
	var got string
	_, err = u(mdCtx("authorization", "Bearer bobtok", tenantMetadataKey, "t9"), nil, &grpc.UnaryServerInfo{FullMethod: "/x/Y"},
		func(c context.Context, _ any) (any, error) { got = ClientID(c); return nil, nil })
	if err != nil || got != "bob" {
		t.Fatalf("id=%q err=%v", got, err)
	}
	if _, err := LoadCredentialStore(filepath.Join(t.TempDir(), "missing.json")); err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestAuthCases(t *testing.T) {
	for name, run := range map[string]func(context.Context) (string, error){"unary": runUnary, "stream": runStream} {
		t.Run(name, func(t *testing.T) {
			if _, err := run(context.Background()); status.Code(err) != codes.Unauthenticated {
				t.Fatalf("no token: %v", err)
			}
			if _, err := run(mdCtx("authorization", "Bearer wrong")); status.Code(err) != codes.Unauthenticated {
				t.Fatalf("bad token: %v", err)
			}
			if _, err := run(mdCtx("authorization", "Bearer secret", tenantMetadataKey, "t2")); status.Code(err) != codes.PermissionDenied {
				t.Fatalf("other tenant: %v", err)
			}
			id, err := run(mdCtx("authorization", "Bearer secret", tenantMetadataKey, "t1"))
			if err != nil || id != "alice" {
				t.Fatalf("valid: id=%q err=%v", id, err)
			}
			cert := &x509.Certificate{Subject: pkix.Name{CommonName: "svc.example"}}
			pctx := peer.NewContext(context.Background(), &peer.Peer{AuthInfo: credentials.TLSInfo{
				State: tls.ConnectionState{PeerCertificates: []*x509.Certificate{cert}},
			}})
			id, err = run(pctx)
			if err != nil || id != "svc" {
				t.Fatalf("mtls: id=%q err=%v", id, err)
			}
		})
	}
}
