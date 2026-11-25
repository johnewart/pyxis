// Copyright 2026 John Ewart <john@johnewart.net>
// SPDX-License-Identifier: Apache-2.0

package llmdtest

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/llmdhost"
	"github.com/johnewart/pyxis/llmdtools"
	"github.com/johnewart/pyxis/toolregistry"
	"github.com/johnewart/pyxis/toolseam"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

const inProcessBufSize = 1024 * 1024

// InProcessOptions configures StartInProcess.
type InProcessOptions struct {
	// DataDir holds llmd's SQLite files. A fresh temp dir is created and
	// removed on Stop if left empty.
	DataDir string

	// RegisterTools installs the HOST's own inline-builtin tools — the
	// same registrar shape llmd's own subsystems use (see
	// internal/server/graph_fixture_test.go's registerFakeGraphTools for
	// the pattern): it receives the server's live *toolregistry.Registry
	// and *toolseam.Handlers and registers definitions/handlers into them.
	// This is the piece a host embedding llmd could not exercise before:
	// its own tools, dispatched through a real llmd tool-call turn.
	RegisterTools func(reg *toolregistry.Registry, handlers *toolseam.Handlers) error

	// Deprecated: SkipDefaultBuiltins is ignored. llmd's core session tools are
	// always installed, because that is the guarantee llmdhost makes: a Host
	// cannot produce a half-wired agent. A test asserting on "only my tools"
	// should filter the registry snapshot by name rather than ask llmd to stand
	// up an agent that cannot set a status or keep a todo list — a shape no
	// production caller can create, and therefore not worth testing.
	SkipDefaultBuiltins bool

	// ToolProviderSecret is passed to llmdhost.Config.ToolProviderSecret.
	// Set it (and the same value on the executor) to test executor tools;
	// empty refuses every ToolProviderService registration.
	ToolProviderSecret string

	// ListenAddr, when non-empty, serves gRPC on a real TCP listener at this
	// address (e.g. "127.0.0.1:0" for an ephemeral port) instead of an
	// in-memory bufconn. Env.GRPCAddr is then the actual bound address and
	// Env.Client dials it over TCP. Everything else is constructed identically.
	ListenAddr string
}

// inProcessListener is the listener choice made by newInProcessListener: the
// listener to serve on, the address clients dial, and the dial options that
// reach it.
type inProcessListener struct {
	lis      net.Listener
	addr     string
	target   string
	dialOpts []grpc.DialOption
}

// newInProcessListener picks bufconn (listenAddr empty) or TCP.
func newInProcessListener(listenAddr string) (*inProcessListener, error) {
	if strings.TrimSpace(listenAddr) != "" {
		lis, err := net.Listen("tcp", listenAddr)
		if err != nil {
			return nil, fmt.Errorf("llmdtest: listen %s: %w", listenAddr, err)
		}
		addr := lis.Addr().String()
		return &inProcessListener{lis: lis, addr: addr, target: addr}, nil
	}
	lis := bufconn.Listen(inProcessBufSize)
	dialer := func(dctx context.Context, _ string) (net.Conn, error) {
		return lis.DialContext(dctx)
	}
	return &inProcessListener{
		lis:      lis,
		addr:     "bufconn",
		target:   "passthrough:///llmdtest-inprocess",
		dialOpts: []grpc.DialOption{grpc.WithContextDialer(dialer)},
	}, nil
}

// StartInProcess constructs a real llmd server in the CALLER's own process
// — no subprocess, no real network socket — and serves it over an
// in-memory bufconn listener. Everything else (the fake OpenAI provider,
// Turn scripting, NewSession, Stop) behaves exactly like Start; Env.Client
// is a normal llmv1.LlmServiceClient that happens to be wired in-process.
//
// This is for a service that embeds/wraps llmd and wants to test its OWN
// inline builtin tools (its own graphtools/wstools-equivalent registrar)
// dispatched through a real llmd tool-call turn, without building or
// spawning the llmd binary. Pass RegisterTools to install those tools.
func StartInProcess(ctx context.Context, opts InProcessOptions) (*Env, error) {
	dataDir := opts.DataDir
	ownsDir := false
	if strings.TrimSpace(dataDir) == "" {
		d, err := os.MkdirTemp("", "llmd-e2e-inproc-")
		if err != nil {
			return nil, fmt.Errorf("llmdtest: mkdir data dir: %w", err)
		}
		dataDir = d
		ownsDir = true
	}

	// Built through the PUBLIC facade, deliberately. This harness used to wire
	// its own LlmServer with six fields where production sets two dozen, so a
	// test could pass against a server no real deployment ever runs. Going
	// through llmdhost means a consumer's integration test and llmd's own binary
	// assemble the runtime identically — and that anything llmdhost cannot
	// express shows up as a failure here rather than as silent divergence.
	var registrars []llmdtools.RegisterFunc
	if opts.RegisterTools != nil {
		registrars = append(registrars, llmdtools.RegisterFunc(opts.RegisterTools))
	}
	host, err := llmdhost.New(llmdhost.Config{
		DatabaseDSN:         filepath.Join(dataDir, "llmd.sqlite"),
		KnowledgeSQLitePath: filepath.Join(dataDir, "llmd-knowledge.sqlite"),
		Tools:               registrars,
		ToolProviderSecret:  opts.ToolProviderSecret,
		// No idle eviction: a test that pauses between turns must not have its
		// session runtime swept out from under it.
		RuntimeIdleEvict: -1,
	})
	if err != nil {
		return nil, fmt.Errorf("llmdtest: %w", err)
	}

	grpcServer := grpc.NewServer()
	host.Register(grpcServer)

	il, err := newInProcessListener(opts.ListenAddr)
	if err != nil {
		_ = host.Close()
		if ownsDir {
			_ = os.RemoveAll(dataDir)
		}
		return nil, err
	}
	go func() {
		if err := grpcServer.Serve(il.lis); err != nil {
			log.Printf("llmdtest: grpc serve ended: %v", err)
		}
	}()

	fakeOpenAI, err := StartFakeOpenAI()
	if err != nil {
		grpcServer.Stop()
		_ = host.Close()
		return nil, err
	}

	dialOpts := append([]grpc.DialOption{grpc.WithTransportCredentials(insecure.NewCredentials())}, il.dialOpts...)
	conn, err := grpc.NewClient(il.target, dialOpts...)
	if err != nil {
		grpcServer.Stop()
		_ = fakeOpenAI.Close()
		_ = host.Close()
		return nil, fmt.Errorf("llmdtest: dial in-process llmd: %w", err)
	}
	client := llmv1.NewLlmServiceClient(conn)

	e := &Env{
		GRPCAddr:   il.addr,
		FakeOpenAI: fakeOpenAI,
		Conn:       conn,
		Client:     client,
		ProviderID: "fake-openai",
		Server:     host,
		Host:       host,
		grpcServer: grpcServer,
		dataDir:    dataDir,
		ownsDir:    ownsDir,
	}

	if err := e.registerFakeOpenAIProvider(ctx, harnessDefaultTenant); err != nil {
		_ = e.Stop(ctx)
		return nil, err
	}

	return e, nil
}
