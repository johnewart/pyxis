// Copyright 2026 John Ewart <john@johnewart.net>
// SPDX-License-Identifier: Apache-2.0

package llmdtest

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/llmdhost"
	"github.com/johnewart/pyxis/tenantscope"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Options configures Start.
type Options struct {
	// BinaryPath is the path to a built llmd binary (cmd/llmd). Required.
	// Build it with `go build -o llmd-e2e-bin ./cmd/llmd` (or reuse your
	// service's own build of the same binary) before running tests.
	BinaryPath string

	// DataDir holds llmd's SQLite files. A fresh temp dir is created and
	// removed on Stop if left empty.
	DataDir string

	// Env adds/overrides environment variables passed to the llmd process,
	// on top of the ones llmdtest sets itself (LLMD_DATABASE_URL,
	// LLMD_KNOWLEDGE_SQLITE_PATH, LLMD_GRPC_ADDR). Set LLMD_GRPC_ADDR here
	// to pin a specific port instead of an ephemeral one.
	Env map[string]string

	// StartTimeout bounds how long Start waits for the gRPC listener to
	// come up. Defaults to 15s.
	StartTimeout time.Duration

	// Stdout / Stderr, when set, receive the child llmd process's output
	// (handy for debugging a failed test run). Both default to discarded.
	Stdout, Stderr *os.File
}

// Env is a running llmd instance plus a fake OpenAI-compatible provider
// already registered against it, ready for a service that embeds/wraps
// llmd to drive real conversations in tests.
type Env struct {
	// GRPCAddr is the dialable address of the spawned llmd's gRPC listener.
	GRPCAddr string

	// FakeOpenAI is the scriptable upstream every registered provider talks
	// to. Enqueue turns on it before issuing a Chat/ChatStream call.
	FakeOpenAI *FakeOpenAI

	// Conn is the raw gRPC connection; Client wraps it. Most callers only
	// need Client, but Conn is exposed for advanced use (custom stubs,
	// interceptor tests, etc).
	Conn   *grpc.ClientConn
	Client llmv1.LlmServiceClient

	// ProviderID is the id RegisterLlmProvider was called with for the
	// FakeOpenAI provider; pass it as EnsureSessionRequest.LlmProviderId.
	ProviderID string

	// Host is the llmd runtime backing this Env when running via
	// StartInProcess (nil for Start, which spawns a subprocess). Use it to
	// reach the live tool registry, handler set or MCP handler.
	Host *llmdhost.Host

	// Deprecated: Server is Host, kept for one release. It was typed `any`
	// because llmd had no public server type to name — llmdhost.Host is that
	// type, so use Host.
	Server any

	cmd        *exec.Cmd
	grpcServer *grpc.Server
	dataDir    string
	ownsDir    bool
}

// Start spawns llmd, waits for its gRPC listener, and registers a fake
// OpenAI-compatible provider against a fresh workspace-agnostic provider id
// ("fake-openai"). Call Stop when done (e.g. via defer or t.Cleanup).
func Start(ctx context.Context, opts Options) (*Env, error) {
	if strings.TrimSpace(opts.BinaryPath) == "" {
		return nil, fmt.Errorf("llmdtest: Options.BinaryPath is required")
	}
	if _, err := os.Stat(opts.BinaryPath); err != nil {
		return nil, fmt.Errorf("llmdtest: llmd binary not found at %q: %w", opts.BinaryPath, err)
	}
	timeout := opts.StartTimeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}

	dataDir := opts.DataDir
	ownsDir := false
	if strings.TrimSpace(dataDir) == "" {
		d, err := os.MkdirTemp("", "llmd-e2e-")
		if err != nil {
			return nil, fmt.Errorf("llmdtest: mkdir data dir: %w", err)
		}
		dataDir = d
		ownsDir = true
	}

	fakeOpenAI, err := StartFakeOpenAI()
	if err != nil {
		return nil, err
	}

	grpcAddr, err := pickLoopbackAddr()
	if err != nil {
		_ = fakeOpenAI.Close()
		return nil, err
	}

	env := map[string]string{
		"LLMD_DATABASE_URL":          filepath.Join(dataDir, "llmd.sqlite"),
		"LLMD_KNOWLEDGE_SQLITE_PATH": filepath.Join(dataDir, "knowledge.sqlite"),
		"LLMD_GRPC_ADDR":             grpcAddr,
	}
	for k, v := range opts.Env {
		env[k] = v
	}
	if v, ok := env["LLMD_GRPC_ADDR"]; ok {
		grpcAddr = v // caller pinned a specific address
	}

	cmd := exec.CommandContext(ctx, opts.BinaryPath)
	cmd.Env = append(os.Environ(), mapToEnvSlice(env)...)
	if opts.Stdout != nil {
		cmd.Stdout = opts.Stdout
	}
	if opts.Stderr != nil {
		cmd.Stderr = opts.Stderr
	}
	if err := cmd.Start(); err != nil {
		_ = fakeOpenAI.Close()
		return nil, fmt.Errorf("llmdtest: start llmd: %w", err)
	}

	if err := waitForGRPC(ctx, grpcAddr, timeout); err != nil {
		_ = cmd.Process.Kill()
		_ = fakeOpenAI.Close()
		return nil, err
	}

	conn, err := grpc.NewClient(grpcAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		_ = cmd.Process.Kill()
		_ = fakeOpenAI.Close()
		return nil, fmt.Errorf("llmdtest: dial llmd at %s: %w", grpcAddr, err)
	}
	client := llmv1.NewLlmServiceClient(conn)

	e := &Env{
		GRPCAddr:   grpcAddr,
		FakeOpenAI: fakeOpenAI,
		Conn:       conn,
		Client:     client,
		ProviderID: "fake-openai",
		cmd:        cmd,
		dataDir:    dataDir,
		ownsDir:    ownsDir,
	}

	if err := e.registerFakeOpenAIProvider(ctx, harnessDefaultTenant); err != nil {
		_ = e.Stop(ctx)
		return nil, err
	}

	return e, nil
}

// harnessDefaultTenant is the tenant Start registers the fake provider under,
// so a broken provider path fails Start rather than the first NewSession.
const harnessDefaultTenant = "llmdtest-default"

// DefaultTenant is the tenant the fake provider is registered under at start.
const DefaultTenant = harnessDefaultTenant

// registerFakeOpenAIProvider registers (idempotently) the fake OpenAI provider
// tenant-wide in tenantID. Provider config is keyed by tenant, so a provider
// registered in one tenant is invisible to every other tenant.
func (e *Env) registerFakeOpenAIProvider(ctx context.Context, tenantID string) error {
	ctx = tenantscope.Scope{TenantID: tenantID}.OutgoingContext(ctx)
	_, err := e.Client.RegisterLlmProvider(ctx, &llmv1.RegisterLlmProviderRequest{
		ProviderId: e.ProviderID,
		Type:       llmv1.LlmProviderType_LLM_PROVIDER_TYPE_OPENAI,
		Config: &llmv1.RegisterLlmProviderRequest_Openai{
			Openai: &llmv1.OpenAiLlmConfig{
				ApiKey:       "fake-key",
				BaseUrl:      strPtr(e.FakeOpenAI.URL),
				DefaultModel: strPtr("fake-model"),
			},
		},
	})
	if err != nil {
		return fmt.Errorf("llmdtest: register fake openai provider: %w", err)
	}
	return nil
}

// NewSession ensures a session bound to sc/sessionID against the fake
// OpenAI provider (registered tenant-wide for sc.TenantID), and returns a
// context carrying the tenant + scope metadata every subsequent call on
// Client needs.
func (e *Env) NewSession(ctx context.Context, sc tenantscope.Scope, sessionID string) (context.Context, error) {
	if err := sc.Validate(); err != nil {
		return nil, fmt.Errorf("llmdtest: new session %q: %w", sessionID, err)
	}
	if err := e.registerFakeOpenAIProvider(ctx, sc.TenantID); err != nil {
		return nil, err
	}
	sessCtx := sc.OutgoingContext(ctx)
	_, err := e.Client.EnsureSession(sessCtx, &llmv1.EnsureSessionRequest{
		SessionId:     sessionID,
		LlmProviderId: strPtr(e.ProviderID),
		DefaultModel:  strPtr("fake-model"),
	})
	if err != nil {
		return nil, fmt.Errorf("llmdtest: ensure session %q: %w", sessionID, err)
	}
	return sessCtx, nil
}

// Stop tears everything down: closes the gRPC connection, kills the llmd
// process, closes the fake OpenAI server, and (if Start created it) removes
// the temp data dir. Safe to call once; idempotent calls are not supported.
func (e *Env) Stop(ctx context.Context) error {
	var firstErr error
	if e.Conn != nil {
		if err := e.Conn.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if e.cmd != nil && e.cmd.Process != nil {
		_ = e.cmd.Process.Kill()
		_, _ = e.cmd.Process.Wait()
	}
	if e.grpcServer != nil {
		e.grpcServer.Stop()
	}
	// After the server stops, so no in-flight RPC is using the databases.
	if e.Host != nil {
		if err := e.Host.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if e.FakeOpenAI != nil {
		if err := e.FakeOpenAI.Close(); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if e.ownsDir && e.dataDir != "" {
		_ = os.RemoveAll(e.dataDir)
	}
	return firstErr
}

func pickLoopbackAddr() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("llmdtest: pick loopback port: %w", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr, nil
}

func waitForGRPC(ctx context.Context, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		d := net.Dialer{Timeout: 500 * time.Millisecond}
		conn, err := d.DialContext(ctx, "tcp", addr)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("llmdtest: llmd did not start listening on %s within %s: %w", addr, timeout, err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func mapToEnvSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, fmt.Sprintf("%s=%s", k, v))
	}
	return out
}

func strPtr(s string) *string { return &s }
