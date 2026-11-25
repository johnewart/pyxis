/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 * SPDX-License-Identifier: Apache-2.0
 */

// Upstream MCP proxy: llmd acting as an MCP *client* against servers configured
// by the user. This is the opposite direction from server.go, which serves the
// MCP surface llmd exposes outward. Files in this direction are prefixed proxy_.
package mcpproxy

import (
	"fmt"
	"strings"
)

// NormalizeUpstreamToolSegment canonicalizes one segment of a proxied MCP tool
// name so that saved per-agent tool allowlists keep resolving.
func NormalizeUpstreamToolSegment(name string) string {
	var b strings.Builder
	lastUnderscore := false
	for _, r := range name {
		ok := (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_'
		if !ok {
			r = '_'
		}
		if r == '_' {
			if lastUnderscore {
				continue
			}
			lastUnderscore = true
		} else {
			lastUnderscore = false
		}
		b.WriteRune(r)
	}
	return strings.Trim(b.String(), "_")
}

// ToolIDCollisionError reports a proxied id that collides with a reserved tool id.
type ToolIDCollisionError struct {
	ToolID        string
	ConflictsWith string
}

func (e *ToolIDCollisionError) Error() string {
	return fmt.Sprintf("proxied tool id %q collides with an existing built-in or registered tool", e.ToolID)
}

// ProxiedToolID composes the stable id an agent sees for an upstream tool.
func ProxiedToolID(providerID, upstreamName string) (string, error) {
	pid := strings.TrimSpace(providerID)
	segment := NormalizeUpstreamToolSegment(strings.TrimSpace(upstreamName))
	if pid == "" || segment == "" {
		return "", fmt.Errorf("providerId and upstreamName are required for proxied tool id")
	}
	return pid + "_" + segment, nil
}

// ParseProxiedToolID splits a proxied id back into provider and upstream name.
// Reports ok=false for anything that is not a proxied id.
func ParseProxiedToolID(id string) (providerID, upstreamName string, ok bool) {
	trimmed := strings.TrimSpace(id)
	idx := strings.Index(trimmed, "_")
	if idx <= 0 || idx >= len(trimmed)-1 {
		return "", "", false
	}
	return trimmed[:idx], trimmed[idx+1:], true
}

// AssertProxiedToolIDAvailable refuses an id already taken by a built-in tool.
func AssertProxiedToolIDAvailable(toolID string, reserved map[string]struct{}) error {
	if _, taken := reserved[toolID]; taken {
		return &ToolIDCollisionError{ToolID: toolID}
	}
	return nil
}
