// Copyright 2026 John Ewart <john@johnewart.net>
// SPDX-License-Identifier: Apache-2.0

// Command fakeserver starts an in-process llmd backed by the llmdtest fake
// OpenAI provider, serves its gRPC API on a loopback TCP port, prints the
// address on stdout as "LISTENING host:port", and serves until killed. It
// exists so non-Go clients (the Python smoke test) can talk to a real llmd
// without a model API key.
//
// The fake provider is registered for tenant "llmdtest-default"; callers must
// send that tenant id in the llmd-tenant-id metadata.
package main

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/johnewart/pyxis/llmdtest"
	"google.golang.org/grpc"
)

// replyText is the content of every scripted fake reply.
const replyText = "hello from fake model"

// scriptedTurns is how many replies are queued; each model request uses one.
const scriptedTurns = 1000

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fakeserver: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	env, err := llmdtest.StartInProcess(ctx, llmdtest.InProcessOptions{})
	if err != nil {
		return fmt.Errorf("start in-process llmd: %w", err)
	}
	defer func() {
		if serr := env.Stop(context.Background()); serr != nil {
			fmt.Fprintf(os.Stderr, "fakeserver: stop: %v\n", serr)
		}
	}()

	turns := make([]llmdtest.Turn, scriptedTurns)
	for i := range turns {
		turns[i] = llmdtest.TextTurn(replyText)
	}
	env.FakeOpenAI.Enqueue(turns...)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	srv := grpc.NewServer()
	env.Host.Register(srv)

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(lis) }()

	fmt.Printf("LISTENING %s\n", lis.Addr().String())

	select {
	case <-ctx.Done():
		srv.Stop()
		return nil
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("serve: %w", err)
		}
		return nil
	}
}
