// Copyright 2026 John Ewart <john@johnewart.net>
// SPDX-License-Identifier: Apache-2.0

// Command llmd-fake serves an in-process llmd backed by the llmdtest fake
// OpenAI provider over real TCP, for test clients in other processes or
// languages. On startup it prints one JSON line on stdout:
//
//	{"grpc_addr":"127.0.0.1:41234","provider_id":"fake-openai","tenant_id":"llmdtest-default","fake_url":"http://127.0.0.1:35000"}
//
// Post scripted model turns to fake_url + "/_script" and read the model
// requests llmd made from fake_url + "/_requests" (schema documented in
// llmdtest/fakeopenai_control.go). Clients must send tenant_id in their tenant
// metadata. Persistence uses a temp dir removed on exit. It runs until
// SIGINT/SIGTERM or until stdin is closed.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/johnewart/pyxis/llmdtest"
)

// readyLine is the JSON printed once the server is ready.
type readyLine struct {
	GRPCAddr   string `json:"grpc_addr"`
	ProviderID string `json:"provider_id"`
	TenantID   string `json:"tenant_id"`
	FakeURL    string `json:"fake_url"`
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdin, os.Stdout); err != nil {
		log.Printf("llmd-fake: %v", err)
		stop()
		os.Exit(1)
	}
}

// run starts the server, writes the ready line to stdout, and blocks until
// ctx is canceled or stdin reaches EOF, then shuts everything down.
func run(ctx context.Context, args []string, stdin io.Reader, stdout io.Writer) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	fs := flag.NewFlagSet("llmd-fake", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:0", "TCP address for the gRPC listener")
	toolSecret := fs.String("tool-secret", "", "shared secret for ToolProviderService registrations (empty refuses them)")
	if err := fs.Parse(args); err != nil {
		return fmt.Errorf("parse flags: %w", err)
	}

	env, err := llmdtest.StartInProcess(ctx, llmdtest.InProcessOptions{
		ListenAddr:         *listen,
		ToolProviderSecret: *toolSecret,
	})
	if err != nil {
		return fmt.Errorf("start: %w", err)
	}

	b, err := json.Marshal(readyLine{
		GRPCAddr:   env.GRPCAddr,
		ProviderID: env.ProviderID,
		TenantID:   llmdtest.DefaultTenant,
		FakeURL:    env.FakeOpenAI.URL,
	})
	if err == nil {
		_, err = fmt.Fprintf(stdout, "%s\n", b)
	}
	if err != nil {
		if stopErr := env.Stop(context.Background()); stopErr != nil {
			log.Printf("llmd-fake: stop after failed ready line: %v", stopErr)
		}
		return fmt.Errorf("write ready line: %w", err)
	}

	go func() {
		if _, err := io.Copy(io.Discard, stdin); err != nil {
			log.Printf("llmd-fake: reading stdin: %v", err)
		}
		cancel()
	}()

	<-ctx.Done()
	if err := env.Stop(context.Background()); err != nil {
		return fmt.Errorf("stop: %w", err)
	}
	return nil
}
