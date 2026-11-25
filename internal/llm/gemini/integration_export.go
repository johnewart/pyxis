//go:build integration

package geminillm

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"
)

// IntegrationCheapModel is the model used for all Gemini integration tests.
const IntegrationCheapModel = DefaultModel

// IntegrationChatTimeout caps simple chat/generate integration tests.
const IntegrationChatTimeout = 30 * time.Second

// IntegrationToolTimeout caps tool-calling integration tests (may need extra round-trips).
const IntegrationToolTimeout = 45 * time.Second

// IntegrationScanTimeout caps model-list scan tests.
const IntegrationScanTimeout = 20 * time.Second

// IntegrationChatContext returns a context with IntegrationChatTimeout.
func IntegrationChatContext(t *testing.T) context.Context {
	return integrationTestContext(t, IntegrationChatTimeout)
}

// IntegrationToolContext returns a context with IntegrationToolTimeout.
func IntegrationToolContext(t *testing.T) context.Context {
	return integrationTestContext(t, IntegrationToolTimeout)
}

// IntegrationScanContext returns a context with IntegrationScanTimeout.
func IntegrationScanContext(t *testing.T) context.Context {
	return integrationTestContext(t, IntegrationScanTimeout)
}

func integrationTestContext(t *testing.T, timeout time.Duration) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	t.Cleanup(cancel)
	return ctx
}

// SkipUnlessGeminiAPIKey skips when GOOGLE_API_KEY is unset.
func SkipUnlessGeminiAPIKey(t *testing.T) string {
	t.Helper()
	key := strings.TrimSpace(os.Getenv("GOOGLE_API_KEY"))
	if key == "" {
		t.Skip("GOOGLE_API_KEY not set")
	}
	return key
}

// NewIntegrationClient builds a Gemini client for integration tests.
func NewIntegrationClient(t *testing.T) *Client {
	t.Helper()
	c, err := New(SkipUnlessGeminiAPIKey(t), "", IntegrationCheapModel, 256)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

// AssertContextActive fails the test when ctx has already expired (guards against hung calls).
func AssertContextActive(t *testing.T, ctx context.Context, label string) {
	t.Helper()
	select {
	case <-ctx.Done():
		t.Fatalf("%s: context expired (%v)", label, ctx.Err())
	default:
	}
}

// CheckStreamFrameContext returns ctx.Err() when the integration timeout fired mid-stream.
func CheckStreamFrameContext(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}
