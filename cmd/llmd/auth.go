package main

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"strings"

	"github.com/johnewart/pyxis/internal/grpcauth"
	"github.com/johnewart/pyxis/llmdhost"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
)

// validateListenAuth refuses to serve gRPC on a non-loopback address unless
// authentication is configured.
func validateListenAuth(addr string, authEnabled bool) error {
	if authEnabled || isLoopbackAddr(addr) {
		return nil
	}
	return fmt.Errorf("LLMD_GRPC_ADDR=%q is not loopback: authentication is required; "+
		"set LLMD_AUTH_CREDENTIALS_FILE (optionally with LLMD_TLS_CERT_FILE, LLMD_TLS_KEY_FILE, "+
		"LLMD_TLS_CLIENT_CA_FILE) or bind 127.0.0.1", addr)
}

// applyAuthFromEnv installs auth interceptors and TLS credentials on cfg when
// LLMD_AUTH_CREDENTIALS_FILE is set. With it unset, cfg is unchanged. It
// reports whether auth was configured.
func applyAuthFromEnv(cfg *llmdhost.Config) (bool, error) {
	credFile := strings.TrimSpace(os.Getenv("LLMD_AUTH_CREDENTIALS_FILE"))
	if credFile == "" {
		return false, nil
	}
	store, err := grpcauth.LoadCredentialStore(credFile)
	if err != nil {
		return false, err
	}
	unary, stream := grpcauth.NewInterceptors(grpcauth.Config{Store: store})
	cfg.UnaryInterceptors = append(cfg.UnaryInterceptors, unary)
	cfg.StreamInterceptors = append(cfg.StreamInterceptors, stream)

	certFile := strings.TrimSpace(os.Getenv("LLMD_TLS_CERT_FILE"))
	keyFile := strings.TrimSpace(os.Getenv("LLMD_TLS_KEY_FILE"))
	caFile := strings.TrimSpace(os.Getenv("LLMD_TLS_CLIENT_CA_FILE"))
	if certFile == "" && keyFile == "" {
		if caFile != "" {
			return false, fmt.Errorf("LLMD_TLS_CLIENT_CA_FILE requires LLMD_TLS_CERT_FILE and LLMD_TLS_KEY_FILE")
		}
		return true, nil
	}
	if certFile == "" || keyFile == "" {
		return false, fmt.Errorf("LLMD_TLS_CERT_FILE and LLMD_TLS_KEY_FILE must both be set")
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return false, fmt.Errorf("load TLS key pair: %w", err)
	}
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return false, fmt.Errorf("read LLMD_TLS_CLIENT_CA_FILE: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return false, fmt.Errorf("LLMD_TLS_CLIENT_CA_FILE %s has no valid certificates", caFile)
		}
		tlsCfg.ClientCAs = pool
		// mTLS: every client must present a certificate signed by the CA.
		tlsCfg.ClientAuth = tls.RequireAndVerifyClientCert
	}
	cfg.ServerOptions = append(cfg.ServerOptions, grpc.Creds(credentials.NewTLS(tlsCfg)))
	return true, nil
}
