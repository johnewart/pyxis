# Pyxis

Pyxis (the `llmd` daemon) is an embeddable agent runtime. It manages chat sessions with language models, runs tools on the model's behalf, talks to several model providers, proxies MCP servers, and keeps a searchable knowledge store. You can embed it in a Go program or run it as a standalone gRPC/Connect server and talk to it from TypeScript, React or Python.

## What it provides

- **Sessions**: persistent conversations with queued messages, forking, cancellation and a streaming event feed.
- **Tools**: a registry of tool definitions and handlers. Hosts add their own tools; Pyxis supplies a set of core session tools.
- **Providers**: model providers are registered at runtime and selected per session or agent profile.
- **MCP**: an MCP proxy that connects to MCP servers and exposes their tools, plus an MCP endpoint a host can serve.
- **Knowledge store**: a lexical index (BM25 plus name trigrams) for documents keyed by tenant and scope. It needs no embeddings or model downloads.

## Embedding Pyxis in Go

The `llmdhost` package wires Pyxis into a process you own. The host creates Pyxis, attaches it to a gRPC server it controls, and contributes its own tools. Pyxis binds no ports itself.

```go
package main

import (
	"log/slog"
	"os"

	"github.com/johnewart/pyxis/llmdhost"
	"github.com/johnewart/pyxis/llmdtools"
	"google.golang.org/grpc"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	host, err := llmdhost.New(llmdhost.Config{
		DatabaseDSN:  "llmd.sqlite",
		KnowledgeDir: "llmd-knowledge",
		Logger:       logger,
		Tools:        []llmdtools.RegisterFunc{ /* your tools */ },

		// Required for out-of-process tool executors; they must present
		// the same value.
		ToolProviderSecret: os.Getenv("TOOL_PROVIDER_SECRET"),
	})
	if err != nil {
		logger.Error("llmd host", "err", err)
		os.Exit(1)
	}
	defer host.Close()

	// Route Pyxis's diagnostics into this process's logger.
	host.InstallProcessDefaults()

	// Attach Pyxis's services to a gRPC server this process owns.
	grpcServer := grpc.NewServer()
	host.Register(grpcServer)

	// ... listen and serve grpcServer; host.MCPHandler() is Pyxis's MCP endpoint.
}
```

`examples/embed/main.go` is a complete, compiled example. It adds a custom tool, serves the MCP handler, composes system prompt sections, plugs a custom chunker and reranker into the knowledge store, and attaches tenant and scope to calls. It lives in its own Go module so it can only use Pyxis's public packages.

## Running the standalone binary

`cmd/llmd` is a standalone server built on `llmdhost`. It is a separate Go module:

```bash
go build -C cmd/llmd -o ../../llmd .
./llmd
```

It is configured with environment variables. The main ones:

| Variable | Purpose |
|----------|---------|
| `LLMD_GRPC_ADDR` | gRPC listen address (default is loopback). |
| `LLMD_DATABASE_URL` | Main database. |
| `LLMD_KNOWLEDGE_DIR` | Knowledge index directory. |
| `LLMD_MCP_HTTP_ADDR` | Address for the MCP HTTP endpoint. |
| `LLMD_CONNECT_HTTP_ADDR` | Address for the Connect/gRPC-Web endpoint. |
| `LLMD_CONNECT_TOKEN` | Bearer token required on the Connect endpoint. |
| `LLMD_CONNECT_ALLOWED_ORIGINS` | Allowed browser origins for the Connect endpoint. |
| `LLMD_MCP_PROXY_ENABLED` | Enable the MCP proxy. |

See `cmd/llmd/main.go` for the full list. The SQLite driver uses CGO; see `docs/DEVELOPING.md` for build requirements.

## TypeScript packages

| Package | Contents |
|---------|----------|
| `@pyxis-llm/client` | TypeScript client for Pyxis's `LlmService`. Import `@pyxis-llm/client/node` for native gRPC or `@pyxis-llm/client/web` for Connect over `fetch`. |
| `@pyxis-llm/core` | Domain types and wire-contract helpers with no transport or framework dependencies. |
| `@pyxis-llm/react` | React chat and markdown components built on `@pyxis-llm/core` and Mantine. |

```bash
npm install @pyxis-llm/client @pyxis-llm/core @pyxis-llm/react
```

The `@pyxis-llm/cli-*` packages (`cli-linux-x64`, `cli-linux-arm64`, `cli-darwin-arm64`, `cli-win32-x64`) carry the `llmd` binary for each platform.

## Python client

`packages/python-client` is the `pyxis-client` gRPC client:

```bash
pip install ./packages/python-client
```

## Development

Build, test and code generation notes are in [docs/DEVELOPING.md](docs/DEVELOPING.md). See [CONTRIBUTING.md](CONTRIBUTING.md) to contribute and [SECURITY.md](SECURITY.md) to report a vulnerability.

## License

Apache License 2.0. See [LICENSE](LICENSE).
