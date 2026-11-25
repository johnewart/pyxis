/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

package mcpproxy

import (
	"context"
	"fmt"
	"os"
	"sort"
	"strings"
)

// SecretResolver resolves a secret ref for the calling identity, right now.
// llmd has no concrete implementation of this (it keeps no secret store);
// this seam exists so a composition root that DOES own one
// can wire it through DefaultTransportFactory without mcpproxy depending on
// that store's package. A nil resolver (the default when llmd wires this up
// itself) simply fails to resolve any secret://, slot:// ref.
type SecretResolver interface {
	Resolve(ctx context.Context, ref string) (string, error)
}

// IsSecretRef reports whether a configured value names a secret rather than
// carrying one. Anything else is a literal.
func IsSecretRef(value string) bool {
	v := strings.TrimSpace(value)
	return strings.HasPrefix(v, "secret://") || strings.HasPrefix(v, "slot://")
}

// resolveValues resolves each configured value at point of use. The returned
// map is caller-owned and short-lived: it is handed straight to one spawn and
// never retained, so a later spawn resolves again rather than reusing a value.
func resolveValues(ctx context.Context, res SecretResolver, in map[string]string) (map[string]string, error) {
	out := make(map[string]string, len(in))
	keys := make([]string, 0, len(in))
	for k := range in {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := in[k]
		if !IsSecretRef(v) {
			out[k] = v
			continue
		}
		if res == nil {
			return nil, fmt.Errorf("cannot resolve %s for %s: no secret resolver", v, k)
		}
		resolved, err := res.Resolve(ctx, v)
		if err != nil {
			// Refusals (unentitled refs) surface verbatim and carry no value.
			return nil, fmt.Errorf("resolving %s for %s: %w", v, k, err)
		}
		out[k] = resolved
	}
	return out, nil
}

// DefaultTransportFactory builds transports that resolve their credentials
// through the Phase-9 resolver at the moment of connect.
func DefaultTransportFactory(res SecretResolver) TransportFactory {
	return func(ctx context.Context, providerID string, cfg ServerConfig) (Transport, error) {
		switch cfg.Transport.Type {
		case TransportStdio:
			resolved, err := resolveValues(ctx, res, cfg.Transport.Env)
			if err != nil {
				return nil, err
			}
			return NewStdioTransport(StdioTransportOptions{
				ProviderID:    providerID,
				Command:       cfg.Transport.Command,
				Args:          cfg.Transport.Args,
				Env:           spawnEnv(resolved),
				Dir:           cfg.Transport.Cwd,
				CallTimeoutMs: cfg.CallTimeoutMs,
			}), nil
		case TransportHTTP, TransportSSE:
			resolved, err := resolveValues(ctx, res, cfg.Transport.Headers)
			if err != nil {
				return nil, err
			}
			return NewHTTPTransport(HTTPTransportOptions{
				ProviderID:    providerID,
				URL:           cfg.Transport.URL,
				Headers:       resolved,
				CallTimeoutMs: cfg.CallTimeoutMs,
			}), nil
		}
		return nil, fmt.Errorf("unknown MCP transport type %q for provider %s", cfg.Transport.Type, providerID)
	}
}

// spawnEnv composes the child environment: the process environment plus the
// values resolved for this spawn only.
func spawnEnv(resolved map[string]string) []string {
	env := append([]string{}, os.Environ()...)
	keys := make([]string, 0, len(resolved))
	for k := range resolved {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		env = append(env, k+"="+resolved[k])
	}
	return env
}
