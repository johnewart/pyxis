// Package toolprovider implements llmd's side of ToolProviderService.Connect,
// the bidirectional stream through which an external host runs tools for llmd.
// The stream carries one of two protocols, chosen by the first frame
// (Server.Connect); the full wire contract is documented on
// llmv1.ToolProviderToServer.
//
// # Executor protocol (preferred) — executor.go
//
// The host sends ExecutorHello declaring tools bound to
// ToolExecutionExecutor{executor_id}. Hub.HandleExecutorHello validates them
// (name required, no duplicates, no clash with a name already in the
// host's tool registry via Hub.InlineNames, no clash with another executor in the tenant, binding must
// match the hello) and registers the accepted ones per tenant, keyed by
// (tenant, executor_id). Registration is tenant-wide, so each ExecutorToolCall
// carries the calling session's tenant_id, scope_id, session_id and turn_id
// (from the toolseam.Invocation passed to Hub.Call).
//
// Hub.Call (the toolseam.ExecutorDispatcher) sends ExecutorToolCall to the
// owning stream and waits for the ToolCallResult, ctx cancellation, or the
// tool's max_duration_seconds (default 600s); on timeout/cancel it sends
// ToolCallCancel. Hub.CancelTurn sends ToolCallCancel{reason:"turn_cancelled"}
// to every in-flight call of a turn and tombstones it. Results larger than
// MaxResultBytes become error results.
//
// When an executor stream ends, its pending calls fail as disconnected and its
// tools stay registered without an owner (calls fail fast until it returns). A
// hello with a new instance_id for the same (tenant, executor_id) supersedes
// and drains the old stream.
//
// The client side is the public package toolexecutor.
//
// # Legacy provider protocol — hub.go
//
// ToolProviderHello registers a stream for an exact (tenant, scope);
// Hub.Invoke sends ToolExecutionInvoke and waits for ToolExecutionResult.
// llmd still uses this as the transport for delegate_to_client tools, from the
// chat loop (internal/server) and the MCP server (internal/mcpserver), when a
// provider stream is connected for the scope. New integrations should use the
// executor protocol.
//
// Both protocols require tenant metadata and Server.SharedSecret (set from
// llmdhost.Config.ToolProviderSecret; this package never reads the
// environment). Executors present it in metadata key ExecutorSecretMetadataKey,
// legacy providers in ToolProviderHello.
package toolprovider
