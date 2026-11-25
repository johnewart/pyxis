// Package grpcauth provides gRPC interceptors that authenticate callers by
// bearer token or mTLS peer identity and enforce per-client tenant allow-lists.
package grpcauth

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/grpc/status"
)

const (
	logModule = "grpcauth"

	// Same string as the private constant in internal/serverutil/scope_wire.go.
	tenantMetadataKey = "llmd-tenant-id"
	scopeMetadataKey  = "llmd-scope-id"
)

// Credential is one client's stored identity. TokenHash is the hex SHA-256 of
// the bearer token; the token itself is never stored. Tenants is the allow-list
// of tenant ids the client may claim ("*" allows any tenant). MTLSIdentity, if
// set, is a certificate common name or SAN that also identifies this client.
type Credential struct {
	ClientID     string   `json:"client_id"`
	TokenHash    string   `json:"token_hash,omitempty"`
	MTLSIdentity string   `json:"mtls_identity,omitempty"`
	Tenants      []string `json:"tenants,omitempty"`
	// Scopes is the optional allow-list of scope ids. Empty means scopes are
	// not restricted; "*" allows any scope.
	Scopes []string `json:"scopes,omitempty"`
}

// CredentialStore looks up client credentials. A database-backed store can
// implement it in place of MemoryStore.
type CredentialStore interface {
	LookupToken(token string) (Credential, bool)
	LookupMTLS(identity string) (Credential, bool)
}

// MemoryStore is an in-memory CredentialStore, typically loaded from JSON.
type MemoryStore struct {
	creds []Credential
}

var _ CredentialStore = (*MemoryStore)(nil)

// NewCredentialStore builds an in-memory store from creds.
func NewCredentialStore(creds []Credential) *MemoryStore {
	return &MemoryStore{creds: append([]Credential(nil), creds...)}
}

// LoadCredentialStore reads a JSON array of Credential from path.
func LoadCredentialStore(path string) (*MemoryStore, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		llmdlog.Stdout(logModule, llmdlog.LevelWarn, "read credential file: path=%q err=%v", path, err)
		return nil, fmt.Errorf("grpcauth: read %s: %w", path, err)
	}
	var creds []Credential
	if err := json.Unmarshal(data, &creds); err != nil {
		llmdlog.Stdout(logModule, llmdlog.LevelWarn, "parse credential file: path=%q err=%v", path, err)
		return nil, fmt.Errorf("grpcauth: parse %s: %w", path, err)
	}
	return NewCredentialStore(creds), nil
}

// HashToken returns the hex SHA-256 of token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// LookupToken returns the credential whose hash matches token. Every stored
// hash is compared (no early exit) with subtle.ConstantTimeCompare.
func (s *MemoryStore) LookupToken(token string) (Credential, bool) {
	if s == nil || token == "" {
		return Credential{}, false
	}
	h := []byte(HashToken(token))
	var found Credential
	ok := false
	for _, c := range s.creds {
		if c.TokenHash == "" {
			continue
		}
		if subtle.ConstantTimeCompare(h, []byte(strings.ToLower(c.TokenHash))) == 1 {
			found, ok = c, true
		}
	}
	return found, ok
}

// LookupMTLS returns the credential whose MTLSIdentity equals identity.
func (s *MemoryStore) LookupMTLS(identity string) (Credential, bool) {
	if s == nil || identity == "" {
		return Credential{}, false
	}
	for _, c := range s.creds {
		if c.MTLSIdentity != "" && c.MTLSIdentity == identity {
			return c, true
		}
	}
	return Credential{}, false
}

// Config configures NewInterceptors.
type Config struct {
	Store CredentialStore
}

type ctxKey struct{}

// ClientID returns the authenticated client id stored by the interceptors.
func ClientID(ctx context.Context) string {
	v, _ := ctx.Value(ctxKey{}).(string)
	return v
}

// WithClientID returns ctx carrying the given authenticated client id.
func WithClientID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, ctxKey{}, id)
}

// authenticate resolves the caller and checks any claimed tenant.
func authenticate(ctx context.Context, cfg Config, method string) (string, error) {
	var cred Credential
	ok := false
	if cfg.Store == nil {
		llmdlog.Stdout(logModule, llmdlog.LevelWarn, "no credential store configured: method=%q", method)
		return "", status.Error(codes.Unauthenticated, "missing or invalid credentials")
	}

	if md, hasMD := metadata.FromIncomingContext(ctx); hasMD {
		for _, v := range md.Get("authorization") {
			if len(v) > 7 && strings.EqualFold(v[:7], "bearer ") {
				if cred, ok = cfg.Store.LookupToken(strings.TrimSpace(v[7:])); ok {
					break
				}
			}
		}
	}
	if !ok {
		if p, hasPeer := peer.FromContext(ctx); hasPeer {
			if ti, isTLS := p.AuthInfo.(credentials.TLSInfo); isTLS {
				cred, ok = matchPeerCert(cfg.Store, ti.State)
			}
		}
	}
	if !ok {
		llmdlog.Stdout(logModule, llmdlog.LevelWarn, "unauthenticated: method=%q", method)
		return "", status.Error(codes.Unauthenticated, "missing or invalid credentials")
	}

	if md, hasMD := metadata.FromIncomingContext(ctx); hasMD {
		for _, tenant := range md.Get(tenantMetadataKey) {
			if !tenantAllowed(cred.Tenants, tenant) {
				llmdlog.Stdout(logModule, llmdlog.LevelWarn,
					"permission denied: method=%q client=%q tenant=%q", method, cred.ClientID, tenant)
				return "", status.Error(codes.PermissionDenied, "tenant not allowed for client")
			}
		}
		if len(cred.Scopes) > 0 {
			for _, scope := range md.Get(scopeMetadataKey) {
				if !tenantAllowed(cred.Scopes, scope) {
					llmdlog.Stdout(logModule, llmdlog.LevelWarn,
						"permission denied: method=%q client=%q scope=%q", method, cred.ClientID, scope)
					return "", status.Error(codes.PermissionDenied, "scope not allowed for client")
				}
			}
		}
	}
	return cred.ClientID, nil
}

func matchPeerCert(store CredentialStore, st tls.ConnectionState) (Credential, bool) {
	if len(st.PeerCertificates) == 0 {
		return Credential{}, false
	}
	cert := st.PeerCertificates[0]
	if c, ok := store.LookupMTLS(cert.Subject.CommonName); ok {
		return c, true
	}
	for _, n := range cert.DNSNames {
		if c, ok := store.LookupMTLS(n); ok {
			return c, true
		}
	}
	for _, e := range cert.EmailAddresses {
		if c, ok := store.LookupMTLS(e); ok {
			return c, true
		}
	}
	for _, u := range cert.URIs {
		if c, ok := store.LookupMTLS(u.String()); ok {
			return c, true
		}
	}
	return Credential{}, false
}

func tenantAllowed(allow []string, tenant string) bool {
	for _, a := range allow {
		if a == "*" || a == tenant {
			return true
		}
	}
	return false
}

type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context { return w.ctx }

// NewInterceptors returns unary and stream server interceptors that require
// authentication. Chain them before any handler-level interceptor.
func NewInterceptors(cfg Config) (grpc.UnaryServerInterceptor, grpc.StreamServerInterceptor) {
	unary := func(ctx context.Context, req any, info *grpc.UnaryServerInfo, h grpc.UnaryHandler) (any, error) {
		id, err := authenticate(ctx, cfg, info.FullMethod)
		if err != nil {
			return nil, err
		}
		return h(context.WithValue(ctx, ctxKey{}, id), req)
	}
	stream := func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, h grpc.StreamHandler) error {
		id, err := authenticate(ss.Context(), cfg, info.FullMethod)
		if err != nil {
			return err
		}
		return h(srv, &wrappedStream{ServerStream: ss, ctx: context.WithValue(ss.Context(), ctxKey{}, id)})
	}
	return unary, stream
}
