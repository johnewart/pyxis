// Package serverutil holds gRPC-server-adjacent helpers with no LlmServer
// receiver, shared across internal/server, graphserver and (later) wsserver.
//
// Audit of the phase-1 target files:
//
//   - scope.go / scope_wire.go: the tenant/scope resolver (llmd-tenant-id,
//     llmd-scope-id, optional llmd-principal-id) lives here; the legacy workspace-id helpers were removed.
//   - session_scope.go: moved. RequireSessionID lives here;
//     llmd/internal/server/session_scope.go keeps only the alias.
//   - tracked_context.go: not moved. injectTrackedContextForSend is a method
//     on *LlmServer touching TrackedDocs/SessionRepo — core session code, not
//     shared plumbing, and this phase does not touch the LlmServer struct.
//   - file_service_addr.go: removed. PeerResolver/FileService address
//     resolution was workspace-host plumbing that does not belong in llmd —
//     it existed only to feed the inline-builtin read_code tool, which
//     llmd does not own (see internal/server/builtin_tools.go). Deleted along
//     with the forwarders in llmd/internal/server/file_service_addr.go.
//   - embed_config.go: moved. ResolveEmbedConfig and its unexported proto-wire
//     helpers live here; llmd/internal/server/embed_config.go keeps only
//     the forwarding alias.
package serverutil
