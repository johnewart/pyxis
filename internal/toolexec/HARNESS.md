# In-process tool-execution harness

`llmd/internal/toolexec` executes tools in-process. The test harness
(`harness_test.go`) wires a real `toolregistry.Registry`, a real
`toolsession.Service` and a fixture workspace so a tool call can be dispatched
end to end **in one process** — no gRPC server, no HTTP webhook, no daemon, no
host process, no network.

## Production seam

```go
type Handler func(ctx context.Context, args json.RawMessage, inv Invocation) (Result, error)

type Result struct{ Content string }          // assistant-visible tool message

// Invocation is invocation IDENTITY and nothing else. A handler receives no
// capabilities: whatever a tool needs (workspace files, a dependency graph, a
// work tracker, an embedding index) is closed over by the registrar that owns
// it and resolved per call from inv.Scope.
type Invocation struct {
    ToolCallID, Name, ArgumentsJSON string
    AgentID, SessionID string
 Scope tenantscope.Scope
    ReadOnly                        bool
}

func NewHandlers() *Handlers
func (h *Handlers) Register(name string, fn Handler) error
func (h *Handlers) Unregister(name string) bool
func (h *Handlers) Lookup(name string) (Handler, bool)
func (h *Handlers) Names() []string

// RunWithHandlers is the only dispatch entry point. There is no
// global-consulting form: every caller — production server or test — passes
// the *Handlers set it was constructed with. A server owns its own set (see
// LlmServer.Handlers in llmd/internal/server) and registers the
// builtins it wants available at startup; two independently constructed
// servers (or two tests) can never see each other's registrations.
func RunWithHandlers(ctx, def, inv, handlers *Handlers) (content string, success bool, errMsg string)
```

`RunWithHandlers`'s `(content, success, errMsg)` contract is unchanged —
`server` and `mcpserver` both dispatch through it with their own handler set.

Gating is **additive**: a tool with no registered handler (and no registry
entry) is simply unknown. Nothing is registered-then-pruned.

## Capabilities are not part of the seam

There is no `FileSource` (or graph, or work-tracker) type in the seam, and no
field on `Invocation` to carry one. There used to be: `Env` held a slot per
capability, which meant every consumer contributing a tool with a new
dependency needed a change **inside llmd**. Capabilities now travel in the
registrar's closure:

```go
func Register(files *myFileClient) llmdtools.RegisterFunc {
    return func(reg *toolregistry.Registry, h *toolseam.Handlers) error {
        return h.Register("my_tool", func(ctx context.Context, args json.RawMessage, inv toolseam.Invocation) (toolseam.Result, error) {
            return run(ctx, args, files.For(inv.Scope)) // closed over, not delivered
        })
    }
}
```

The seam does still own path validation, because dispatch must agree with tools
on what a workspace-relative path is:

```go
func CleanWorkspacePath(relPath string) (string, error) // refuses "", "/abs", "..", null bytes
```

## Harness

```go
h := NewHarness(t, map[string]string{"pkg/sample.txt": "line one\n..."})
h.RegisterTool("read_code", llmv1.ToolClassification_TOOL_CLASSIFICATION_QUERY, readCodeHandler)
content, ok, errMsg := h.Dispatch(context.Background(), "read_code", `{"path":"pkg/sample.txt","start_line":2,"end_line":4}`)
```

- `NewHarness(t, files)` builds a `t.TempDir()` fixture workspace (no committed
  fixtures, no shared global state) plus `Registry`, `Session`, `Handlers`.
  `h.Files` is a plain test type for handlers to **close over** — the harness
  does not inject it, because production dispatch does not either.
- `RegisterTool` registers the `ToolDefinition` **and** its handler together.
  Give read-only tools `TOOL_CLASSIFICATION_QUERY` — harness dispatches run with
  `ReadOnly: true`, and `toolmerge.IsMutationTool` would reject a mutation
  classification before execution.
- `Dispatch` looks the definition up in the registry and calls
  `RunWithHandlers`, mirroring the server-side tool loop.

## Adding another tool

1. Write a `Handler` that unmarshals its own args struct and closes over
   whatever it needs (`h.Files`, a fixture graph, a recording scheduler) — no
   harness changes and no seam changes needed. Needing to add a field to the
   seam to make a tool work is the signal that something is wrong.
2. `h.RegisterTool("your_tool", ..., yourToolHandler)` alongside
   `read_code`; multiple tools in one session are covered by
   `TestHarness_twoToolsInOneSession`.
3. Note that llmd's own tool-execution seam (this package) owns only the
   session tools directly (`add_todo`, `update_todo`, `list_todos`,
   `set_status`, `set_session_title`, `ask_user`). Every product tool —
   `traverse_graph`, `search`, `enumerate`, `read_code`/`write_code`, the
   document family — is registered into the same `*Handlers` set by the host
   or by external modules contributing tools that import
   `toolseam`; this harness only exercises the dispatch mechanism, not those
   handlers' own logic.

## Running

```
cd llmd && go test ./internal/toolexec/... ./internal/toolregistry/...
cd llmd && go vet ./internal/toolexec/... ./internal/toolregistry/...
```
