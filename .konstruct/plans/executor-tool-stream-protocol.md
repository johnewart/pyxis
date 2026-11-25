---
konstruct:
  docUuid: 26754c51-3be1-4933-bb79-dde0ecf9668e
  docKind: plan
created: 2026-10-04T02:30:22.304Z
implemented: false
title: Executor tool stream protocol
summary: "Three milestones: proto + Hub executor registry,
  dispatch/listing/turn-cancel in llmd, then konstructd run_command adapter."
sourceDesign:
  name: executor-tool-stream-protocol.md
  docUuid: d600bcff-5984-42e5-b1c5-b23208c29eec
  title: Executor tool stream protocol
assistantSessionId: 8d1e1a91-8d53-47d8-ad09-804331d98513
---
# Executor tool stream protocol — implementation plan

Source design: *Executor tool stream protocol*. This plan covers the llmd side in this repo, then the konstructd adapter (Milestone 3), which lives outside this repo.

## What the code looks like today (graph- and read-backed)

* `internal/toolprovider/hub.go` — `Hub{streams map[Scope]*activeStream, pending map[execID]chan Result}`. `Register`, `Invoke`, `DeliverResult` and `HasProvider` have **no per-stream pending table**: when `Register`'s `unregister()` runs, it only deletes the stream, so waiters sit until their timeout. Inbound users: `internal/server/server.go` (≈L1873/L1922), `internal/mcpserver/server.go` (≈L525/L527), `llmdhost/host.go` (L182, `NewHub`), plus `internal/server/tool_dispatch_test.go` and `internal/mcpserver/tool_dispatch_test.go`. That makes 4 production dependents, so the fan-in is small.
* `internal/toolprovider/server.go` — `Server.Connect` requires `GetHello()` as the first frame, checks `LLMD_TOOL_PROVIDER_SHARED_SECRET`, and gets the scope from `serverutil.ScopeFromContext`. After hello, the `default:` arm drops unknown frames **without logging**, which breaks the never-swallow rule.
* `internal/toolexec/exec.go` — `RunWithHandlers(ctx, def, inv, *Handlers)` runs the read-only check first and then switches on `http_webhook / inline_builtin / delegate_to_client`. **It cannot reach the Hub.** `Handlers` is `toolseam.Handlers`, a plain name→func map. Production callers are `internal/server/server.go:2068` (chat loop), `internal/server/invoke_tool.go:99` and `internal/mcpserver/server.go:589`. About 14 more test call sites exist in `internal/toolexec/*_test.go` and `internal/server/invoke_tool_test.go`. `exec.go` has ≈17 inbound dependents, so the signature **must not change**.
* `toolseam/handler.go` — `Invocation{ToolCallID, Name, ArgumentsJSON, AgentID, SessionID, ReadOnly, Scope}`. It has **no `TurnID` and no origin**, and the design's `ToolCall` needs both.
* `internal/toolmerge/effective.go` — `Effective(Request{ServerWide, Session, Disabled, Policy})` is the single point that decides what is both listed and executable. The listing callers are `internal/server/server.go:519` (`registeredTools()`) and `internal/mcpserver/server.go:732` (`ServerWideToolDefinitions()`).
* **There is no turn-cancel RPC or function.** A grep for `CancelTurn|Interrupt|Abort` RPCs found nothing. A turn ends when the client aborts the `ChatStream` (see `internal/server/interrupt_order_test.go` and `turn_gate.go`). The "existing turn-cancel path" in the design is therefore **the ChatStream ctx ending before the turn completes**, and `Hub.CancelTurn` gets wired there (Phase 2.4). I've flagged this as a gap in the design, not invented an RPC.
* Codegen: `make generate` (root `Makefile`) regenerates `gen/llmv1` and the TS clients.

### Decisions made while planning

1. **The dispatch seam is a new optional interface, not a new `RunWithHandlers` parameter.** Add `toolseam.ExecutorDispatcher` (one method, `Call(ctx, def, inv) (content string, success bool, errMsg string)`). `Handlers` gets a setter/getter for it (`SetExecutor` / `Executor`). `RunWithHandlers` keeps its signature, and none of its \~17 dependents change. `*toolprovider.Hub` implements the interface, and each composition root installs it on the server's handler set.
2. **`Invocation` gains `TurnID string` and `Origin` (an int enum in `toolseam`).** Both fields are additive and zero-valued for existing callers. The three production dispatch sites fill them in.
3. **Executor tools are tenant-scoped definitions held by the Hub**, not rows in the server-wide registry. Listing merges them into `Request.ServerWide` only for the matching tenant, through a new `Hub.ExecutorDefinitions(tenantID)`. No persistence is added, so the schema rule doesn't apply.

::konstruct{#milestone-1}

## Milestone 1 — the wire and Hub support executor streams, with no dispatch yet

When this milestone is done, the proto carries every executor message. Legacy provider calls fail immediately on disconnect instead of waiting out their timeout. A fake executor can connect with `ExecutorHello`, and it gets an ack that accepts or rejects each tool according to the per-tenant ownership rules. Supersede and detach behave as the design describes. No tool is routed to an executor yet, so product behaviour is unchanged apart from the faster legacy failure. The proof is `go build ./...` plus targeted `go test ./internal/toolprovider/...` runs with bufconn tests.

::konstruct{#phase-1-1}

### Phase 1.1 — Proto messages and codegen

* In `proto/llmv1/llm.proto`, add `ToolExecutionExecutor` as a new arm of `ToolDefinition.execution` (≈L288; use the next free field number). Also add `ExecutorHello`, `ExecutorHelloAck`, `RejectedTool`, `ToolCallOrigin`, `ToolCall`, `ToolCallCancel`, `ToolCallResult`, `ToolCallOutput` and `ToolCallFailure`, exactly as written in the design. The design's `ToolAttachment` is the existing `ToolResultAttachment` (the type `Hub.DeliverResult` already uses). Add the new oneof arms `ToolProviderToServer.frame{executor_hello, tool_call_result}` and `ToolServerToProvider.frame{executor_hello_ack, tool_call, tool_call_cancel}`, numbered after the existing arms. Don't renumber anything.
* Run `make generate` and commit the regenerated `gen/llmv1/*` and TS client output.
* Gotcha: `RunWithHandlers`' `default:` arm will now return "no execution binding" for executor defs. That's acceptable until Phase 2.2.
* Size: 1 proto file plus generated output. No Go logic changes.
* **Proof:** `go build ./...` exits 0. `ToolCall`, `ExecutorHello` and `ToolExecutionExecutor` are defined in `gen/llmv1`.

::konstruct{#phase-1-2}

### Phase 1.2 — Per-stream pending table; legacy calls fail on disconnect

* In `hub.go`, give `activeStream` an id and a `pending map[string]chan Result`. `Invoke` now records its waiter on the stream as well as in the global `pending` map (or moves to the per-stream map with a stream-indexed lookup for `DeliverResult`). Register's `unregister()` closes or fills every pending waiter with a typed "tool provider disconnected, outcome unknown" error and logs at `Warn` with the scope and the number of calls failed.
* Supersede in `Register` (a new stream replaces an old one for the same scope) drains the old stream the same way.
* Keep the `Invoke` / `DeliverResult` / `HasProvider` signatures, which the 4 production dependents use, unchanged.
* In `server.go`, replace the silent `default:` with a `Debug` log naming the frame type.
* Size: 2 files, about 4 dependents unaffected because the signatures stay the same.
* **Proof:** a new `internal/toolprovider/hub_disconnect_test.go` (a fake stream over bufconn or a stub `ToolProviderService_ConnectServer`) shows that an `Invoke` blocked with a 60s timeout returns the disconnected error within 1s after the stream ends. Run it with `go test ./internal/toolprovider/... -run Disconnect`. The existing `go test ./internal/server/... -run ToolDispatch` and `go test ./internal/mcpserver/... -run ToolDispatch` still pass.

::konstruct{#phase-1-3}

### Phase 1.3 — Executor registry and hello/ack handling

* Add a new file `internal/toolprovider/executor.go` containing:
  * `executorKey{tenantID, executorID}` → `*executorEntry{instanceID, stream *activeStream, names []string, defs map[name]*ToolDefinition}`.
  * `owners map[tenantName]executorID` and `defs map[tenant]map[name]*ToolDefinition`. Definitions stay registered when the stream detaches (ownerless, as the design specifies).
  * `Hub.InlineNames func() map[string]struct{}`, an injected checker for globally-held inline names that is set by the composition root. If it's nil, no inline conflicts are detected.
  * `Hub.HandleExecutorHello(sc, hello, stream) (ack, detach func())`. Under the registry lock it accepts each tool unless the name is held inline, the name is held by a different executor in the same tenant, or the execution isn't `ToolExecutionExecutor{executor_id == hello.executor_id}`. The same `(tenant, executor_id)` with a new `instance_id` supersedes: swap the owner, then drain the old stream's pending table (from Phase 1.2). `detach` makes the tools ownerless and logs at `Warn` with `executor_id`, `instance_id` and the pending count.
  * `MaxResultBytes` constant (for example 1 MiB), returned in the ack.
  * `Hub.ExecutorDefinitions(tenantID) []*ToolDefinition`, used in Milestone 2.
* In `server.go`, make `Connect` branch on the first frame. `GetHello()` takes the legacy path unchanged. `GetExecutorHello()` takes the new path, with the **same** secret and `ScopeFromContext` checks factored into a shared helper. It sends `ExecutorHelloAck`, then loops on `tool_call_result`. Delivery is a stub until Phase 2.1, but any frame that arrives is logged.
* Size: 1 new file plus `server.go`. No external dependents yet.
* **Proof:** `internal/toolprovider/executor_hello_test.go` uses bufconn with a real `Server` and covers: (a) the ack accepts new names and rejects an inline-held name with a reason; (b) a second executor in the same tenant claiming the name is rejected; (c) another tenant can claim the same name; (d) a new `instance_id` supersedes; (e) a hello with a wrong or unset secret is refused. Gate: `go test ./internal/toolprovider/... -run ExecutorHello`.

::konstruct{#milestone-2}

## Milestone 2 — executor tools are listed per tenant and dispatched from every origin, with cancel and turn-cancel

When this milestone is done, an executor-declared tool appears only in its own tenant's sessions. A call from the chat loop, MCP or `InvokeTool` reaches the executor as a `ToolCall`. Cancel, timeout, oversize, unattached and disconnect each produce llmd-owned failure strings, and aborting a ChatStream turn cancels every in-flight executor call of that turn and blocks new ones. The proof is targeted `go test` runs in `internal/toolprovider`, `internal/toolexec`, `internal/server` and `internal/mcpserver` with a fake executor.

::konstruct{#phase-2-1}

### Phase 2.1 — `Hub.Call`: correlation, cancel, deadline and size cap

* Add `Hub.Call(ctx, def, inv toolseam.Invocation) (content string, success bool, errMsg string)` with the following steps:
  * Look up `(inv.Scope.TenantID, name)`. With no definition, return the "not registered" error. With a definition but no owner, fail fast with "executor \[id] is not attached" (rule 5).
  * Reject the call if the turn is tombstoned (stubbed here; filled in by Phase 2.4).
  * Check that the stream's tenant equals the call's tenant (a `Warn` and refusal otherwise).
  * Mint a `call_id`, register it in the stream's pending table, and send `ToolCall` with the identity fields, the origin and `deadline = now + max_duration_seconds`. Use a default of 600s when the field is 0, and wrap ctx with that deadline.
  * `select` on the result, `ctx.Done()` or a stream drain:
    * On `ctx.Done()`, send `ToolCallCancel` and return "cancelled" or "timed out" at once, logging at `Info` with origin, elapsed time and max duration.
  * Deliver `tool_call_result`. An unknown or late `call_id` is logged at `Debug` and dropped. A result larger than `MaxResultBytes` becomes a failure that states the size.
* Wire `Server.Connect`'s `tool_call_result` arm to `Hub.DeliverCallResult`.
* Implement `toolseam.ExecutorDispatcher`. Add that interface and the `Invocation.TurnID` / `Invocation.Origin` fields to `toolseam/handler.go` in this phase. They're additive and zero-valued, so dependents are unaffected.
* Size: `hub.go` / `executor.go` / `server.go` plus `toolseam/handler.go`. `toolseam` has ≈4 importers and doesn't break because the change is additive.
* **Proof:** `internal/toolprovider/executor_call_test.go` covers a completed output, a handler failure passed through verbatim, cancel returning before any result (with the late result dropped), a deadline, oversize, unattached fast-fail, and a stream drop failing pending calls. Gate: `go test ./internal/toolprovider/... -run ExecutorCall`.

::konstruct{#phase-2-2}

### Phase 2.2 — Executor case in `RunWithHandlers` and composition-root wiring

* In `toolseam.Handlers`, add `SetExecutor(ExecutorDispatcher)` / `Executor()`.
* In `exec.go`, add `case def.GetExecutor() != nil:` **after** the existing read-only check. Call `handlers.Executor().Call(ctx, def, inv)`. If no executor is set, return "executor dispatch is not configured".
* Wiring: wherever the server's handler set is built (`internal/server` `handlerSet()`, plus `llmdhost/host.go`, which already creates `toolHub` at L182), call `SetExecutor(h.toolHub)` and set `toolHub.InlineNames` from the server-wide registry. Per the project rule, wire this in `llmdhost` (the shared root konstructd links), **not** in `cmd/llmd/main.go` alone.
* At the three production sites (`server.go:~2068` chat loop, `invoke_tool.go:99`, `mcpserver/server.go:589`), set `inv.TurnID` and `inv.Origin`. Find the turn id available at each site (the chat loop has `turnID`; for MCP, follow the session's current turn as used in `turn_seq_test.go`). If MCP can't resolve one, leave it empty, write that down in the phase summary, and continue.
* Size: `exec.go`, `toolseam/handler.go`, `llmdhost/host.go`, 3 call sites. `RunWithHandlers` keeps its signature, so its ≈17 dependents don't change.
* **Proof:** `internal/toolexec/executor_case_test.go` uses a stub `ExecutorDispatcher` and checks that a mutation executor tool on a read-only invocation is refused **without** calling the stub, and that a non-read-only call reaches it. Gate: `go test ./internal/toolexec/...`. `go build ./...` exits 0.

::konstruct{#phase-2-3}

### Phase 2.3 — Tenant-scoped listing through `toolmerge`

* Where `toolmerge.Request` is built (`internal/server/server.go:519`, `internal/mcpserver/server.go:732`, and any `ListSessionTools` path that uses it), append `ToolProviderHub.ExecutorDefinitions(session tenant)` to `ServerWide` (or `Session` when the profile lists them). That makes profile selection and `Disabled` work the same way as for other tools. Other tenants never see the name, and execution goes through the same `Effective` / `Allows` check, so it's refused there too.
* Size: 2 to 3 call sites in `internal/server` and `internal/mcpserver`.
* **Proof:** a test in `internal/server` registers an executor tool for tenant A on a test hub and shows it appears in tenant A's effective set and not in tenant B's. Tenant B naming it in `InvokeTool` is refused. Gate: `go test ./internal/server/... -run ExecutorTenant`.

::konstruct{#phase-2-4}

### Phase 2.4 — `Hub.CancelTurn` with tombstones, wired to ChatStream abort

* In the Hub, add a `(tenant, turn_id) → set[call_id]` index (filled by `Call` when `inv.TurnID != ""`) and `CancelTurn(tenantID, turnID) (cancelled int)`. It sends `ToolCallCancel` for each call, makes each waiter return "cancelled: turn cancelled", and adds a tombstone with TTL ≥ the largest `max_duration_seconds` (with lazy expiry). It's idempotent and logs at `Info` with counts. `Call` refuses tombstoned turns, and that counter is included in the log.
* Wiring: in `ChatStream`'s turn loop (`internal/server/server.go`), when the stream ctx ends before the turn completes (the client-abort path tested in `interrupt_order_test.go`), call `s.ToolProviderHub.CancelTurn(sc.TenantID, turnID)`. Check for nil, because `agent_loop_test.go` intentionally runs with a nil hub.
* Size: Hub plus one hook in `server.go`.
* **Proof:** `internal/toolprovider/cancel_turn_test.go` uses bufconn. A blocking call with `Origin = MCP` and a ctx that is **not** cancelled receives `ToolCallCancel` on the fake executor's wire after `CancelTurn`, and the waiter returns. A later `Call` for the same turn is refused without any frame sent. A second `CancelTurn` returns 0. Gate: `go test ./internal/toolprovider/... -run CancelTurn` and `go test ./internal/server/... -run Interrupt`.

::konstruct{#milestone-3}

## Milestone 3 — konstructd serves `run_command` over the executor stream

When this milestone is done, konstructd opens one executor stream per tenant it serves and declares `run_command`, with its inline registration removed in the same change. Calls run through the existing `toolseam` handler chain, and cancel kills the process group. **This code is in the konstructd repository, not this workspace.** Draft this milestone's blueprint against that tree once Milestones 1 and 2 have landed. The proof is an adapter test in konstructd that uses an in-process llmd (`llmdtest`), where `run_command` runs end to end and a cancel kills a `sleep` child.

::konstruct{#phase-3-1}

### Phase 3.1 — Adapter package

One stream per tenant, with the tenant scope in the context. `ExecutorHello` is built from the builtin tables and filtered to an allowlist (initially `run_command`). For each `ToolCall`, rebuild `toolseam.Invocation`, apply the deadline ctx, record the `call_id → cancel` map entry, and run the handler in a goroutine. `ToolCallCancel` triggers the stored cancel, and unknown ids are logged. Reconnect uses backoff, and `instance_id` is set once per process. Names rejected in the ack are logged at `Warn`.

* **Proof:** an adapter unit test with a fake llmd stream checks the cancel and unknown-id handling, and the konstructd package test passes.

::konstruct{#phase-3-2}

### Phase 3.2 — Move `run_command`

In one change, declare `run_command` through the adapter and remove its inline registration from the handler set. Otherwise rule 4 rejects the name, because the inline owner still holds it.

* **Proof:** an end-to-end test in konstructd with in-process llmd shows that the ack accepts `run_command` and that a chat-loop call returns command output. It also shows that cancelling the turn ends a `sleep 60` within a few seconds.

Later builtins (`task_started` / `phase_done`, then the rest) each follow the Phase 3.2 pattern as their own small change once `run_command` has soaked.

## Risks

* **The turn-cancel hook point is an inference.** The design assumes a turn-cancel path exists. In this repo, the only one is ChatStream ctx abort. If konstructd cancels turns some other way, add the `CancelTurn` call there as well.
* **MCP turn id availability** isn't confirmed. Without it, rule 12 covers chat-loop and `InvokeTool` calls, and covers MCP only through ctx propagation. Phase 2.2 records what it finds.
* **Inline-name conflict detection** depends on `Hub.InlineNames` being wired in `llmdhost`. If it's left nil, the ack accepts names that are held inline. Phase 2.2's wiring is the guard.