//go:build integration

// Package geminiintegration runs end-to-end tests against the Gemini Developer API.
//
// These tests are NOT run by default (go test ./...). Run explicitly:
//
//	go test -tags=integration ./integration/gemini/... -count=1 -v
//
// Requires GOOGLE_API_KEY (Developer API key — test-only, not a client's config).
// Uses gemini-3.5-flash (free-tier friendly) via geminillm.DefaultModel.
package geminiintegration
