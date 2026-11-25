package server

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestDiscoverProviderBinaries_RejectsNonCliProviderType(t *testing.T) {
	// Scanning for "the Anthropic binary" is meaningless; say so rather than
	// returning an empty list that reads like "you have nothing installed".
	_, err := (&LlmServer{}).DiscoverProviderBinaries(context.Background(),
		&llmv1.DiscoverProviderBinariesRequest{
			ProviderType: llmv1.LlmProviderType_LLM_PROVIDER_TYPE_ANTHROPIC,
		})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("code = %s, want InvalidArgument (err=%v)", status.Code(err), err)
	}
}

func TestDiscoverProviderBinaries_EmptyResultIsNotAnError(t *testing.T) {
	// "Found nothing" is a valid answer that a client must be able to render,
	// so it must not arrive as a gRPC error with the detail buried in a string.
	if runtime.GOOS == "windows" {
		t.Skip("PATH/HOME isolation below assumes a unix layout")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("PATH", t.TempDir())
	t.Setenv("CURSOR_AGENT_PATH", "")

	res, err := (&LlmServer{}).DiscoverProviderBinaries(context.Background(),
		&llmv1.DiscoverProviderBinariesRequest{
			ProviderType: llmv1.LlmProviderType_LLM_PROVIDER_TYPE_CURSOR_CLI,
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// searched must be populated regardless, so the user can be told where
	// llmd actually looked.
	if len(res.GetSearched()) == 0 {
		t.Fatal("searched is empty; an empty result is useless without it")
	}
}

func TestDiscoverProviderBinaries_ExplicitBinaryIsReportedBack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("fake CLI fixture is a shell script")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 9.9.9\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	res, err := (&LlmServer{}).DiscoverProviderBinaries(context.Background(),
		&llmv1.DiscoverProviderBinariesRequest{
			ProviderType: llmv1.LlmProviderType_LLM_PROVIDER_TYPE_CLAUDE_CODE_V2,
			Binary:       &bin,
		})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(res.GetBinaries()) != 1 {
		t.Fatalf("want exactly the requested binary, got %d entries", len(res.GetBinaries()))
	}
	got := res.GetBinaries()[0]
	// Discovery reports the requested path unresolved.
	if got.GetPath() != bin {
		t.Fatalf("path = %s, want %s", got.GetPath(), bin)
	}
	if !got.GetUsable() || got.GetVersion() != "9.9.9" {
		t.Fatalf("usable=%v version=%q, want usable with version 9.9.9", got.GetUsable(), got.GetVersion())
	}
	if got.GetSource() != "override" {
		t.Fatalf("source = %q, want override", got.GetSource())
	}
}
