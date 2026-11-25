// Command llmd-embed-example is a complete llmd host built from OUTSIDE llmd's
// module path, using only llmd's public API.
//
// It exists to be compiled, not run. Its job is to fail the build the moment
// llmd's public surface stops being sufficient to host llmd — see this module's
// go.mod for why a package inside llmd's own import-path tree cannot do that.
//
// It deliberately exercises every part a real consumer needs:
//
//   - llmdhost.New          wire + migrate, binding nothing
//   - Host.Register         attach llmd to a gRPC server the CALLER owns
//   - llmdtools.RegisterFunc contribute a tool with its own dependency
//   - toolseam.Invocation   dispatch identity, capability from the closure
//   - systemprompt          prompt sections
//   - Config.Logger         llmd's diagnostics into the host's logger
//   - Host.MCPHandler       llmd's MCP endpoint on the host's own listener
//
// If a future change forces this file to import llmd/internal/... to keep
// working, that import will not compile, and the right fix is to widen the
// public API rather than to work around it here.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"

	"github.com/johnewart/pyxis/tenantscope"

	"github.com/johnewart/pyxis/coretools"
	"github.com/johnewart/pyxis/lexstore"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/llmdhost"
	"github.com/johnewart/pyxis/llmdlog"
	"github.com/johnewart/pyxis/llmdtools"
	"github.com/johnewart/pyxis/systemprompt"
	"github.com/johnewart/pyxis/toolregistry"
	"github.com/johnewart/pyxis/toolseam"
	"google.golang.org/grpc"
)

// fileIndex stands in for whatever per-scope dependency a real consumer's
// tools need: a dependency graph, a project daemon, a work tracker, an
// embedding index. llmd knows nothing about it, and that is the property this
// example is here to protect.
type fileIndex struct{ root string }

// For resolves the index for one tenant + scope. A tenant-wide call (empty
// ScopeID) gets the tenant's root.
func (f *fileIndex) For(sc tenantscope.Scope) *fileIndex {
	root := f.root + "/" + sc.TenantID
	if sc.ScopeID != "" {
		root += "/" + sc.ScopeID
	}
	return &fileIndex{root: root}
}

func (f *fileIndex) Read(path string) (string, error) {
	return fmt.Sprintf("contents of %s/%s", f.root, path), nil
}

// registerReadFile contributes one tool. This is the whole extension pattern:
// the registrar CLOSES OVER its dependency and resolves it per call from
// Invocation.Scope. Nothing is handed to the handler by llmd except the
// identity of the call, so adding a tool with a brand-new dependency never
// requires a change inside llmd.
func registerReadFile(idx *fileIndex) llmdtools.RegisterFunc {
	return func(reg *toolregistry.Registry, handlers *toolseam.Handlers) error {
		// Both halves, or the tool is broken in one direction: a definition
		// without a handler is advertised but fails to execute; a handler
		// without a definition is never offered to a model.
		if err := reg.Register(&llmv1.ToolDefinition{
			Name:           "read_file",
			Classification: llmv1.ToolClassification_TOOL_CLASSIFICATION_QUERY,
			Execution: &llmv1.ToolDefinition_InlineBuiltin{
				InlineBuiltin: &llmv1.ToolExecutionInlineBuiltin{},
			},
		}); err != nil {
			return err
		}
		return handlers.Register("read_file", func(_ context.Context, args json.RawMessage, inv toolseam.Invocation) (toolseam.Result, error) {
			var parsed struct {
				Path string `json:"path"`
			}
			if len(args) > 0 {
				if err := json.Unmarshal(args, &parsed); err != nil {
					return toolseam.Result{}, err
				}
			}
			body, err := idx.For(inv.Scope).Read(parsed.Path)
			if err != nil {
				return toolseam.Result{}, err
			}
			return toolseam.Result{Content: body}, nil
		})
	}
}

// knowledgeOptions plugs this host's corpus into llmd's knowledge index:
// markdown notes split at headings, each section defining its heading as a
// name, reranked by query-term overlap. Every seam is optional.
func knowledgeOptions() lexstore.Options {
	return lexstore.Options{
		Chunker: lexstore.ChunkerFunc(func(d lexstore.DocumentIn) []string {
			var out []string
			for _, sec := range strings.Split(d.Content, "\n## ") {
				if s := strings.TrimSpace(sec); s != "" {
					out = append(out, s)
				}
			}
			return out
		}),
		Namer: lexstore.NamerFunc(func(c lexstore.ChunkInfo) ([]string, []string) {
			heading, _, _ := strings.Cut(c.Content, "\n")
			return []string{strings.TrimLeft(heading, "# ")}, []string{c.Name}
		}),
		Reranker: lexstore.TermOverlapReranker,
	}
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	host, err := llmdhost.New(llmdhost.Config{
		DatabaseDSN:  "embed-example.sqlite",
		KnowledgeDir: "embed-example-knowledge",
		Logger:              logger,
		Tools: []llmdtools.RegisterFunc{
			registerReadFile(&fileIndex{root: "/workspaces"}),
		},
		MCPProxyEnabled: true,

		// Required for out-of-process tools: executors (package toolexecutor)
		// must present this same value or llmd refuses their registration. The
		// host decides where it comes from; llmdhost never reads the environment.
		// An in-process host can mint a random value and hand it to both sides.
		ToolProviderSecret: os.Getenv("EMBED_TOOL_PROVIDER_SECRET"),

		// The host's corpus shape: chunking, naming, reranking.
		KnowledgeOptions: knowledgeOptions(),
	})
	if err != nil {
		logger.Error("llmd host", "err", err)
		os.Exit(1)
	}
	defer host.Close()

	// Routes llmd's diagnostics into this process's logger. Explicit because it
	// writes process-wide state.
	host.InstallProcessDefaults()

	// The critical path for an embedder: llmd's services go onto a gRPC server
	// this process owns, alongside its own, rather than llmd running standalone.
	grpcServer := grpc.NewServer()
	host.Register(grpcServer)

	// The host's own tools are live in the same registry llmd dispatches from.
	for _, def := range host.Tools().Snapshot() {
		logger.Info("tool advertised", "name", def.GetName())
	}
	if _, ok := host.Handlers().Lookup("read_file"); !ok {
		logger.Error("read_file handler missing")
		os.Exit(1)
	}

	// llmd's MCP endpoint on a listener this process chose. llmd picks no ports.
	mcpLis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		logger.Error("mcp listen", "err", err)
		os.Exit(1)
	}
	go func() { _ = http.Serve(mcpLis, host.MCPHandler()) }()

	// System prompt sections are PER REQUEST (or per agent profile), not per
	// host: they ride on ChatStreamRequest, so there is no Config.Prompt to set.
	// A host composes them and renders provider-specific delivery like this
	// before issuing a call.
	sections := []systemprompt.Section{
		{ID: "example.intro", Content: "You are running inside an embedded llmd host."},
		{ID: "workspace.isolation", Content: systemprompt.ClaudeIsolationSection},
	}
	rendered := systemprompt.RenderSystemPrompt(sections, systemprompt.CursorOverlay)
	logger.Info("prompt rendered",
		"prompt_bytes", len(rendered.PromptBody),
		"mdc_files", len(rendered.MDCFiles),
	)

	// Every agent gets llmd's core session tools whatever else is registered. A
	// host asserting on its own tool surface needs to know which names are llmd's
	// rather than hard-coding a list that silently goes stale.
	for _, name := range coretools.Names() {
		if _, ok := host.Handlers().Lookup(name); !ok {
			logger.Error("core tool not registered", "name", name)
			os.Exit(1)
		}
	}

	// A host emits its own records in llmd's NDJSON shape, so one parser reads
	// one format for both.
	llmdlog.Stdout("embed-example", llmdlog.LevelInfo, "host started with %d core tools", coretools.Count())

	// Sessions: the out-of-band operations a host performs around a chat turn.
	sessions := host.Sessions()
	logger.Info("sessions", "loaded", sessions.Count())

	// Tenant + scope travel as gRPC metadata; a host picks both and attaches
	// them with tenantscope.Scope{TenantID: "acme", ScopeID: "project-1"}.OutgoingContext(ctx)
	// rather than spelling a wire key.
	logger.Info("llmd embedded",
		"session_events_ws_path", llmdhost.SessionEventsWSPath,
		"runtimes_loaded", host.RuntimeCount(),
		"database", host.DatabaseDSN(),
		"knowledge_database", host.KnowledgeDSN(),
		"mcp_addr", mcpLis.Addr().String(),
	)

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		logger.Error("listen", "err", err)
		os.Exit(1)
	}
	if err := grpcServer.Serve(lis); err != nil {
		logger.Error("serve", "err", err)
		os.Exit(1)
	}
}
