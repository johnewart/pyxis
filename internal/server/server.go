package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/johnewart/pyxis/agentpid"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/agentprofile"
	"github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"github.com/johnewart/pyxis/internal/llm/backend"
	"github.com/johnewart/pyxis/internal/llm/chatdebug"
	"github.com/johnewart/pyxis/internal/llm/common/toolcapture"
	"github.com/johnewart/pyxis/internal/llm/llmerr"
	"github.com/johnewart/pyxis/internal/llm/providerkind"
	"github.com/johnewart/pyxis/internal/llm/providers"
	"github.com/johnewart/pyxis/internal/llm/retry"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/sessionmanager"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/internal/toolargs"
	"github.com/johnewart/pyxis/internal/tooldelegate"
	"github.com/johnewart/pyxis/internal/toolexec"
	"github.com/johnewart/pyxis/internal/toolmerge"
	"github.com/johnewart/pyxis/internal/toolprovider"
	"github.com/johnewart/pyxis/internal/toolresult"
	"github.com/johnewart/pyxis/internal/toolsession"
	"github.com/johnewart/pyxis/internal/tooltimeout"
	"github.com/johnewart/pyxis/llmdlog"
	"github.com/johnewart/pyxis/policyhook"
	"github.com/johnewart/pyxis/systemprompt"
	"github.com/johnewart/pyxis/toolregistry"
	"github.com/johnewart/pyxis/toolseam"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
)

// LlmServer implements llmv1.LlmServiceServer.
type LlmServer struct {
	llmv1.UnimplementedLlmServiceServer

	// Tracer emits turn, round, provider and tool spans; nil disables them.
	Tracer trace.Tracer

	// noTools tracks turns running with TurnOptions.disable_tools (see
	// no_tools_turn.go). Lazily initialized.
	noToolsOnce sync.Once
	noTools     *noToolsTurns
	// LLM is used when Providers is nil (e.g. unit tests). llmd's own binary
	// (cmd/llmd/main.go) always sets Providers instead: a providers.Composite
	// backed by the database only (RegisterLlmProvider, per workspace) —
	// there is no providers file and never a process-wide default provider.
	LLM backend.Backend

	// Providers selects the backend per agent binding; nil falls back to LLM.
	Providers *providers.Composite

	// CheckHooks overrides the clients CheckProviderConnection uses for API
	// provider configs (tests inject fakes). Nil fields use the real ones.
	CheckHooks *ProviderCheckHooks

	// PolicyHooks are the policy hooks enforced on every provider call and
	// tool execution. Nil means no-op (everything allowed).
	// A *policyhook.Guard here is used as is (its fail mode and metrics apply).
	PolicyHooks policyhook.Hooks
	// DefaultCheckPolicy is the operator default check policy; nil means the
	// built-in default.
	DefaultCheckPolicy *policyhook.CheckPolicy

	// RetryPolicy is the retry policy for API-provider backends; nil uses
	// retry.DefaultPolicy. Per-call overrides come from CallOptions.
	RetryPolicy *retry.Policy

	// Optional: server-wide built-in tools merged with per-agent tools (via mergeToolDefs).
	Tools *toolregistry.Registry

	// Handlers is the explicit inline-builtin handler set this server dispatches
	// through (InstallBuiltinTools registers into it). Servers own their set —
	// never toolexec.DefaultHandlers — so two independently constructed
	// LlmServers never share registration. Falls back to a fresh empty set if
	// left nil, so a caller that forgets to wire one fails closed (unknown
	// tool) rather than leaking into process-wide state.
	Handlers *toolexec.Handlers

	// Required for Chat / Generate / ChatStream / GetSessionHistory / tool registration: one managed agent per agent_id.
	SessionRuntimes *sessionmanager.Manager

	// ToolWaits correlates SubmitToolResults with delegate_to_client tool execution during ChatStream. If nil, delegate tools fail on stream.
	ToolWaits *tooldelegate.Registry

	// ToolProviderHub routes delegate_to_client execution to an attached ToolProviderService bidi stream (e.g. a client app) when registered for the workspace.
	ToolProviderHub *toolprovider.Hub

	// ProviderSessionSync persists stateful-backend transcript watermarks (Cursor --resume). Nil disables incremental delivery for stateful providers.
	ProviderSessionSync *store.ProviderSessionSyncRepo

	// EmbeddingProviders persists embedding backend configs, one table per kind (a client registers via RegisterUserEmbeddingProvider). Keyed by tenant and scope; reads fall back to the tenant-wide row.
	EmbeddingProviders *store.EmbeddingProviderRepo

	// TurnMetadata optionally persists per-turn token accounting stats.
	// When nil, usage is still streamed to the client via EvAssistantTurnFinished.Usage but not stored.
	TurnMetadata *store.TurnMetadataRepo

	// Usage optionally persists one usage row per provider call (tenant, scope,
	// principal, provider, model, tokens, cost, outcome) and serves QueryUsage.
	// Nil disables recording; QueryUsage then reports FailedPrecondition.
	Usage *store.UsageRepo

	// ToolUsage optionally persists one row per tool execution (all dispatch
	// paths). Nil disables recording.
	ToolUsage *store.ToolUsageRepo

	// ToolResultSidecar optionally persists original uncompressed tool results for expand_tool_result.
	// When nil, compression is disabled regardless of environment settings.
	ToolResultSidecar *store.ToolResultSidecarRepo

	// Attachments persists session UI-artifact rows (ask cards, embedded build runs). It is the only
	// read/write path for those rows — a client reaches them over gRPC and never opens llmd's SQLite.
	Attachments *store.ChatAttachmentRepo

	// SessionRepo manages session persistence (including compression settings).
	SessionRepo *store.SessionRepo
	// AgentProfileRepo is the per-workspace (dynamic) agent profile tier
	// (llmd_agent_profiles). Optional: nil means only static profiles are
	// served and CreateAgentProfile/UpdateAgentProfile/DeleteAgentProfile
	// fail closed with FailedPrecondition. When AgentProfiles is not injected,
	// the lazy agentProfiles accessor builds the resolver over this repo.
	AgentProfileRepo *store.AgentProfileRepo
	// DecorationsRepo persists typed turn/tool-result decorations
	// (llmd_turn_decorations; optional; nil disables it).
	DecorationsRepo *store.TurnDecorationRepo

	// TrackedDocs persists immutable tracked-document version snapshots (plan/design/idea
	// bodies attached to turns). Send-time assembly injects them into outbound model
	// messages; the persisted user content stays raw. Nil disables tracked documents.
	TrackedDocs *store.TrackedDocumentRepo

	// SessionCache optionally persists the generic per-session K/V used by tools
	// (e.g. read_code line-range coverage). Only used here for cascade cleanup on
	// session delete; the SessionCacheService serves the get/set/delete RPCs.
	SessionCache *store.SessionCacheRepo

	// Sessions owns per-session tool-result handling (compression settings, dedup
	// state, sidecar persistence). The SAME instance is shared with the MCP
	// delegated-tool path so both surfaces flow through one pipeline. When not
	// injected (e.g. unit tests), it is lazily built from ToolResultSidecar +
	// SessionRepo via sessionService.
	Sessions     *toolsession.Service
	sessionsOnce sync.Once

	// AgentProfiles resolves agent profiles for the AgentProfile RPCs and
	// CreateSessionFromAgentProfile: the static (code-registered) tier plus an
	// optional per-workspace DB tier. Nil means static-only — the lazy
	// agentProfiles accessor falls back to a resolver over agentprofile.Default
	// with no dynamic store, so hosts that never wire profiles still serve the
	// static registrations and the CRUD RPCs fail closed (FailedPrecondition).
	AgentProfiles     *agentprofile.Resolver
	agentProfilesOnce sync.Once

	// SessionEvents persists ConversationEvent rows for replay and subscription.
	// When set, every ConversationStreamFrame sent via ChatStream is persisted with seq.
	SessionEvents *store.SessionEventRepo

	// Storage reports database use and compacts it (GetStorageStats, Compact).
	Storage *store.StorageRepo
	compact compactState

	// TurnRegister issues gap-free, monotonically increasing turn_seq numbers per
	// session. Advanced exactly once per ChatStream turn-start (EvAssistantTurnStarted)
	// so the outbound frame and persisted llmd_session_turns row agree. Nil disables
	// turn_seq stamping (turn_seq stays unset on the wire).
	TurnRegister *store.TurnRegister

	// SessionDrafts persists the single unsent composer draft per session, so a
	// reload or crash does not lose what someone had typed. Nil disables drafts
	// (the RPCs then report FailedPrecondition rather than silently no-op'ing).
	SessionDrafts *store.SessionDraftRepo
	// SessionQueue persists the per-session message queue: ordering, the pause
	// flag and the claim/lease guarantees that make competing drainers safe. llmd
	// drains it itself (see queue_drainer.go) except on sessions with
	// client-owned tools, which are left to client drainers. Nil disables queueing.
	SessionQueue *store.SessionQueueRepo
	// DisableServerQueueDrain stops llmd from draining queues itself, leaving
	// all draining to clients. For tests that need queued entries to stay put.
	DisableServerQueueDrain bool
	queueDrain              *queueDrainer
	queueDrainOnce          sync.Once

	// turnGates serializes assistant turns per session so one turn's commit
	// always precedes the next turn's history read (see turnGate for why the
	// interrupt flow depends on that). Lazily created: LlmServer is assembled as
	// a struct literal everywhere, so there is no constructor to initialize it.
	turnGates     *turnGate
	turnGatesOnce sync.Once

	// BaseContext is the server lifetime context. Assistant turns run on a
	// context derived from it (NOT from the client request), so a client going
	// away never cancels a turn; ending BaseContext (host shutdown) does. Nil
	// means context.Background(). llmdhost sets it from Config.Context.
	BaseContext context.Context

	// turns tracks in-flight turns per session for CancelTurn and shutdown.
	// Lazily created for the same struct-literal reason as turnGates.
	turns     *turnRunner
	turnsOnce sync.Once
}

// turnRunner returns the in-flight turn registry, creating it on first use.
func (s *LlmServer) turnRunner() *turnRunner {
	s.turnsOnce.Do(func() { s.turns = newTurnRunner() })
	return s.turns
}

// WaitForTurns blocks until every in-flight turn has committed, or timeout.
// Hosts call it after cancelling BaseContext and before closing databases, so
// the partials salvaged on shutdown are actually persisted.
func (s *LlmServer) WaitForTurns(timeout time.Duration) bool {
	return s.turnRunner().wait(timeout)
}

// CancelTurn is the explicit Stop for a session's running turn. Client
// disconnect never cancels a turn; this is the only way to cut one short. The
// turn salvages its streamed-so-far partial and commits it with
// finish_reason = req.reason (default "cancelled").
func (s *LlmServer) CancelTurn(ctx context.Context, req *llmv1.CancelTurnRequest) (*llmv1.CancelTurnResponse, error) {
	aid, err := requireSessionID(req.GetSessionId())
	if err != nil {
		return nil, err
	}
	// Scope check: a caller may only cancel turns of sessions it can see.
	if err := s.ensureKnownSession(ctx, aid); err != nil {
		return nil, err
	}
	sid := strings.TrimSpace(req.GetSessionId())
	ok := s.turnRunner().cancel(sid, req.GetReason())
	llmdlog.Stdout("server", llmdlog.LevelInfo, "CancelTurn: session=%q reason=%q found_running_turn=%v", sid, req.GetReason(), ok)
	return &llmv1.CancelTurnResponse{Cancelled: ok}, nil
}

// sessionTurnGate returns the per-session turn gate, creating it on first use.
func (s *LlmServer) sessionTurnGate() *turnGate {
	s.turnGatesOnce.Do(func() { s.turnGates = newTurnGate() })
	return s.turnGates
}

// toDecorations converts the wire ToolResultAttachment list a delegated tool
// attached to its result into toolsession's proto-free Decoration type. llmd
// never interprets Kind or URI; this is a pure field-name mapping.
func toDecorations(atts []*llmv1.ToolResultAttachment) []toolsession.Decoration {
	if len(atts) == 0 {
		return nil
	}
	out := make([]toolsession.Decoration, len(atts))
	for i, a := range atts {
		out[i] = toolsession.Decoration{Kind: a.GetKind(), URI: a.GetUri()}
	}
	return out
}

// Guardrail: every LlmService RPC in llm.proto must be implemented on *LlmServer.
var _ llmv1.LlmServiceServer = (*LlmServer)(nil)

func (s *LlmServer) registeredTools() []*llmv1.ToolDefinition {
	if s.Tools == nil {
		return nil
	}
	return s.Tools.Snapshot()
}

// ServerWideToolDefinitions returns built-in tools merged into every agent tool list (same source as gRPC ListTools server side).
func (s *LlmServer) ServerWideToolDefinitions() []*llmv1.ToolDefinition {
	return s.registeredTools()
}

func mergeToolDefs(server, agent []*llmv1.ToolDefinition) []*llmv1.ToolDefinition {
	return toolmerge.MergeDefinitions(server, agent)
}

func toolDefByName(defs []*llmv1.ToolDefinition) map[string]*llmv1.ToolDefinition {
	m := make(map[string]*llmv1.ToolDefinition)
	for _, d := range defs {
		if d == nil {
			continue
		}
		n := strings.TrimSpace(d.GetName())
		if n == "" {
			continue
		}
		m[n] = d
	}
	return m
}

func (s *LlmServer) ensureKnownSession(ctx context.Context, sessionID string) error {
	sid := strings.TrimSpace(sessionID)
	if sid == "" {
		return status.Error(codes.InvalidArgument, "session_id is required")
	}
	if s.SessionRuntimes == nil {
		return status.Error(codes.FailedPrecondition, "session manager not configured")
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return err
	}
	if err := s.SessionRuntimes.EnsureLoaded(ctx, sc, sid); err != nil {
		if errors.Is(err, sessionmanager.ErrUnknownSession) {
			return status.Errorf(codes.NotFound, "unknown session_id %q", sid)
		}
		return status.Errorf(codes.Internal, "load session %q: %v", sid, err)
	}
	ma, ok := s.SessionRuntimes.Get(sc, sid)
	if !ok {
		return status.Errorf(codes.NotFound, "unknown session_id %q", sid)
	}
	// Normalized: a runtime's scope never carries a principal, so a request
	// that does would otherwise never match the session it just created.
	if ma.Scope != sessionmanager.NormalizeScope(sc) {
		return status.Errorf(codes.NotFound, "unknown session_id %q", sid)
	}
	return nil
}

// logLLMRequest records every outbound LLM request at info level: routing
// metadata only (never prompt or message text). model is the model llmd
// requested; CLI providers may map it further and log the final value.
func logLLMRequest(providerID, model, sessionID string, tools []string) {
	llmdlog.Stdout("llm", llmdlog.LevelInfo,
		"llm request => provider_id=%q model_id=%q session_id=%q tools=%v",
		providerID, model, sessionID, tools)
}

func cloneCallOptions(opt *llmv1.CallOptions) *llmv1.CallOptions {
	if opt == nil {
		return &llmv1.CallOptions{}
	}
	return proto.Clone(opt).(*llmv1.CallOptions)
}

func (s *LlmServer) effectiveCallOptions(ctx context.Context, agentID string, opt *llmv1.CallOptions) *llmv1.CallOptions {
	out := cloneCallOptions(opt)
	if strings.TrimSpace(out.GetModel()) != "" {
		return out
	}
	// A model chosen for THIS session (model selector → UpdateSessionLlm) wins
	// over the provider default. Provider and model are bound together there,
	// so when the session has a provider bound, its model belongs to that
	// provider and cannot be a stale cross-provider leftover.
	if s.SessionRuntimes != nil {
		if ma, ok := s.runtimeFor(ctx, strings.TrimSpace(agentID)); ok {
			if strings.TrimSpace(ma.ProviderID) != "" {
				if dm := strings.TrimSpace(ma.DefaultModel); dm != "" {
					out.Model = dm
					return out
				}
			}
		}
	}
	// Prefer the resolved provider's default_model so "Default" tracks the current provider; avoid a stale
	// per-agent default from a previous provider (e.g. Bedrock id after switching to Cursor).
	if s.Providers != nil && s.SessionRuntimes != nil {
		if ma, ok := s.runtimeFor(ctx, strings.TrimSpace(agentID)); ok {
			pid := strings.TrimSpace(s.Providers.DefaultID())
			if p := strings.TrimSpace(ma.ProviderID); p != "" {
				pid = p
			}
			if e, ok := s.Providers.Get(ctx, s.scopeOrZero(ctx), pid); ok && e != nil {
				if dm := strings.TrimSpace(e.DefaultModel); dm != "" {
					out.Model = dm
					return out
				}
			}
		}
	}
	if s.SessionRuntimes == nil {
		return out
	}
	ma, ok := s.runtimeFor(ctx, strings.TrimSpace(agentID))
	if !ok {
		return out
	}
	if dm := strings.TrimSpace(ma.DefaultModel); dm != "" {
		out.Model = dm
	}
	return out
}

func systemMessagesFingerprint(msgs []*llmv1.Message) string {
	h := sha256.New()
	for _, m := range msgs {
		if m == nil {
			continue
		}
		if strings.TrimSpace(m.GetRole()) != "system" {
			continue
		}
		h.Write([]byte(m.GetContent()))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}

func (s *LlmServer) resolvedProviderID(ctx context.Context, agentID string) string {
	if s.Providers == nil || s.SessionRuntimes == nil {
		return ""
	}
	pid := strings.TrimSpace(s.Providers.DefaultID())
	if ma, ok := s.runtimeFor(ctx, strings.TrimSpace(agentID)); ok {
		if p := strings.TrimSpace(ma.ProviderID); p != "" {
			pid = p
		}
	}
	return strings.TrimSpace(pid)
}

func (s *LlmServer) providerStatefulHistory(ctx context.Context, agentID string) bool {
	if s.Providers == nil {
		return false
	}
	pid := strings.TrimSpace(s.Providers.DefaultID())
	if s.SessionRuntimes != nil {
		if ma, ok := s.runtimeFor(ctx, strings.TrimSpace(agentID)); ok {
			if p := strings.TrimSpace(ma.ProviderID); p != "" {
				pid = p
			}
		}
	}
	e, ok := s.Providers.Get(ctx, s.scopeOrZero(ctx), pid)
	if !ok || e == nil {
		return false
	}
	return e.Kind.StatefulProviderHistory()
}

// retryPolicy returns the configured retry policy, or the default when
// RetryPolicy is nil.
func (s *LlmServer) retryPolicy() retry.Policy {
	if s.RetryPolicy != nil {
		return *s.RetryPolicy
	}
	return retry.DefaultPolicy()
}

// backendFor resolves the provider backend for an agent. Every chat llmd
// makes goes through here, which is why chat capture is attached at this
// point: one wrap covers Chat, Generate and ChatStream for every provider.
// chatdebug.Wrap is a no-op returning be unchanged when chat debugging is
// off, so the normal path is untouched.
func (s *LlmServer) backendFor(ctx context.Context, agentID string) (backend.Backend, error) {
	aid := strings.TrimSpace(agentID)
	if s.Providers != nil {
		pid := strings.TrimSpace(s.Providers.DefaultID())
		if s.SessionRuntimes != nil {
			if ma, ok := s.runtimeFor(ctx, aid); ok {
				if p := strings.TrimSpace(ma.ProviderID); p != "" {
					pid = p
				}
			}
		}
		sc, err := s.scope(ctx)
		if err != nil {
			return nil, err
		}
		be, ok := s.Providers.Backend(ctx, sc, pid)
		if !ok || be == nil {
			return nil, status.Errorf(codes.NotFound, "unknown or unavailable llm provider %q", pid)
		}
		// Retry sits inside the chat-debug recorder so every attempt is
		// recorded. Only remote API kinds are wrapped; CLI and local
		// providers are untouched.
		if e, found := s.Providers.Get(ctx, sc, pid); found && e != nil && e.Kind.IsAPIBacked() {
			be = retry.Wrap(be, s.retryPolicy(), pid)
		}
		return chatdebug.Wrap(be, chatdebug.Meta{ProviderID: pid, AgentID: aid}), nil
	}
	if s.LLM == nil {
		return nil, status.Error(codes.FailedPrecondition, "LLM backend not configured")
	}
	return chatdebug.Wrap(s.LLM, chatdebug.Meta{AgentID: aid}), nil
}

func (s *LlmServer) validateAgentLlmBinding(ctx context.Context, providerID string) error {
	pid := strings.TrimSpace(providerID)
	if pid == "" {
		return nil
	}
	if s.Providers == nil {
		return status.Error(codes.FailedPrecondition, "LLM provider registry not configured (Providers is nil)")
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return err
	}
	if _, ok := s.Providers.Get(ctx, sc, pid); !ok {
		return status.Errorf(codes.InvalidArgument, "unknown llm_provider_id %q", pid)
	}
	return nil
}

// reconcileSessionLlmBinding auto-binds a ChatStream turn's provider/model
// onto the session when the caller's CallOptions name one that differs from
// what the session currently has bound. This is what lets a client just send
// a message with a different model/provider instead of calling
// UpdateSessionLlm as a separate round trip first: an unbound session's
// first message sets its binding, and any later message naming a different
// provider or model rebinds it from there.
//
// It runs through sessionmanager.Manager.UpdateLlm — the exact same path
// UpdateSessionLlm uses — so this performs the identical persistence
// (ma.ProviderID/DefaultModel + the session row) and leaves the existing
// stateful/non-stateful history reconciliation (ProviderSessionSync,
// resume-id/watermark handling in ChatStream below) to apply exactly as it
// already does when the binding changes via the explicit RPC. No new
// reconciliation mechanism, no special-casing: the session's binding simply
// changes before the rest of ChatStream reads it.
func (s *LlmServer) reconcileSessionLlmBinding(ctx context.Context, aid string, opt *llmv1.CallOptions) error {
	if opt == nil || s.SessionRuntimes == nil {
		return nil
	}
	reqProvider := strings.TrimSpace(opt.GetProviderId())
	reqModel := strings.TrimSpace(opt.GetModel())
	if reqProvider == "" && reqModel == "" {
		return nil
	}
	ma, ok := s.runtimeFor(ctx, strings.TrimSpace(aid))
	if !ok {
		// ensureKnownSession already validated the agent exists and belongs to
		// this workspace immediately before this call; nothing to reconcile
		// against if it has since vanished.
		return nil
	}
	var providerUpdate, modelUpdate *string
	if reqProvider != "" && reqProvider != strings.TrimSpace(ma.ProviderID) {
		// Validate before rebinding, same as EnsureSession/UpdateSessionLlm:
		// an inline switch to an unknown provider must fail the turn, not
		// silently rebind to something backendFor will reject later anyway.
		if err := s.validateAgentLlmBinding(ctx, reqProvider); err != nil {
			return err
		}
		providerUpdate = &reqProvider
	}
	if reqModel != "" && reqModel != strings.TrimSpace(ma.DefaultModel) {
		modelUpdate = &reqModel
	}
	if providerUpdate == nil && modelUpdate == nil {
		return nil
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return err
	}
	if err := s.SessionRuntimes.UpdateLlm(ctx, sc, aid, providerUpdate, modelUpdate); err != nil {
		return status.Errorf(codes.InvalidArgument, "%v", err)
	}
	return nil
}

func kindToProto(k providerkind.Kind) llmv1.LlmProviderType {
	switch k {
	case providerkind.OpenAI:
		return llmv1.LlmProviderType_LLM_PROVIDER_TYPE_OPENAI
	case providerkind.Ollama:
		return llmv1.LlmProviderType_LLM_PROVIDER_TYPE_OLLAMA
	case providerkind.Anthropic:
		return llmv1.LlmProviderType_LLM_PROVIDER_TYPE_ANTHROPIC
	case providerkind.CursorCLI:
		return llmv1.LlmProviderType_LLM_PROVIDER_TYPE_CURSOR_CLI
	case providerkind.ClaudeCodeV2:
		return llmv1.LlmProviderType_LLM_PROVIDER_TYPE_CLAUDE_CODE_V2
	case providerkind.Bedrock:
		return llmv1.LlmProviderType_LLM_PROVIDER_TYPE_BEDROCK
	case providerkind.Gemini:
		return llmv1.LlmProviderType_LLM_PROVIDER_TYPE_GEMINI
	case providerkind.OpenRouter:
		return llmv1.LlmProviderType_LLM_PROVIDER_TYPE_OPENROUTER
	default:
		return llmv1.LlmProviderType_LLM_PROVIDER_TYPE_UNSPECIFIED
	}
}

// ctxWithCursorAgentRun attaches tenant + agent + session for Cursor CLI PID → MCP bridge registration.
func (s *LlmServer) ctxWithCursorAgentRun(ctx context.Context, agentID, sessionID string, readOnly bool) context.Context {
	sc, err := s.scope(ctx)
	if err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn,
			"ctxWithCursorAgentRun: session=%q agent=%q: no scope, skipping PID run details: %v", sessionID, agentID, err)
		return ctx
	}
	return agentpid.WithRunDetails(ctx, agentpid.Run{
		Scope:             sc,
		AgentID:           strings.TrimSpace(agentID),
		SessionID:         strings.TrimSpace(sessionID),
		ClientWorkspaceID: sc.ScopeID,
		ReadOnly:          readOnly,
	})
}

// toolExecutionStarted builds the ToolExecutionStarted event for one tool
// call, carrying the arguments so a UI can describe what the call is doing
// ("Reading lines 15-200 of foo.go") rather than just naming the tool.
//
// Native backends also emit EvToolCallStreamFinished with the same arguments,
// so this is redundant for them — deliberately. MCP-dispatched tools have no
// such frame, and a UI should not need to know which transport produced a tool
// call in order to find its arguments.
func toolExecutionStarted(turnID string, tc *llmv1.ToolCall) *llmv1.EvToolExecutionStarted {
	started := &llmv1.EvToolExecutionStarted{
		TurnId:     turnID,
		ToolCallId: tc.GetId(),
		Name:       tc.GetName(),
	}
	if args, truncated := toolargs.ForDisplay(tc.GetArgumentsJson()); args != "" {
		started.ArgumentsJson = proto.String(args)
		if truncated {
			started.ArgumentsTruncated = proto.Bool(true)
		}
	}
	return started
}

// effectiveToolRequest builds the toolmerge.Request describing what session
// agentID may see and call. This is the ONE place the inputs are assembled:
// every advertisement path (the provider tool list, ListSessionTools, MCP
// tools/list) and every execution path (the ChatStream tool loop, InvokeTool)
// must derive its answer from this, or a tool hidden on one path stays callable
// on another.
//
// An unknown/unloaded session gets the bare server-wide merge with no policy —
// there is no session to carry exclusions, and failing closed here would strip
// tools from every caller that legitimately has no session (e.g. a plain
// InvokeTool with no session_id).
func (s *LlmServer) effectiveToolRequest(ctx context.Context, agentID string) toolmerge.Request {
	aid := strings.TrimSpace(agentID)
	req := toolmerge.Request{ServerWide: s.registeredTools()}
	var ma *sessionmanager.SessionRuntime
	ok := false
	if s.SessionRuntimes != nil {
		ma, ok = s.runtimeFor(ctx, aid)
	}
	// Executor tools are tenant-scoped: append only the session tenant's
	// definitions, never into the global registry.
	tenantID := ""
	if ok {
		tenantID = ma.Scope.TenantID
	} else if sc, err := serverutil.ScopeFromContext(ctx); err != nil {
		llmdlog.Stdout("server", llmdlog.LevelDebug,
			"effectiveToolRequest: no scope for executor tools: agent=%q err=%v", aid, err)
	} else {
		tenantID = sc.TenantID
	}
	if tenantID != "" {
		req.ServerWide = append(req.ServerWide, s.ToolProviderHub.ExecutorDefinitions(tenantID)...)
	}
	if !ok {
		return req
	}
	// DisabledToolSet returns a copy, so the runtime's live map is never
	// handed to the merge.
	req.Session = ma.Tools.Snapshot()
	req.Disabled = ma.DisabledToolSet()
	req.Policy = toolmerge.ParsePolicy(ma.ToolPolicyName())
	return req
}

// mergedToolDefinitions is the session's effective tool set — what the model is
// offered, what ListSessionTools reports, and what dispatch will accept.
func (s *LlmServer) mergedToolDefinitions(ctx context.Context, agentID string) []*llmv1.ToolDefinition {
	return toolmerge.EffectiveDefinitions(s.effectiveToolRequest(ctx, agentID))
}

// effectiveToolProvenance is mergedToolDefinitions with each tool's origin
// (core / session-registered / server-wide), so "why does this agent have X"
// is answerable without reading code.
func (s *LlmServer) effectiveToolProvenance(ctx context.Context, agentID string) []toolmerge.Entry {
	return toolmerge.Effective(s.effectiveToolRequest(ctx, agentID))
}

// toolAllowedForSession reports whether sessionID may execute name. Callers
// MUST consult this before running a handler: advertisement is not
// enforcement, and a model can hallucinate a name, a replayed frame can carry
// one, and a hand-crafted RPC can name anything at all.
func (s *LlmServer) toolAllowedForSession(ctx context.Context, sessionID, name string) bool {
	if s.toolsDisabledForTurnCtx(ctx, sessionID) {
		return false
	}
	return toolmerge.Allows(s.effectiveToolRequest(ctx, sessionID), name)
}

func (s *LlmServer) mergedToolsStream(ctx context.Context, agentID string, readOnly bool) func() []*llmv1.ToolDefinition {
	return func() []*llmv1.ToolDefinition {
		tools := toolmerge.StripForProvider(s.mergedToolDefinitions(ctx, agentID))
		if readOnly {
			tools = toolmerge.FilterForReadOnly(tools)
		}
		return tools
	}
}

// offeredToolDefinitions is the same set mergedToolsStream offers the provider
// (read-only filter included) but UNSTRIPPED, so execution metadata such as
// executor.max_duration_seconds is still present. Server-side use only: never
// hand these to a provider.
// defaultFinalRoundInstruction is sent on the wrap-up round when final_round is
// enabled but carries no instruction.
const defaultFinalRoundInstruction = "You have used all available tool rounds. Do not call any tools; reply to the user now with a final text answer."

func (s *LlmServer) offeredToolDefinitions(ctx context.Context, agentID string, readOnly bool) []*llmv1.ToolDefinition {
	defs := s.mergedToolDefinitions(ctx, agentID)
	if readOnly {
		defs = toolmerge.FilterForReadOnly(defs)
	}
	return defs
}

func (s *LlmServer) SubmitToolResults(ctx context.Context, req *llmv1.SubmitToolResultsRequest) (*llmv1.SubmitToolResultsResponse, error) {
	aid := strings.TrimSpace(req.GetSessionId())
	if aid == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is required")
	}
	nonce := strings.TrimSpace(req.GetClientWaitNonce())
	if nonce == "" {
		return nil, status.Error(codes.InvalidArgument, "client_wait_nonce is required")
	}
	if strings.TrimSpace(req.GetToolCallId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "tool_call_id is required")
	}
	if err := s.ensureKnownSession(ctx, aid); err != nil {
		return nil, err
	}
	if s.ToolWaits == nil {
		return nil, status.Error(codes.FailedPrecondition, "client tool delegation is not enabled on this server")
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.ToolWaits.Submit(nonce, aid, req.GetToolCallId(), sc, req.GetContent(), req.GetIsError(), req.GetEndsTurn()); err != nil {
		switch {
		case errors.Is(err, tooldelegate.ErrNotFound):
			return nil, status.Errorf(codes.NotFound, "%v", err)
		case errors.Is(err, tooldelegate.ErrAgentMismatch), errors.Is(err, tooldelegate.ErrToolCallMismatch), errors.Is(err, tooldelegate.ErrScopeMismatch):
			return nil, status.Errorf(codes.PermissionDenied, "%v", err)
		case errors.Is(err, tooldelegate.ErrDuplicate):
			return nil, status.Errorf(codes.AlreadyExists, "%v", err)
		default:
			return nil, status.Errorf(codes.Internal, "%v", err)
		}
	}
	return &llmv1.SubmitToolResultsResponse{}, nil
}

// GenerateOnce runs a stateless single completion: no agent, no session, no
// persistence, no tools. The caller supplies the provider explicitly (model via
// options.model). Enabled-model validation is intentionally skipped so the
// onboarding provider probe can exercise a model that is not yet enabled.
func (s *LlmServer) GenerateOnce(ctx context.Context, req *llmv1.GenerateOnceRequest) (*llmv1.AssistantReply, error) {
	if s.Providers == nil {
		return nil, status.Error(codes.FailedPrecondition, "LLM provider registry not configured (Providers is nil)")
	}
	if len(req.GetPromptParts()) > 0 && strings.TrimSpace(req.GetPrompt()) != "" {
		return nil, status.Error(codes.InvalidArgument, "set either prompt or prompt_parts, not both")
	}
	pid := strings.TrimSpace(req.GetProviderId())
	if pid == "" {
		pid = strings.TrimSpace(s.Providers.DefaultID())
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	be, ok := s.Providers.Backend(ctx, sc, pid)
	if !ok || be == nil {
		return nil, status.Errorf(codes.NotFound, "unknown or unavailable llm provider %q", pid)
	}
	opt := cloneCallOptions(req.GetOptions())
	if strings.TrimSpace(opt.GetModel()) == "" {
		if e, ok := s.Providers.Get(ctx, sc, pid); ok && e != nil {
			if dm := strings.TrimSpace(e.DefaultModel); dm != "" {
				opt.Model = dm
			}
		}
	}
	debugLogCallOptions("GenerateOnce", "", "", req.GetOptions(), opt)
	logLLMRequest(pid, opt.GetModel(), "", nil)
	genStarted := time.Now()
	var reply *llmv1.AssistantReply
	var gerr error
	if len(req.GetPromptParts()) > 0 {
		reply, gerr = backend.GenerateWithParts(ctx, be, req.GetSystemPrompt(), req.GetPromptParts(), opt, nil)
	} else {
		reply, gerr = be.Generate(ctx, req.GetSystemPrompt(), req.GetPrompt(), opt, nil)
	}
	s.recordUsage(ctx, sc, usageCall{
		rpc: "GenerateOnce", providerID: pid, providerType: kindToProto(providerKindFromComposite(s.Providers, ctx, sc, pid)),
		model: opt.GetModel(), started: genStarted, reply: reply, err: gerr,
	})
	if gerr != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn, "GenerateOnce: backend generate failed: provider=%q err=%v", pid, gerr)
		return nil, llmerr.ToStatus(gerr, kindToProto(providerKindFromComposite(s.Providers, ctx, sc, pid)))
	}
	return reply, nil
}

// preparedComplete is the validated, backend-ready form of a CompleteRequest.
type preparedComplete struct {
	sc         serverutil.Scope
	providerID string
	backend    backend.Backend
	provider   llmv1.LlmProviderType
	opt        *llmv1.CallOptions
	messages   []*llmv1.Message
	tools      []*llmv1.ToolDefinition
}

// prepareComplete validates a CompleteRequest and resolves its backend, model
// and message list. It reads no session state and writes nothing. Messages are
// passed through in order; a system prompt, when given, is prepended as a
// role=system message. Tools are forwarded as definitions only.
func (s *LlmServer) prepareComplete(ctx context.Context, rpc string, req *llmv1.CompleteRequest) (*preparedComplete, error) {
	// The tenant is checked first so an unauthenticated caller learns nothing
	// about providers or models.
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	if s.Providers == nil {
		return nil, status.Error(codes.FailedPrecondition, "LLM provider registry not configured (Providers is nil)")
	}
	if len(req.GetMessages()) == 0 {
		return nil, status.Error(codes.InvalidArgument, "messages is required")
	}
	pid := strings.TrimSpace(req.GetProviderId())
	if pid == "" {
		pid = strings.TrimSpace(s.Providers.DefaultID())
	}
	entry, ok := s.Providers.Get(ctx, sc, pid)
	if !ok || entry == nil || entry.Backend == nil {
		return nil, status.Errorf(codes.NotFound, "unknown or unavailable llm provider %q", pid)
	}
	opt := cloneCallOptions(req.GetOptions())
	model := strings.TrimSpace(opt.GetModel())
	if model == "" {
		model = strings.TrimSpace(entry.DefaultModel)
		opt.Model = model
	}
	if model == "" {
		return nil, status.Errorf(codes.InvalidArgument, "model is required: provider %q has no default model", pid)
	}
	enabled := false
	for _, m := range entry.SnapshotCatalog() {
		if m.ModelID == model {
			enabled = true
			break
		}
	}
	if !enabled {
		return nil, status.Errorf(codes.NotFound, "model %q is not enabled for llm provider %q", model, pid)
	}
	msgs := make([]*llmv1.Message, 0, len(req.GetMessages())+1)
	hasSystem := false
	for _, m := range req.GetMessages() {
		if m.GetRole() == "system" {
			hasSystem = true
			break
		}
	}
	if strings.TrimSpace(req.GetSystem()) != "" && !hasSystem {
		msgs = append(msgs, &llmv1.Message{Role: "system", Content: req.GetSystem()})
	}
	msgs = append(msgs, req.GetMessages()...)
	debugLogCallOptions(rpc, "", "", req.GetOptions(), opt)
	toolNames := make([]string, 0, len(req.GetTools()))
	for _, t := range req.GetTools() {
		toolNames = append(toolNames, t.GetName())
	}
	logLLMRequest(pid, model, "", toolNames)
	return &preparedComplete{sc: sc, providerID: pid, backend: entry.Backend, provider: kindToProto(providerKindFromComposite(s.Providers, ctx, sc, pid)), opt: opt, messages: msgs, tools: req.GetTools()}, nil
}

// Complete runs a stateless completion against a backend. It touches no
// session, history, turn or event store, and never executes the tool
// definitions it is given: tool calls come back in the reply.
func (s *LlmServer) Complete(ctx context.Context, req *llmv1.CompleteRequest) (*llmv1.CompleteResponse, error) {
	p, err := s.prepareComplete(ctx, "Complete", req)
	if err != nil {
		return nil, err
	}
	if err := s.applyCompletePolicy(ctx, p); err != nil {
		return nil, err
	}
	started := time.Now()
	pctx, pspan := s.startSpan(ctx, "provider.chat_stream",
		attribute.String("llmd.model", p.opt.GetModel()),
		attribute.String("llmd.provider.id", p.providerID),
		attribute.String("llmd.provider.type", p.provider.String()))
	reply, err := p.backend.Chat(pctx, p.messages, p.opt, p.tools)
	endSpan(pspan, ctx, err)
	s.recordUsage(ctx, p.sc, usageCall{
		rpc: "Complete", providerID: p.providerID, providerType: p.provider,
		model: p.opt.GetModel(), started: started, reply: reply, err: err,
	})
	if err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn, "Complete: backend chat failed: %v", err)
		return nil, llmerr.ToStatus(err, p.provider)
	}
	return &llmv1.CompleteResponse{Reply: reply}, nil
}

// CompleteStream is the streaming form of Complete: content and thinking
// deltas and tool calls are forwarded as they arrive, followed by one final
// reply frame carrying the full reply and token usage.
func (s *LlmServer) CompleteStream(req *llmv1.CompleteRequest, stream llmv1.LlmService_CompleteStreamServer) error {
	ctx := stream.Context()
	p, err := s.prepareComplete(ctx, "CompleteStream", req)
	if err != nil {
		return err
	}
	if err := s.applyCompletePolicy(ctx, p); err != nil {
		return err
	}
	var (
		content  strings.Builder
		thinking strings.Builder
		calls    []*llmv1.ToolCall
		final    *llmv1.AssistantReply
		usage    *llmv1.TokenUsage
		finish   string
		replyID  string
		failure  string
	)
	emit := func(f *llmv1.ConversationStreamFrame) error {
		switch fr := f.GetFrame().(type) {
		case *llmv1.ConversationStreamFrame_AssistantTextDelta:
			d := fr.AssistantTextDelta.GetDelta()
			content.WriteString(d)
			return stream.Send(&llmv1.CompleteStreamResponse{Frame: &llmv1.CompleteStreamResponse_ContentDelta{ContentDelta: d}})
		case *llmv1.ConversationStreamFrame_AssistantThinkingDelta:
			d := fr.AssistantThinkingDelta.GetDelta()
			thinking.WriteString(d)
			return stream.Send(&llmv1.CompleteStreamResponse{Frame: &llmv1.CompleteStreamResponse_ThinkingDelta{ThinkingDelta: d}})
		case *llmv1.ConversationStreamFrame_ToolCallStreamFinished:
			e := fr.ToolCallStreamFinished
			tc := &llmv1.ToolCall{Id: e.GetId(), Name: e.GetName(), ArgumentsJson: e.GetArgumentsJson()}
			calls = append(calls, tc)
			return stream.Send(&llmv1.CompleteStreamResponse{Frame: &llmv1.CompleteStreamResponse_ToolCall{ToolCall: tc}})
		case *llmv1.ConversationStreamFrame_AssistantTurnFinished:
			e := fr.AssistantTurnFinished
			usage = e.GetUsage()
			finish = e.GetFinishReason()
			replyID = e.GetAssistantMessageId()
		case *llmv1.ConversationStreamFrame_AssistantTurnFailed:
			failure = fr.AssistantTurnFailed.GetError()
		case *llmv1.ConversationStreamFrame_StreamComplete:
			final = fr.StreamComplete
		}
		return nil
	}
	toolsFn := func() []*llmv1.ToolDefinition { return p.tools }
	started := time.Now()
	pctx, pspan := s.startSpan(ctx, "provider.chat_stream",
		attribute.String("llmd.model", p.opt.GetModel()),
		attribute.String("llmd.provider.id", p.providerID),
		attribute.String("llmd.provider.type", p.provider.String()))
	streamErr := p.backend.ChatStream(pctx, p.messages, p.opt, toolsFn, emit)
	endSpan(pspan, ctx, streamErr)
	callErr := streamErr
	if callErr == nil && failure != "" {
		callErr = errors.New(failure)
	}
	s.recordUsage(ctx, p.sc, usageCall{
		rpc: "CompleteStream", providerID: p.providerID, providerType: p.provider,
		model: p.opt.GetModel(), started: started, reply: final, usage: usage, err: callErr,
	})
	if err := streamErr; err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn, "CompleteStream: backend stream failed: %v", err)
		return llmerr.ToStatus(err, p.provider)
	}
	if failure != "" {
		llmdlog.Stdout("server", llmdlog.LevelWarn, "CompleteStream: backend reported turn failure: %s", failure)
		return llmerr.ToStatus(errors.New(failure), p.provider)
	}
	if final == nil {
		final = &llmv1.AssistantReply{
			Content:         content.String(),
			Id:              replyID,
			ToolCalls:       calls,
			FinishReason:    finish,
			ThinkingContent: thinking.String(),
			Usage:           usage,
		}
	} else if final.GetUsage() == nil && usage != nil {
		final.Usage = usage
	}
	return stream.Send(&llmv1.CompleteStreamResponse{Frame: &llmv1.CompleteStreamResponse_FinalReply{FinalReply: final}})
}

func (s *LlmServer) GetSessionHistory(ctx context.Context, req *llmv1.GetSessionHistoryRequest) (*llmv1.GetSessionHistoryResponse, error) {
	if strings.TrimSpace(req.GetSessionId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is required")
	}
	aid, err := requireSessionID(req.GetSessionId())
	if err != nil {
		return nil, err
	}
	resp := &llmv1.GetSessionHistoryResponse{}
	if req.GetStatusOnly() {
		// Poll-cheap path: no runtime load, no transcript/attachments/docs. The
		// response is flagged so callers never read the empty fields as data.
		resp.StatusOnly = true
		s.fillSessionStatus(ctx, req.GetSessionId(), resp)
		return resp, nil
	}
	// History of a not-yet-ensured (or unknown) session is legitimately empty — match the
	// prior direct-SQLite reader, which returned no rows rather than erroring. Only a genuine
	// internal failure should propagate; an unknown agent/session falls through to attachments
	// (which are store-backed by workspace+session and independent of the in-memory hub).
	//
	// This is a read: it goes straight to the scoped history store and never
	// builds a session runtime (no session-row read, no tools_json unmarshal).
	// History is scoped by (tenant, scope), so another scope's session simply
	// reads as empty — the same answer the old runtime-gated path gave.
	if s.SessionRuntimes != nil {
		sc, serr := s.scope(ctx)
		if serr != nil {
			return nil, serr
		}
		if hist := s.SessionRuntimes.HistoryFor(sc, aid); hist != nil {
			if h, ok := hist.(*store.History); ok {
				// Store-backed messages are freshly built per call: no clone needed.
				if s.TurnMetadata != nil {
					resp.Messages = h.HistoryWithUsage(ctx, req.GetSessionId(), int(req.GetMaxMessages()), s.TurnMetadata)
				} else {
					resp.Messages = h.History(req.GetSessionId(), int(req.GetMaxMessages()))
				}
			} else {
				// Other stores (e.g. in-memory) may hand out live objects: clone.
				msgs := hist.History(req.GetSessionId(), int(req.GetMaxMessages()))
				out := make([]*llmv1.Message, 0, len(msgs))
				for _, m := range msgs {
					if m == nil {
						continue
					}
					out = append(out, proto.Clone(m).(*llmv1.Message))
				}
				resp.Messages = out
			}
		}
	}
	s.appendSessionAttachmentRows(ctx, req.GetSessionId(), resp)
	s.appendTrackedDocumentRefs(ctx, req.GetSessionId(), resp)
	s.fillSessionStatus(ctx, req.GetSessionId(), resp)
	return resp, nil
}

// fillSessionStatus sets turn_in_progress / events_seq / turn_start_seq. A
// returning client uses these to decide whether to rejoin a running turn via
// SubscribeSessionEvents rather than treat the transcript as settled.
//
// events_seq is read BEFORE turn state so a turn that starts in between is
// still covered: its frames land after events_seq, and a client that
// subscribes from events_seq sees them.
func (s *LlmServer) fillSessionStatus(ctx context.Context, sessionID string, resp *llmv1.GetSessionHistoryResponse) {
	if s.SessionEvents != nil {
		sc, serr := s.scope(ctx)
		if serr != nil {
			llmdlog.Stdout("server", llmdlog.LevelWarn, "GetSessionHistory: resolve scope for event log head: session=%q err=%v", sessionID, serr)
		} else if head, herr := s.SessionEvents.LastSeq(ctx, sc, sessionID); herr == nil {
			resp.EventsSeq = head
		} else {
			llmdlog.Stdout("server", llmdlog.LevelWarn, "GetSessionHistory: event log head failed: session=%q err=%v", sessionID, herr)
		}
	}
	if startSeq, ok := s.turnRunner().active(sessionID); ok {
		resp.TurnInProgress = true
		resp.TurnStartSeq = startSeq
		if at, ok := s.turnRunner().startedAt(sessionID); ok {
			resp.TurnStartedAtMs = at.UnixMilli()
		}
	}
	// Reported even while a newer turn runs: it is still the last FINISHED one.
	if ft, ok := s.turnRunner().lastFinished(sessionID); ok {
		resp.LastTurnStartedAtMs = ft.startedAt.UnixMilli()
		resp.LastTurnEndedAtMs = ft.endedAt.UnixMilli()
	}
}

// appendTrackedDocumentRefs loads the session's tracked-document versions and seed for
// UI display (badges + viewer). Best-effort: a missing workspace or load error leaves the
// fields empty rather than failing the whole history fetch. These are display-only — the
// model-facing injection is assembled separately in ChatStream.
func (s *LlmServer) appendTrackedDocumentRefs(ctx context.Context, sessionID string, resp *llmv1.GetSessionHistoryResponse) {
	if resp == nil {
		return
	}
	sc, err := s.scope(ctx)
	if err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn, "GetSessionHistory: resolve scope for tracked documents: session=%q err=%v", sessionID, err)
		return
	}
	if s.TrackedDocs != nil {
		if versions, verr := s.TrackedDocs.ListSessionVersions(ctx, sc, sessionID); verr != nil {
			llmdlog.Stdout("server", llmdlog.LevelWarn, "GetSessionHistory: list tracked document versions failed: %s session=%q err=%v", sc.String(), sessionID, verr)
		} else {
			for _, v := range versions {
				change := "updated"
				if v.Seq == 0 {
					change = "initial"
				}
				resp.TrackedDocuments = append(resp.TrackedDocuments, &llmv1.TrackedDocumentRef{
					DocumentId:      v.DocumentID,
					Name:            v.Name,
					Version:         int32(v.Seq),
					AnchorMessageId: v.AnchorMessageID,
					Change:          change,
					Content:         v.Content,
				})
			}
		}
	}
	if s.SessionRepo != nil {
		sess, serr := s.SessionRepo.GetSession(ctx, sc, sessionID)
		if serr != nil && !errors.Is(serr, gorm.ErrRecordNotFound) {
			llmdlog.Stdout("server", llmdlog.LevelWarn, "GetSessionHistory: load session for initial seed failed: %s session=%q err=%v", sc.String(), sessionID, serr)
		} else if serr == nil && sess != nil {
			resp.InitialSeed = sess.InitialSeed
		}
	}
}

// appendSessionAttachmentRows loads UI-artifact attachment rows for the session and adds them to the
// response. Best-effort: a missing workspace or load error leaves attachment_rows empty rather than
// failing the whole history fetch.
func (s *LlmServer) appendSessionAttachmentRows(ctx context.Context, sessionID string, resp *llmv1.GetSessionHistoryResponse) {
	if s.Attachments == nil || resp == nil {
		return
	}
	sc, err := s.scope(ctx)
	if err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn, "GetSessionHistory: resolve scope for attachments: session=%q err=%v", sessionID, err)
		return
	}
	rows, err := s.Attachments.LoadRows(ctx, sc, sessionID)
	if err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn, "GetSessionHistory: load attachment rows failed: %s session=%q err=%v", sc.String(), sessionID, err)
		return
	}
	for _, r := range rows {
		resp.AttachmentRows = append(resp.AttachmentRows, &llmv1.SessionAttachmentRow{
			Content:    r.Content,
			ToolCallId: r.ToolCallID,
		})
	}
}

func (s *LlmServer) ReplaceSessionHistory(ctx context.Context, req *llmv1.ReplaceSessionHistoryRequest) (*llmv1.ReplaceSessionHistoryResponse, error) {
	sid := strings.TrimSpace(req.GetSessionId())
	if sid == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is required")
	}
	aid, err := requireSessionID(sid)
	if err != nil {
		return nil, err
	}
	if err := s.ensureKnownSession(ctx, aid); err != nil {
		return nil, err
	}
	if s.SessionRuntimes == nil {
		return nil, status.Error(codes.FailedPrecondition, "session manager not configured")
	}
	ma, ok := s.runtimeFor(ctx, aid)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "unknown session_id %q", sid)
	}
	hist := ma.Core.History()
	if hist != nil {
		hist.Clear(sid)
		for _, m := range req.GetMessages() {
			if m == nil {
				continue
			}
			hist.Store(sid, proto.Clone(m).(*llmv1.Message))
		}
	}
	if s.ProviderSessionSync != nil {
		sc, err := s.scope(ctx)
		if err != nil {
			return nil, err
		}
		if err := s.ProviderSessionSync.DeleteForSession(ctx, sc, aid, sid); err != nil {
			return nil, status.Errorf(codes.Internal, "clear provider session sync: %v", err)
		}
		// Cascade: delete turn metadata, sidecars, cache and decorations (the
		// shared per-session list owned by SessionRepo).
		if s.SessionRepo != nil {
			if derr := s.SessionRepo.PurgeDerivedRows(ctx, sc, sid); derr != nil {
				llmdlog.Stdout("server", llmdlog.LevelWarn, "clear derived session rows failed: scope=%+v session=%q err=%v", sc, sid, derr)
			}
		}
		// Drop in-memory dedup state so a reused session id doesn't back-reference
		// tool calls that no longer exist.
		s.clearSessionDedup(sid)
	}
	return &llmv1.ReplaceSessionHistoryResponse{}, nil
}

func (s *LlmServer) ListLlmProviders(ctx context.Context, req *llmv1.ListLlmProvidersRequest) (*llmv1.ListLlmProvidersResponse, error) {
	_ = req
	if s.Providers == nil {
		return nil, status.Error(codes.FailedPrecondition, "LLM provider registry not configured")
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	rows := s.Providers.ListMerged(ctx, sc)
	out := make([]*llmv1.LlmProviderInfo, 0, len(rows))
	for _, row := range rows {
		e := row.Entry
		if e == nil {
			continue
		}
		out = append(out, &llmv1.LlmProviderInfo{
			Id:                    row.ID,
			Type:                  kindToProto(e.Kind),
			DefaultModel:          e.DefaultModel,
			CatalogModelCount:     int32(e.CatalogLen()),
			ModelsRefreshedUnixMs: e.RefreshedUnixMs(),
			UserRegistered:        row.UserRegistered,
			Stateful:              e.Kind.StatefulProviderHistory(),
		})
	}
	return &llmv1.ListLlmProvidersResponse{Providers: out}, nil
}

// ListSupportedLlmProviderTypes returns the static catalog of provider kinds llmd can register,
// independent of s.Providers (no configured instances required). Drives "add provider" UI.
func (s *LlmServer) ListSupportedLlmProviderTypes(ctx context.Context, req *llmv1.ListSupportedLlmProviderTypesRequest) (*llmv1.ListSupportedLlmProviderTypesResponse, error) {
	_ = req
	kinds := providerkind.All()
	out := make([]*llmv1.SupportedLlmProviderType, 0, len(kinds))
	for _, k := range kinds {
		out = append(out, &llmv1.SupportedLlmProviderType{
			Type:           kindToProto(k),
			ShortCode:      k.String(),
			DisplayName:    k.DisplayName(),
			RequiresApiKey: k.RequiresAPIKey(),
			IsCliBacked:    k.IsCLIBacked(),
		})
	}
	return &llmv1.ListSupportedLlmProviderTypesResponse{Types: out}, nil
}

func (s *LlmServer) RefreshLlmProviderModels(ctx context.Context, req *llmv1.RefreshLlmProviderModelsRequest) (*llmv1.RefreshLlmProviderModelsResponse, error) {
	if s.Providers == nil {
		return nil, status.Error(codes.FailedPrecondition, "LLM provider registry not configured")
	}
	pid := strings.TrimSpace(req.GetProviderId())
	if pid == "" {
		return nil, status.Error(codes.InvalidArgument, "provider_id is required")
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	n, err := s.Providers.RefreshModels(ctx, sc, pid)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "%v", err)
	}
	return &llmv1.RefreshLlmProviderModelsResponse{ModelsDiscovered: int32(n)}, nil
}

func (s *LlmServer) ListLlmCatalogModels(ctx context.Context, req *llmv1.ListLlmCatalogModelsRequest) (*llmv1.ListLlmCatalogModelsResponse, error) {
	if s.Providers == nil {
		return nil, status.Error(codes.FailedPrecondition, "LLM provider registry not configured")
	}
	filter := ""
	if req != nil && req.ProviderId != nil {
		filter = strings.TrimSpace(*req.ProviderId)
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	raw := s.Providers.ListCatalog(ctx, sc, filter)
	out := make([]*llmv1.CatalogLlmModel, 0, len(raw))
	for _, m := range raw {
		cm := &llmv1.CatalogLlmModel{
			ProviderId: m.ProviderID,
			ModelId:    m.ModelID,
		}
		if d := strings.TrimSpace(m.Display); d != "" && d != m.ModelID {
			cm.DisplayName = proto.String(d)
		}
		out = append(out, cm)
	}
	return &llmv1.ListLlmCatalogModelsResponse{Models: out}, nil
}

func (s *LlmServer) RegisterLlmProvider(ctx context.Context, req *llmv1.RegisterLlmProviderRequest) (*llmv1.RegisterLlmProviderResponse, error) {
	if s.Providers == nil || s.Providers.DB == nil {
		return nil, status.Error(codes.FailedPrecondition, "user LLM provider persistence not configured")
	}
	if req == nil || strings.TrimSpace(req.GetProviderId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "provider_id is required")
	}
	// Registering a provider is rare, slow (it can shell out and make real model
	// calls) and fails in several different places that all surface as one opaque
	// gRPC error. Trace every stage at INFO so "adding a provider does nothing"
	// is answerable from the log alone.
	regStarted := time.Now()
	llmdlog.Stdout(providerLogModule, llmdlog.LevelInfo,
		"register provider: id=%q type=%s", req.GetProviderId(), req.GetType())

	cfg, err := providers.BuildProviderConfigFromRequest(req)
	if err != nil {
		llmdlog.Stdout(providerLogModule, llmdlog.LevelError,
			"register provider %q rejected: invalid config: %v", req.GetProviderId(), err)
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	// CLI-backed providers get their binary resolved (and, when unset, scanned
	// for) on this machine before the config is validated or stored. See
	// providers.ResolveCLIBinaryForConfig for why this happens once, here.
	if err := providers.ResolveCLIBinaryForConfig(ctx, cfg); err != nil {
		llmdlog.Stdout(providerLogModule, llmdlog.LevelError,
			"register provider %q rejected: could not resolve CLI binary: %v", req.GetProviderId(), err)
		return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
	}
	ent, err := providers.BuildEntryFromProviderConfig(cfg)
	if err != nil {
		llmdlog.Stdout(providerLogModule, llmdlog.LevelError,
			"register provider %q rejected: could not build backend: %v", req.GetProviderId(), err)
		return nil, status.Errorf(codes.InvalidArgument, "invalid provider config: %v", err)
	}
	if err := ent.ProbeClaudeCodeV2Resume(); err != nil {
		llmdlog.Stdout(providerLogModule, llmdlog.LevelError,
			"register provider %q rejected after %s: CLI probe failed: %v",
			req.GetProviderId(), time.Since(regStarted).Round(time.Millisecond), err)
		return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
	}
	pid := strings.TrimSpace(req.GetProviderId())
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.Providers.DB.UpsertConfig(ctx, sc, cfg); err != nil {
		llmdlog.Stdout(providerLogModule, llmdlog.LevelError,
			"register provider %q failed to persist: %v", pid, err)
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	s.Providers.InvalidateUserProvider(sc, pid)
	llmdlog.Stdout(providerLogModule, llmdlog.LevelInfo,
		"register provider %q OK in %s", pid, time.Since(regStarted).Round(time.Millisecond))
	return &llmv1.RegisterLlmProviderResponse{}, nil
}

func (s *LlmServer) UnregisterLlmProvider(ctx context.Context, req *llmv1.UnregisterLlmProviderRequest) (*llmv1.UnregisterLlmProviderResponse, error) {
	if s.Providers == nil || s.Providers.DB == nil {
		return nil, status.Error(codes.FailedPrecondition, "user LLM provider persistence not configured")
	}
	if req == nil || strings.TrimSpace(req.GetProviderId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "provider_id is required")
	}
	pid := strings.TrimSpace(req.GetProviderId())
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.Providers.DB.Delete(ctx, sc, pid); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "unknown user provider %q", pid)
		}
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	// Drop the cached catalog too, otherwise re-registering this id (possibly as
	// a different provider kind) would hydrate the dead provider's models.
	// Best-effort: the provider row is already gone, so failing the RPC here
	// would report an unregister that actually succeeded.
	if err := s.Providers.DeleteModels(ctx, sc, pid); err != nil {
		llmdlog.Stdout(providerLogModule, llmdlog.LevelWarn,
			"unregister provider %q (%s): dropping cached catalog failed: %v", pid, sc, err)
	}
	s.Providers.InvalidateUserProvider(sc, pid)
	return &llmv1.UnregisterLlmProviderResponse{}, nil
}

func (s *LlmServer) GetLlmProviderConfig(ctx context.Context, req *llmv1.GetLlmProviderConfigRequest) (*llmv1.GetLlmProviderConfigResponse, error) {
	if s.Providers == nil || s.Providers.DB == nil {
		return nil, status.Error(codes.FailedPrecondition, "user LLM provider persistence not configured")
	}
	pid := strings.TrimSpace(req.GetProviderId())
	if pid == "" {
		return nil, status.Error(codes.InvalidArgument, "provider_id is required")
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	cfg, err := s.Providers.DB.Get(ctx, sc, pid)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "unknown user provider %q", pid)
		}
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	resp, err := providers.BuildGetLlmProviderConfigResponse(cfg)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return resp, nil
}

func (s *LlmServer) UpdateLlmProviderConfig(ctx context.Context, req *llmv1.UpdateLlmProviderConfigRequest) (*llmv1.UpdateLlmProviderConfigResponse, error) {
	if s.Providers == nil || s.Providers.DB == nil {
		return nil, status.Error(codes.FailedPrecondition, "user LLM provider persistence not configured")
	}
	pid := strings.TrimSpace(req.GetProviderId())
	if pid == "" {
		return nil, status.Error(codes.InvalidArgument, "provider_id is required")
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	if _, err := s.Providers.DB.Get(ctx, sc, pid); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "unknown user provider %q", pid)
		}
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	updStarted := time.Now()
	llmdlog.Stdout(providerLogModule, llmdlog.LevelInfo,
		"update provider config: id=%q type=%s", pid, req.GetType())

	cfg, err := providers.BuildProviderConfigFromUpdateRequest(req)
	if err != nil {
		llmdlog.Stdout(providerLogModule, llmdlog.LevelError,
			"update provider %q rejected: invalid config: %v", pid, err)
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	// Update replaces the stored config wholesale, so it must re-resolve too:
	// without this, updating any other field while leaving binary blank would
	// erase the path that create resolved.
	if err := providers.ResolveCLIBinaryForConfig(ctx, cfg); err != nil {
		llmdlog.Stdout(providerLogModule, llmdlog.LevelError,
			"update provider %q rejected: could not resolve CLI binary: %v", pid, err)
		return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
	}
	ent, err := providers.BuildEntryFromProviderConfig(cfg)
	if err != nil {
		llmdlog.Stdout(providerLogModule, llmdlog.LevelError,
			"update provider %q rejected: could not build backend: %v", pid, err)
		return nil, status.Errorf(codes.InvalidArgument, "invalid provider config: %v", err)
	}
	if err := ent.ProbeClaudeCodeV2Resume(); err != nil {
		llmdlog.Stdout(providerLogModule, llmdlog.LevelError,
			"update provider %q rejected after %s: CLI probe failed: %v",
			pid, time.Since(updStarted).Round(time.Millisecond), err)
		return nil, status.Errorf(codes.FailedPrecondition, "%v", err)
	}
	if err := s.Providers.DB.UpsertConfig(ctx, sc, cfg); err != nil {
		llmdlog.Stdout(providerLogModule, llmdlog.LevelError,
			"update provider %q failed to persist: %v", pid, err)
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	llmdlog.Stdout(providerLogModule, llmdlog.LevelInfo,
		"update provider %q OK in %s", pid, time.Since(updStarted).Round(time.Millisecond))
	s.Providers.InvalidateUserProvider(sc, pid)
	return &llmv1.UpdateLlmProviderConfigResponse{}, nil
}

func (s *LlmServer) RegisterUserEmbeddingProvider(ctx context.Context, req *llmv1.RegisterUserEmbeddingProviderRequest) (*llmv1.RegisterUserEmbeddingProviderResponse, error) {
	if s.EmbeddingProviders == nil {
		return nil, status.Error(codes.FailedPrecondition, "user embedding provider persistence not configured")
	}
	if req == nil || strings.TrimSpace(req.GetProviderId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "provider_id is required")
	}
	pid := strings.TrimSpace(req.GetProviderId())
	cfg := &store.EmbeddingProviderConfig{ProviderID: pid}
	switch c := req.GetConfig().(type) {
	case nil:
		return nil, status.Error(codes.InvalidArgument, "embedding provider config is required (oneof ollama, openai_compat, bedrock)")
	case *llmv1.RegisterUserEmbeddingProviderRequest_Ollama:
		o := c.Ollama
		if o == nil {
			return nil, status.Error(codes.InvalidArgument, "ollama config is required")
		}
		cfg.Kind = "ollama"
		cfg.Ollama = &store.EmbeddingProviderOllamaRow{
			BaseURL:    o.GetBaseUrl(),
			EmbedModel: o.GetEmbedModel(),
			Dimensions: int(o.GetDimensions()),
		}
	case *llmv1.RegisterUserEmbeddingProviderRequest_OpenaiCompat:
		o := c.OpenaiCompat
		if o == nil {
			return nil, status.Error(codes.InvalidArgument, "openai_compat config is required")
		}
		cfg.Kind = "openai_compat"
		apiKey := ""
		if o.ApiKey != nil {
			apiKey = strings.TrimSpace(*o.ApiKey)
		}
		cfg.OpenAICompat = &store.EmbeddingProviderOpenAICompatRow{
			BaseURL:    o.GetBaseUrl(),
			APIKey:     apiKey,
			EmbedModel: o.GetEmbedModel(),
			Dimensions: int(o.GetDimensions()),
		}
	case *llmv1.RegisterUserEmbeddingProviderRequest_Bedrock:
		b := c.Bedrock
		if b == nil {
			return nil, status.Error(codes.InvalidArgument, "bedrock config is required")
		}
		cfg.Kind = "bedrock"
		profile := ""
		if b.Profile != nil {
			profile = strings.TrimSpace(*b.Profile)
		}
		endpoint := ""
		if b.EndpointUrl != nil {
			endpoint = strings.TrimSpace(*b.EndpointUrl)
		}
		cfg.Bedrock = &store.EmbeddingProviderBedrockRow{
			Region:      b.GetRegion(),
			Profile:     profile,
			EmbedModel:  b.GetEmbedModel(),
			Dimensions:  int(b.GetDimensions()),
			EndpointURL: endpoint,
		}
	default:
		return nil, status.Error(codes.InvalidArgument, "embedding provider config is required (oneof ollama, openai_compat, bedrock)")
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.EmbeddingProviders.UpsertConfig(ctx, sc, cfg); err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &llmv1.RegisterUserEmbeddingProviderResponse{}, nil
}

func (s *LlmServer) UnregisterUserEmbeddingProvider(ctx context.Context, req *llmv1.UnregisterUserEmbeddingProviderRequest) (*llmv1.UnregisterUserEmbeddingProviderResponse, error) {
	if s.EmbeddingProviders == nil {
		return nil, status.Error(codes.FailedPrecondition, "user embedding provider persistence not configured")
	}
	if req == nil || strings.TrimSpace(req.GetProviderId()) == "" {
		return nil, status.Error(codes.InvalidArgument, "provider_id is required")
	}
	pid := strings.TrimSpace(req.GetProviderId())
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.EmbeddingProviders.Delete(ctx, sc, pid); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, status.Errorf(codes.NotFound, "unknown embedding provider %q", pid)
		}
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &llmv1.UnregisterUserEmbeddingProviderResponse{}, nil
}

func (s *LlmServer) ListEmbeddingProviders(ctx context.Context, req *llmv1.ListEmbeddingProvidersRequest) (*llmv1.ListEmbeddingProvidersResponse, error) {
	_ = req
	if s.EmbeddingProviders == nil {
		return nil, status.Error(codes.FailedPrecondition, "user embedding provider persistence not configured")
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	ids, err := s.EmbeddingProviders.ListIDs(ctx, sc)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}
	return &llmv1.ListEmbeddingProvidersResponse{ProviderIds: ids}, nil
}

func (s *LlmServer) ChatStream(req *llmv1.ChatStreamRequest, stream grpc.ServerStreamingServer[llmv1.ConversationStreamFrame]) (retErr error) {
	ctx := stream.Context()
	aid, err := requireSessionID(req.GetSessionId())
	if err != nil {
		return err
	}
	if err := s.ensureKnownSession(ctx, aid); err != nil {
		return err
	}
	sid := strings.TrimSpace(req.GetSessionId())
	if err := s.reconcileSessionLlmBinding(ctx, aid, req.GetOptions()); err != nil {
		return err
	}
	opt := s.effectiveCallOptions(ctx, aid, req.GetOptions())
	debugLogCallOptions("ChatStream", aid, sid, req.GetOptions(), opt)
	readOnly := opt.GetReadOnly()
	ctx = s.ctxWithCursorAgentRun(ctx, aid, sid, readOnly)
	be, err := s.backendFor(ctx, aid)
	if err != nil {
		return err
	}
	incoming := req.GetMessages()

	if s.SessionRuntimes == nil {
		return status.Error(codes.FailedPrecondition, "session manager not configured")
	}
	ma, ok := s.runtimeFor(ctx, aid)
	if !ok {
		return status.Errorf(codes.NotFound, "unknown session_id %q", aid)
	}
	hist := ma.Core.History()
	hub := ma.Core
	// Deferred first so it runs LAST — after the turn is unregistered and the
	// gate released — so the drainer sees the session idle. Kicks the next
	// queued message, if any.
	if drainSc, serr := s.scope(ctx); serr == nil {
		defer s.kickQueueDrain(drainSc, strings.TrimSpace(req.GetSessionId()))
	}
	ma.MarkChatStreamStart()
	defer ma.MarkChatStreamEnd()

	// Take this session's turn slot before reading history, and hold it through
	// the end-of-turn commit below. A turn that arrives while another is running
	// waits here, in arrival order, instead of racing it: that is what makes an
	// interrupt (abort the live turn, immediately send the replacement) produce
	// the transcript the user saw rather than one where the interrupted partial
	// lands after the message that interrupted it. See turnGate.
	var releaseTurn func()
	if req.GetTurnOptions().GetQueueIfBusy() && s.SessionQueue != nil && sid != "" {
		// The caller asked llmd to decide: run now only if the session is
		// genuinely free, otherwise queue it (see TurnOptions.queue_if_busy).
		rel, queuedEntry, qerr := s.takeSessionOrEnqueue(ctx, sid, incoming, req.GetTurnOptions())
		if qerr != nil {
			return qerr
		}
		if queuedEntry != nil {
			return stream.Send(&llmv1.ConversationStreamFrame{
				SchemaVersion: conversationevents.SchemaVersion,
				Frame: &llmv1.ConversationStreamFrame_TurnQueued{
					TurnQueued: &llmv1.EvTurnQueued{SessionId: sid, Entry: queuedEntry},
				},
			})
		}
		releaseTurn = rel
	} else {
		releaseTurn, err = s.sessionTurnGate().acquire(ctx, sid)
		if err != nil {
			// The caller gave up (or went away) while queued — no turn to run.
			return err
		}
	}
	defer releaseTurn()

	// Trim the turn's events only after finishTurn has unregistered the turn
	// (so history no longer reports it in progress) and while the turn gate is
	// still held. Deferred before finishTurn so it runs after it. trimSc and
	// trimEvents are set once the scope is resolved below.
	var (
		trimSc     serverutil.Scope
		trimEvents bool
	)
	defer func() {
		if !trimEvents {
			return
		}
		pctx := context.WithoutCancel(ctx)
		if n, perr := s.SessionEvents.TrimToNewest(pctx, trimSc, sid); perr != nil {
			llmdlog.Stdout("server", llmdlog.LevelWarn, "ChatStream: trim committed events failed: session=%q err=%v", sid, perr)
		} else if n > 0 {
			llmdlog.Stdout("server", llmdlog.LevelDebug, "ChatStream: trimmed %d committed events: session=%q", n, sid)
		}
	}()

	// From here on the turn runs on a server-owned context, NOT the request's.
	// A client going away (navigation, reload, network blip) must not kill the
	// turn: it finishes, commits to history, and a returning client rejoins via
	// SubscribeSessionEvents. Only CancelTurn (explicit Stop) or server shutdown
	// end it early. The turn ctx keeps the request's values (scope, tenant, …).
	// finishTurn is deferred after releaseTurn so it runs first: the turn is
	// unregistered before the gate hands the session to the next turn.
	turnCtx, finishTurn := s.turnRunner().start(s.BaseContext, ctx, sid)
	defer finishTurn()
	ctx = turnCtx
	var turnSpan trace.Span
	ctx, turnSpan = s.startSpan(ctx, "turn", attribute.String("llmd.session.id", sid))
	turnSpanCtx := ctx
	defer func() { endSpan(turnSpan, turnSpanCtx, retErr) }()

	// Get the workspace id early for session operations
	sc, workspaceErr := s.scope(ctx)

	// Request tool_context overrides the session's EnsureSession default.
	ctx = withToolContext(ctx, resolveToolContext(req.GetToolContext(), ma.ToolContextCopy()))

	// TurnOptions.disable_tools: registered for the life of the turn so the
	// MCP surface (Claude Code / Cursor) refuses tools too, not just this loop.
	noTools := req.GetTurnOptions().GetDisableTools()
	if noTools {
		if workspaceErr != nil {
			llmdlog.Stdout("server", llmdlog.LevelWarn,
				"ChatStream: disable_tools turn without a scope; MCP will not see it: session=%q err=%v", sid, workspaceErr)
		} else {
			defer s.beginNoToolsTurn(sc, sid)()
		}
		llmdlog.Stdout("server", llmdlog.LevelInfo, "ChatStream: tools disabled for this turn: session=%q", sid)
	}

	// Record where this turn's frames begin in the event log, so a client that
	// comes back mid-turn can rejoin from exactly here (GetSessionHistory
	// turn_start_seq) without re-receiving the session's older events.
	if workspaceErr == nil && s.SessionEvents != nil && sid != "" {
		if head, herr := s.SessionEvents.LastSeq(ctx, sc, sid); herr == nil {
			s.turnRunner().setEventsSeqAtStart(sid, head)
			// On commit, the defer registered after releaseTurn drops every
			// event but the newest: finished turns are read from history, and the
			// kept row keeps seq monotonic. Never on read.
			trimSc, trimEvents = sc, true
		} else {
			llmdlog.Stdout("server", llmdlog.LevelWarn, "ChatStream: event log head failed: session=%q err=%v", sid, herr)
		}
	}

	// The turn's status headline ends with the turn, however it ends (done,
	// failed, cancelled). Deferred after the event trim above so it runs
	// BEFORE it (defers are LIFO): the trim then also removes the end-of-turn
	// status frame. WithoutCancel because a cancelled turn's ctx is already
	// dead and the clear must still land.
	if workspaceErr == nil && sid != "" {
		defer s.clearTurnStatus(context.WithoutCancel(ctx), sc, sid, func(f *llmv1.ConversationStreamFrame) { hub.Session(sid).Dispatch(f) })
	}

	// Load session compression settings (early, for use in tool execution)
	var sessionCompressionSettings toolresult.Settings
	if sid != "" && workspaceErr == nil && s.SessionRepo != nil {
		sess, gerr := s.SessionRepo.GetSession(ctx, sc, sid)
		if gerr != nil && !errors.Is(gerr, gorm.ErrRecordNotFound) {
			llmdlog.Stdout("server", llmdlog.LevelWarn, "ChatStream: load session for compression settings failed (using defaults): %s session=%q err=%v", sc.String(), sid, gerr)
		}
		sessionCompressionSettings = s.compressionSettings(sess)
		llmdlog.Stdout("server", llmdlog.LevelInfo,
			"ChatStream compression: session=%q enabled=%v minify_dedup=%v extractive_search=%v threshold=%d",
			sid, sessionCompressionSettings.Enabled, sessionCompressionSettings.MinifyDedup, sessionCompressionSettings.ExtractiveSearch, sessionCompressionSettings.MinThresholdToks)
	}

	var histMsgs []*llmv1.Message
	if hist != nil && sid != "" {
		histMsgs = hist.History(sid, 0)
	}
	llmdlog.Stdout("server", llmdlog.LevelInfo,
		"ChatStream history: scope=%s session=%q hist_nil=%v hist_msgs=%d incoming=%d", sc.String(), sid, hist == nil, len(histMsgs), len(incoming))
	full := make([]*llmv1.Message, 0, len(histMsgs)+len(incoming))
	full = append(full, histMsgs...)
	full = append(full, incoming...)

	// Tracked documents + session seed: persist this turn's snapshots (hash-deduped)
	// and inject the model-facing blocks into the outbound user messages. Send-time
	// only — histMsgs and incoming stay raw, so persisted user content is exactly what
	// the user typed and later sends re-derive the injection deterministically.
	full = s.injectTrackedContextForSend(ctx, sc, sid, req, full)

	// Compression chokepoint — runs over the WHOLE prompt for the provider send only.
	// llmd owns history server-side, so prior tool results arrive via histMsgs (not
	// incoming); compressing only incoming would miss every result after the turn it
	// first appeared on. This is a send-time transform: the canonical store (histMsgs)
	// and the to-be-persisted incoming are never mutated — they stay raw, and are
	// re-compressed deterministically on each later send (rebuilding the in-memory
	// dedup map after a reload). The in-loop ingestToolResult call below still covers
	// the rare inline/hub tool-execution path.
	full = s.compressToolResultsForSend(ctx, aid, sid, full, sessionCompressionSettings)

	providerID := s.resolvedProviderID(ctx, aid)
	var providerKind providerkind.Kind
	if workspaceErr == nil {
		providerKind = providerKindFromComposite(s.Providers, ctx, s.scopeOrZero(ctx), providerID)
	}
	ctx, applied := applyStructuredSystemPrompt(ctx, req, full, providerID, providerKind, s.sessionDefaultSystemPromptSections(ctx, aid))
	full = applied.Messages

	stateful := sid != "" && s.ProviderSessionSync != nil && hist != nil && s.providerStatefulHistory(ctx, aid)
	var (
		resumeID  string
		watermark int
		sysFP     string
	)
	if stateful {
		if workspaceErr != nil {
			return workspaceErr
		}
		if providerID == "" {
			stateful = false
		}
	}
	if stateful {
		// Identity = the FINAL effective system-prompt content (post-render),
		// independent of HOW it was delivered (structured sections vs flat
		// prompt). `full` is already applied.Messages, so its system message
		// carries the rendered body either way. Keying on the delivery channel
		// (applied.Fingerprint vs fallback) used to flip sysFP between two hash
		// spaces whenever the app's delivery mode wobbled turn-to-turn, churning
		// the resume chain and causing intermittent amnesia. Hash the content.
		promptFP := systemMessagesFingerprint(full)
		sysFP = systemprompt.CombinedSessionFingerprint(promptFP, providerID, providerKind.String())
		// Fold the serving model into the stateful identity: a model switch (or
		// provider fan-out drift) must force a full resync, because a CLI
		// --resume session is bound to the model it was created under and will
		// not restore prior context across models.
		sysFP = systemprompt.WithModelFingerprint(sysFP, opt.GetModel())
		prev, err := s.ProviderSessionSync.Get(ctx, sc, aid, sid, providerID)
		if err != nil {
			return status.Errorf(codes.Internal, "provider session sync: %v", err)
		}
		kind := providerKindFromComposite(s.Providers, ctx, s.scopeOrZero(ctx), providerID)
		plan := resolveStatefulProviderSync(kind, prev, sysFP, len(histMsgs), len(full))
		resumeID = plan.resumeID
		watermark = plan.watermark
		var (
			prevWatermark = -1
			prevBackend   = ""
			prevFP        = ""
		)
		if prev != nil {
			prevWatermark = prev.SyncedMessageCount
			prevBackend = strings.TrimSpace(prev.BackendSessionID)
			prevFP = prev.SystemFingerprint
		}
		llmdlog.Stdout("server", llmdlog.LevelInfo,
			"ChatStream stateful plan: session=%q provider=%q kind=%q model=%q prev_watermark=%d prev_backend=%q fp_changed=%v -> resumeID=%q watermark=%d fullResync=%v",
			sid, providerID, kind.String(), opt.GetModel(), prevWatermark, prevBackend, prevFP != sysFP, resumeID, watermark, plan.fullResync)
	}

	messages := append([]*llmv1.Message(nil), full...)

	var final *llmv1.AssistantReply
	var activeTurnID string
	// send() queues this turn's session events without waiting for each write
	// (AppendEventAsync); wait for them before returning, so a caller that reads
	// the event log after ChatStream sees the whole turn.
	if s.SessionEvents != nil {
		defer func() {
			if err := s.SessionEvents.Flush(context.Background()); err != nil {
				llmdlog.Stdout("server", llmdlog.LevelWarn, "ChatStream: flush session events failed: session=%q err=%v", sid, err)
			}
		}()
	}
	// The turn is minted at round start (before the provider runs) rather than
	// reactively on the provider's AssistantTurnStarted frame, so turn_seq is
	// authoritative and already correct for both in-band (gRPC tool loop) and
	// out-of-band (MCP server) tool dispatch. send() carries this pre-issued seq
	// so the turn is never advanced twice. See the round loop below.
	var preAdvancedSeq int64
	var preAdvancedThisRound bool
	var thinkingAcc strings.Builder
	var lastActivity string
	var lastUsage *llmv1.TokenUsage
	// roundStarted is when the current round's provider call began.
	// lastModelDurationMs is that call's wall time: stamped onto its
	// turn-finished frame (so live clients see it) and saved with its tokens.
	var roundStarted time.Time
	var lastModelDurationMs *int64
	// partialTurn folds the active assistant turn's frames in memory, through the
	// SAME reducer the raw-chunk replay uses, so a turn that dies mid-flight can
	// be finalized from process memory. ChatStream no longer writes the
	// llmd_turn_raw_chunks table: it held the same frames as the session event
	// log, one insert per token, and replaying them through this same reducer
	// can only rebuild what memory already has. Reset per turn by the reducer's
	// own AssistantTurnStarted branch.
	partialTurn := conversationevents.IdleTurnState()
	// roundComplete is set when the provider emits StreamComplete in the current
	// round; reset at each round start. Lets a backend that returns nil after a
	// cancel be told apart from one that genuinely finished.
	var roundComplete bool
	// clientGone latches once stream.Send fails: the caller's stream is dead, but
	// the turn keeps running — folding, hub fan-out and event persistence go on so
	// the reply is committed and other/returning observers still see it.
	var clientGone bool

	wireNorm := newChatStreamWireNormalizer(hist != nil && sid != "")
	send := func(f *llmv1.ConversationStreamFrame) error {
		if f != nil {
			if ts := f.GetAssistantTurnStarted(); ts != nil {
				activeTurnID = ts.GetTurnId()
				lastActivity = ""
				// Advance the turn register exactly once per turn-start so both the
				// persisted llmd_session_turns row and the outbound wire frame agree
				// on turn_seq. Transport-local: store.History.Store (also driven by
				// this same handler for message persistence) must never advance it,
				// or turns would be double-counted.
				if preAdvancedThisRound {
					// Turn already minted at round start (before the provider could issue
					// out-of-band MCP tool calls), so TurnRegister.Current() is already
					// correct when those calls land. Reuse that seq; never advance twice.
					ts.TurnSeq = &preAdvancedSeq
				} else if s.TurnRegister != nil && sid != "" {
					if seq, err := s.TurnRegister.Advance(ctx, sc, sid, time.Now(), activeTurnID); err == nil {
						ts.TurnSeq = &seq
					} else {
						llmdlog.Stdout("server", llmdlog.LevelWarn, "ChatStream: turn register advance failed: scope=%+v session=%q turn=%q err=%v", sc, sid, activeTurnID, err)
					}
				}
			}
			if as := f.GetAssistantActivitySnapshot(); as != nil {
				if h := strings.TrimSpace(as.GetHeadline()); h != "" {
					lastActivity = h
				}
			}
			if atf := f.GetAssistantTurnFinished(); atf != nil {
				lastUsage = atf.GetUsage()
				if atf.ModelDurationMs == nil && !roundStarted.IsZero() {
					d := time.Since(roundStarted).Milliseconds()
					atf.ModelDurationMs = &d
				}
				lastModelDurationMs = atf.ModelDurationMs
			}
			if r := f.GetStreamComplete(); r != nil {
				final = r
				roundComplete = true
			}
			// Fold into the in-memory partial BEFORE any persistence or wire work,
			// so the recoverable partial covers every frame the provider produced —
			// including ones whose stream.Send is about to fail because the client
			// just went away.
			partialTurn = conversationevents.ReduceActiveAssistantTurn(partialTurn, f)
		}
		outs, err := wireNorm.Next(f)
		if err != nil {
			return err
		}
		for _, out := range outs {
			if out == nil {
				continue
			}
			if td := out.GetAssistantThinkingDelta(); td != nil && td.GetDelta() != "" {
				thinkingAcc.WriteString(td.GetDelta())
			}
			if !clientGone {
				if err := stream.Send(out); err != nil {
					// The caller left (or its transport broke). Not fatal: stop writing
					// to this stream and let the turn run to completion for history,
					// the hub, and the event log.
					clientGone = true
					llmdlog.Stdout("server", llmdlog.LevelInfo,
						"ChatStream: client stream gone; turn continues detached: session=%q turn=%q err=%v", sid, activeTurnID, err)
				}
			}
			// Persist the event: the repo's writer also pushes the committed row
			// to SubscribeSessionEvents / WebSocket tails (see
			// internal/sessionevents). Queued, not awaited: a slow write must not
			// stall the provider's stream, and the queue keeps order with every
			// other append to this session. The hub below is for in-process
			// folds, not subscriber delivery.
			if s.SessionEvents != nil && sid != "" && sc.TenantID != "" {
				if eventJSON, merr := protojson.Marshal(out); merr != nil {
					llmdlog.Stdout("server", llmdlog.LevelWarn, "ChatStream: marshal session event failed: session=%q turn=%q err=%v", sid, activeTurnID, merr)
				} else if aerr := s.SessionEvents.AppendEventAsync(sc, sid, string(eventJSON)); aerr != nil {
					llmdlog.Stdout("server", llmdlog.LevelWarn, "ChatStream: append session event failed: session=%q turn=%q err=%v", sid, activeTurnID, aerr)
				}
			}
			if hub != nil && sid != "" {
				hub.Session(sid).Dispatch(out)
			}
		}
		return nil
	}

	if um := lastCommittedUserMessage(incoming); um != nil {
		// A new user turn: no earlier headline applies. The end-of-turn clear
		// normally got there first; this covers a turn that never reached its
		// end (crash/restart). Before UserTurnCommitted, so a client that reads
		// the session after seeing that frame never gets a stale one.
		if workspaceErr == nil && sid != "" {
			s.clearTurnStatus(ctx, sc, sid, func(f *llmv1.ConversationStreamFrame) { hub.Session(sid).Dispatch(f) })
		}
		preview := um.GetContent()
		if len(preview) > 512 {
			// Truncate at a UTF-8 rune boundary: raw byte slicing can split a multi-byte
			// sequence (e.g. emoji, CJK, arrows), leaving an invalid tail that fails
			// proto string marshaling with "string field contains invalid UTF-8".
			preview = strings.ToValidUTF8(preview[:512], "")
		}
		// The runner's start time, so the live clock and the session status's
		// turn timing measure from the same instant.
		committedAt := time.Now()
		if at, ok := s.turnRunner().startedAt(sid); ok {
			committedAt = at
		}
		if err := send(&llmv1.ConversationStreamFrame{
			SchemaVersion: conversationevents.SchemaVersion,
			Frame: &llmv1.ConversationStreamFrame_UserTurnCommitted{
				UserTurnCommitted: &llmv1.EvUserTurnCommitted{
					UserMessageId: um.GetId(),
					TextPreview:   ptrOrNil(preview),
					CommittedAtMs: proto.Int64(committedAt.UnixMilli()),
				},
			},
		}); err != nil {
			return err
		}
	}

	// Set when the loop exits on a round whose reply was already appended to
	// messages (tool-requested end of turn, or the no-tools refusal), so the
	// post-loop commit does not persist that reply a second time.
	var finalAlreadyInMessages bool
	toolsFn := s.mergedToolsStream(ctx, aid, readOnly)
	if noTools {
		toolsFn = func() []*llmv1.ToolDefinition { return nil }
	}
	maxR := toolexec.EffectiveMaxToolRounds(req.GetOptions())
	persistCut := len(messages)

	if ChatDebugEnabled() {
		allDefs := s.mergedToolDefinitions(ctx, aid)
		filtered := toolsFn()
		debugLogf("ChatStream read_only=%v agent_id=%q session_id=%q registered_tools=%d advertised_tools=%d filtered_out=%v cursor_mode=%q",
			readOnly, aid, sid, len(allDefs), len(filtered), mutationToolNamesFiltered(allDefs, filtered), cursorModeForReadOnly(readOnly))
	}

	resumeHealed := false
	// commitCtx normally tracks ctx; on a user cancel we swap in a detached
	// context so the end-of-turn commit (history + provider-sync watermark) still
	// lands even though ctx is already Canceled. canceled/canceledErr carry the
	// cancellation out to the caller after the turn has been finalized.
	commitCtx := ctx
	var canceled bool
	var canceledErr error
	// canceledReason is the finish_reason stamped on the salvaged partial (and on
	// the terminal frames announcing it): "cancelled" for a user abort,
	// "timed_out" for a deadline. Empty while the turn is healthy.
	var canceledReason string
	// turn_seq is minted per assistant round (advanced at each EvAssistantTurnStarted),
	// so a multi-round tool loop spans several seqs within one ChatStream. Record the
	// seq active when each message was produced so a round-1 tool result keeps its
	// issuing turn's seq (capture-at-start), not the loop's final seq.
	msgSeq := map[*llmv1.Message]int64{}
	var firstTurnSeq int64
	// executorCancelHooked records that the abort hook (CancelTurn on ctx end)
	// has been registered for this turn.
	executorCancelHooked := false
	// incomingPersisted records that this send's user messages were written to
	// history at turn start (so session loads mid-turn show them).
	incomingPersisted := false
	// lastPolicyAllowed keeps the FilterTools decision across rounds its
	// cadence skips, for the rest of this turn.
	var lastPolicyAllowed map[string]bool
	for i := 0; i < maxR; i++ {
		activeTurnID = ""
		roundComplete = false
		preAdvancedThisRound = false
		preAdvancedSeq = 0
		ctxBackend := ctx
		var backendSlice []*llmv1.Message
		if stateful {
			if watermark > len(messages) {
				return status.Error(codes.Internal, "provider sync watermark out of range")
			}
			backendSlice = messages[watermark:]
			if len(backendSlice) == 0 {
				return status.Error(codes.Internal, "empty backend slice for stateful provider")
			}
			ctxBackend = attachStatefulBackendSession(ctx, providerKindFromComposite(s.Providers, ctx, s.scopeOrZero(ctx), providerID), sid, resumeID, func(id string) {
				if t := strings.TrimSpace(id); t != "" {
					resumeID = t
				}
			})
		} else {
			backendSlice = messages
		}
		// Wrap-up round: with final_round enabled, the last allowed round offers
		// no tools and carries the instruction as a transient user message. The
		// instruction goes only into a copy of the backend slice, never into
		// messages, because messages is persisted at end of turn. When maxR is 1
		// the only round is also the last, so it is a wrap-up round with no
		// tools. An empty instruction falls back to a default.
		wrapUp := opt.GetFinalRound().GetEnabled() && i == maxR-1
		roundToolsFn := toolsFn
		if wrapUp {
			roundToolsFn = func() []*llmv1.ToolDefinition { return nil }
			instr := strings.TrimSpace(opt.GetFinalRound().GetInstruction())
			if instr == "" {
				instr = defaultFinalRoundInstruction
			}
			wrapped := make([]*llmv1.Message, 0, len(backendSlice)+1)
			wrapped = append(wrapped, backendSlice...)
			wrapped = append(wrapped, &llmv1.Message{Role: "user", Content: instr})
			backendSlice = wrapped
		}
		// Policy hooks: FilterTools narrows what this round offers; CheckLLMCall
		// can end the turn before any provider call.
		policyProviderType := kindToProto(providerKind).String()
		var policyAllowed map[string]bool
		if s.PolicyHooks != nil && !wrapUp {
			filtered, allowed := s.policyFilterTools(ctx, policyhook.FilterToolsRequest{
				Scope: policyScopeOf(sc), AgentID: aid, SessionID: sid, Round: uint32(i),
				ProviderID: providerID, ProviderType: policyProviderType, Model: strings.TrimSpace(opt.GetModel()),
				Purpose: llmv1.PolicyPurpose_POLICY_PURPOSE_CHAT_STREAM, ReadOnly: readOnly,
				Messages: backendSlice, Tools: roundToolsFn(),
			}, lastPolicyAllowed)
			policyAllowed = allowed
			lastPolicyAllowed = allowed
			roundToolsFn = func() []*llmv1.ToolDefinition { return filtered }
		}
		if reason, deny := s.policyCheckLLM(ctx, policyhook.LLMCallRequest{
			Scope: policyScopeOf(sc), AgentID: aid, SessionID: sid, Round: uint32(i),
			ProviderID: providerID, ProviderType: policyProviderType, Model: strings.TrimSpace(opt.GetModel()),
			Purpose: llmv1.PolicyPurpose_POLICY_PURPOSE_CHAT_STREAM, ReadOnly: readOnly, Messages: backendSlice,
		}); deny {
			llmdlog.Stdout("server", llmdlog.LevelWarn, "ChatStream: policy denied LLM call: session=%q provider=%q reason=%q", sid, providerID, reason)
			if serr := send(&llmv1.ConversationStreamFrame{
				SchemaVersion: conversationevents.SchemaVersion,
				Frame: &llmv1.ConversationStreamFrame_AssistantTurnFailed{AssistantTurnFailed: &llmv1.EvAssistantTurnFailed{
					TurnId: activeTurnID, Error: "policy denied LLM call: " + reason,
				}},
			}); serr != nil {
				llmdlog.Stdout("server", llmdlog.LevelWarn, "ChatStream: send policy denial frame failed: session=%q err=%v", sid, serr)
			}
			s.recordUsage(ctx, sc, usageCall{
				rpc: "ChatStream", providerID: providerID, providerType: kindToProto(providerKind),
				model: strings.TrimSpace(opt.GetModel()), started: time.Now(),
				err: status.Error(codes.PermissionDenied, reason),
			})
			return status.Error(codes.PermissionDenied, "policy denied LLM call: "+reason)
		}
		if ChatDebugEnabled() {
			defs := roundToolsFn()
			debugLogf("ChatStream invoke backend agent_id=%q session_id=%q persist=1 backend_msgs=%d total_msgs=%d tool_defs=%d tool_names=%v round=%d/%d stateful=%v",
				aid, sid, len(backendSlice), len(messages), len(defs), toolDefNames(defs), i+1, maxR, stateful)
		}
		// Attach a sink so CLI-backed providers (Claude Code, Cursor) can capture
		// tool_use/tool_result messages from their internal loops for SQLite persistence.
		toolSink := &toolcapture.Sink{}
		ctxBackend = toolcapture.NewContext(ctxBackend, toolSink)
		// Outer tool-call deadline for providers that wrap every tool call in one
		// timeout of their own (Claude Code's MCP_TOOL_TIMEOUT). Computed HERE from
		// the full definitions: toolsFn hands providers stripped copies with no
		// executor max_duration_seconds, so they cannot derive it themselves.
		// The wrap-up round offers no tools, so its backstop is computed from none.
		backstopDefs := s.offeredToolDefinitions(ctx, aid, readOnly)
		if wrapUp {
			backstopDefs = nil
		} else if policyAllowed != nil {
			kept := make([]*llmv1.ToolDefinition, 0, len(backstopDefs))
			for _, d := range backstopDefs {
				if policyAllowed[d.GetName()] {
					kept = append(kept, d)
				}
			}
			backstopDefs = kept
		}
		ctxBackend = tooltimeout.WithBackstop(ctxBackend, tooltimeout.Backstop(backstopDefs))
		// llmd is the turn authority: mint the turn HERE — when the round begins,
		// before invoking the provider — so turn_seq is authoritative and already
		// correct no matter how this round's tools are dispatched: in-band by the
		// gRPC tool loop, or out-of-band via the MCP server (Claude Code / Cursor),
		// which reads TurnRegister.Current() to stamp turn_seq on graph decorations.
		// Advancing reactively on the provider's AssistantTurnStarted frame used to
		// race the out-of-band MCP calls, which then recorded turn_seq 0. send()
		// reuses this seq (preAdvancedThisRound) rather than advancing again.
		if s.TurnRegister != nil && sid != "" {
			if seq, aerr := s.TurnRegister.Advance(ctx, sc, sid, time.Now(), ""); aerr == nil {
				preAdvancedSeq = seq
				preAdvancedThisRound = true
			} else {
				llmdlog.Stdout("server", llmdlog.LevelWarn, "ChatStream: pre-turn advance failed: scope=%+v session=%q err=%v", sc, sid, aerr)
			}
		}
		// Persist what the user sent NOW, at turn start, not in the end-of-turn
		// commit. A turn outlives its client, and anyone loading the session while
		// it runs (a reload, another window, a rejoin) reads history: the message
		// they sent must be there, above the live reply, not appear only once the
		// model is done. Stamped with this first round's seq — the same turn_seq
		// the end-of-turn commit used to give it. Once only: a stale-resume heal
		// re-runs round 0.
		if !incomingPersisted && hist != nil && sid != "" {
			incomingPersisted = true
			seq := preAdvancedSeq
			if !preAdvancedThisRound && s.TurnRegister != nil {
				seq = s.TurnRegister.Current(sc, sid)
			}
			gh, _ := hist.(*store.History)
			for _, m := range incoming {
				if gh != nil && seq > 0 {
					gh.StoreInTurn(sid, seq, m)
				} else {
					hist.Store(sid, m)
				}
			}
			if workspaceErr == nil {
				s.persistIncomingAttachments(ctx, sc, sid, incoming)
			}
		}
		// cutTurn finalizes a turn whose ctx was ended on purpose (CancelTurn or
		// shutdown): salvage the streamed-so-far partial as this turn's reply and
		// fall through to the normal commit on a detached ctx. Assigning final
		// unconditionally (nil when nothing was captured) clears any prior
		// completed round so the commit does not double-store it.
		cutTurn := func(cause error) {
			canceled = true
			canceledErr = cause
			canceledReason = turnCutReason(ctx)
			commitCtx = context.WithoutCancel(ctx)
			final = s.reconstructPartialTurn(commitCtx, activeTurnID, sid, sc, partialTurn, canceledReason)
		}
		logLLMRequest(providerID, opt.GetModel(), sid, toolDefNames(roundToolsFn()))
		lastUsage = nil // a prior failed round's usage must not leak into this call's row
		lastModelDurationMs = nil
		usageStarted := time.Now()
		roundStarted = usageStarted
		roundCtx0, roundSpan := s.startSpan(ctx, "llm.round",
			attribute.String("llmd.session.id", sid),
			attribute.Int("llmd.round", i+1))
		_, provSpan := s.startSpan(roundCtx0, "provider.chat_stream",
			attribute.String("llmd.model", strings.TrimSpace(opt.GetModel())),
			attribute.String("llmd.provider.id", providerID),
			attribute.String("llmd.provider.type", kindToProto(providerKind).String()))
		if provSpan != nil {
			ctxBackend = trace.ContextWithSpan(ctxBackend, provSpan)
		}
		berr := be.ChatStream(ctxBackend, backendSlice, opt, roundToolsFn, send)
		endSpan(provSpan, ctx, berr)
		endSpan(roundSpan, ctx, berr)
		var usageReply *llmv1.AssistantReply
		if roundComplete {
			usageReply = final
		}
		// One row per provider call, on every outcome (ok, error kind, cancel).
		// Server-drained queue turns go through this same ChatStream, so they
		// are recorded here and nowhere else.
		s.recordUsage(ctx, sc, usageCall{
			rpc: "ChatStream", providerID: providerID, providerType: kindToProto(providerKind),
			model: strings.TrimSpace(opt.GetModel()), started: usageStarted,
			reply: usageReply, usage: lastUsage, err: berr,
		})
		if berr == nil && ctx.Err() != nil && (!roundComplete || len(final.GetToolCalls()) > 0) {
			// The backend treated the cancel as a clean end of stream (common for
			// subprocess providers whose child was killed). It is still a cut turn:
			// salvage instead of committing an incomplete/absent reply or running
			// this round's tools on a dead ctx.
			llmdlog.Stdout("server", llmdlog.LevelInfo,
				"ChatStream: backend returned nil after turn cancel; salvaging partial: session=%q turn=%q round_complete=%v", sid, activeTurnID, roundComplete)
			cutTurn(status.Error(codes.Canceled, "turn cancelled"))
			break
		}
		if err := berr; err != nil {
			// Self-heal a stale resume pointer: the provider CLI rejected the
			// backend session id we stored (conversation store diverged between
			// app instances, ~/.claude cleaned, machine migrated). Clear the sync
			// row and retry this round ONCE with a full resync from llmd's
			// canonical transcript instead of surfacing a dead session to the user.
			if stateful && !resumeHealed && strings.TrimSpace(resumeID) != "" && isStaleProviderResumeError(err) {
				resumeHealed = true
				llmdlog.Stdout("server", llmdlog.LevelWarn,
					"ChatStream stateful resume rejected by provider; clearing sync row and retrying with full resync: session=%q provider=%q stale_backend_session=%q err=%v",
					sid, providerID, resumeID, err)
				if s.ProviderSessionSync != nil {
					if derr := s.ProviderSessionSync.DeleteForSession(ctx, sc, aid, sid); derr != nil {
						llmdlog.Stdout("server", llmdlog.LevelWarn, "ChatStream: clear provider sync row failed: scope=%+v session=%q err=%v", sc, sid, derr)
					}
				}
				plan := staleResumeRetryPlan(providerKindFromComposite(s.Providers, ctx, s.scopeOrZero(ctx), providerID))
				resumeID = plan.resumeID
				watermark = plan.watermark
				i--
				continue
			}
			// A mid-stream user cancel is not an error turn: finalize whatever
			// streamed so far as this turn's assistant reply and fall through to the
			// normal end-of-turn commit, so llmd's transcript and the backend
			// --resume session stay in sync. The backend already holds the user
			// message (and its own partial) in its session; recording ours here
			// advances the watermark so the next turn resumes cleanly instead of
			// forcing a full resync. ctx is Canceled, so commit on a detached ctx.
			// Any death of ctx — not just an explicit cancel — means the turn was cut
			// short rather than that the provider failed, so the streamed-so-far text
			// must be kept either way. Testing for Canceled alone used to throw away
			// everything a timed-out turn had already produced.
			if ctx.Err() != nil {
				cutTurn(err)
				break
			}
			// Genuine backend error (not a user cancel): propagate without committing.
			llmdlog.Stdout("server", llmdlog.LevelWarn, "ChatStream: backend stream failed: session=%q provider=%q err=%v", sid, providerID, err)
			return llmerr.ToStatus(err, kindToProto(providerKind))
		}
		// Store captured provider-internal tool messages (Claude Code / Cursor inner loop
		// rounds) under dedicated "captured_*" kinds.  They appear in the diagnostic context
		// view but are excluded from History() so they never pollute the LLM context on
		// subsequent turns.  Do NOT append them to messages — that would send them back to
		// the model as if they were client-level tool rounds.
		if captured := toolSink.Take(); len(captured) > 0 {
			if hist != nil && sid != "" {
				if gh, ok := hist.(*store.History); ok && gh != nil {
					var capSeq int64
					if s.TurnRegister != nil {
						capSeq = s.TurnRegister.Current(sc, sid)
					}
					gh.StoreCaptured(sid, capSeq, captured)
				}
			}
		}
		// Persist token accounting for this LLM round.
		if s.TurnMetadata != nil && sid != "" && sc.TenantID != "" && activeTurnID != "" {
			meta := store.TurnMetadataRow{
				TenantID:  sc.TenantID,
				ScopeID:   sc.ScopeID,
				SessionID: sid,
				TurnID:    activeTurnID,
				// MessageID lets ListMessageUsage join directly on cm.message_id instead of
				// the randomly-generated cm.turn_id, which never matches activeTurnID.
				MessageID: final.GetId(),
				// Stamp the provider+model that actually served this turn (not the
				// session default, which can drift under provider fan-out / model switches).
				ProviderID: providerID,
				ModelID:    strings.TrimSpace(opt.GetModel()),
			}
			if lastUsage != nil {
				meta.ProviderInToks = lastUsage.InputTokens
				meta.ProviderOutToks = lastUsage.OutputTokens
				meta.ProviderCacheReadToks = lastUsage.CacheReadTokens
				meta.ProviderCacheWriteToks = lastUsage.CacheWriteTokens
			}
			// The provider's turn-finished frame carries the call's time; a round
			// that ended without one (cut short) is timed up to now.
			meta.ModelDurationMs = lastModelDurationMs
			if meta.ModelDurationMs == nil && !roundStarted.IsZero() {
				d := time.Since(roundStarted).Milliseconds()
				meta.ModelDurationMs = &d
			}
			if merr := s.TurnMetadata.Upsert(ctx, meta); merr != nil {
				llmdlog.Stdout("server", llmdlog.LevelWarn, "ChatStream: turn metadata upsert failed: session=%q turn=%q err=%v", sid, activeTurnID, merr)
			}
			lastUsage = nil // reset for next round
			lastModelDurationMs = nil
		}
		// Strip or convert tool-call markup the model wrote as plain text, for
		// tools offered this round only, before deciding whether to dispatch.
		final = applyTextToolCalls(final, toolDefNames(roundToolsFn()), opt.GetTextToolCallMode(), providerID, strings.TrimSpace(opt.GetModel()), sid, i+1)
		if final == nil || len(final.GetToolCalls()) == 0 {
			break
		}
		if wrapUp {
			// The model ignored the no-tools wrap-up and still asked for tools.
			// Keep its text, drop the calls, and end the turn without an error.
			llmdlog.Stdout("server", llmdlog.LevelWarn,
				"ChatStream wrap-up round returned tool calls; dropping them: session=%q turn=%q calls=%d", sid, activeTurnID, len(final.GetToolCalls()))
			stripped, ok := proto.Clone(final).(*llmv1.AssistantReply)
			if !ok {
				return status.Error(codes.Internal, "wrap-up: clone reply failed")
			}
			stripped.ToolCalls = nil
			final = stripped
			break
		}
		if i == maxR-1 {
			return status.Errorf(codes.ResourceExhausted, "max_tool_rounds exceeded (%d)", maxR)
		}
		turnID := activeTurnID
		if !executorCancelHooked && s.ToolProviderHub != nil && turnID != "" {
			executorCancelHooked = true
			hub, tenant, tid := s.ToolProviderHub, sc.TenantID, turnID
			defer func() {
				// Only on abort: the stream ctx ended before the turn completed.
				if ctx.Err() == nil {
					return
				}
				n := hub.CancelTurn(tenant, tid)
				llmdlog.Stdout("server", llmdlog.LevelInfo,
					"chat stream aborted mid-turn: cancelled executor calls: tenant=%s turn=%s cancelled=%d", tenant, tid, n)
			}()
		}
		// Seq active for this round: shared by this round's assistant message and
		// the tool results it issues (matches env.TurnSeq captured at dispatch).
		var roundSeq int64
		if s.TurnRegister != nil {
			roundSeq = s.TurnRegister.Current(sc, sid)
		}
		if firstTurnSeq == 0 {
			firstTurnSeq = roundSeq
		}
		roundAsst := assistantMessageFromReply(final)
		messages = append(messages, roundAsst)
		if roundSeq > 0 {
			msgSeq[roundAsst] = roundSeq
		}
		if stateful {
			watermark = len(messages)
		}
		by := toolDefByName(s.mergedToolDefinitions(ctx, aid))
		if noTools {
			// The model called tools despite being offered none. Refuse every
			// call (each still gets a result so the transcript stays valid) and
			// end the turn after this round instead of re-querying the model.
			for _, tc := range final.GetToolCalls() {
				if tc == nil || tc.GetId() == "" {
					continue
				}
				llmdlog.Stdout("server", llmdlog.LevelWarn,
					"ChatStream refused tool call: tools disabled for this turn: tool=%q session=%q turn=%q",
					tc.GetName(), sid, turnID)
				toolMsg := &llmv1.Message{Role: "tool", ToolCallId: tc.GetId(), Content: NoToolsTurnRefusal}
				messages = append(messages, toolMsg)
				if roundSeq > 0 {
					msgSeq[toolMsg] = roundSeq
				}
			}
			if stateful {
				watermark = len(messages)
			}
			// roundAsst (this reply) is already in messages; don't store it
			// a second time as the turn's final reply.
			finalAlreadyInMessages = true
			break
		}
		// turnCtl collects end-turn requests from this round's tool calls: any
		// SUCCESSFUL tool may ask to end the turn (Result.EndsTurn inline,
		// ends_turn on executor/provider/client results — e.g. ask_user, or a
		// tool that proposes a change and waits for approval). Every tool call
		// in the round still runs; afterwards, if any asked, the loop stops
		// instead of re-querying the model. A model has no way to itself
		// decline to keep generating, so this is what makes "this call ends
		// your turn" true rather than a suggestion. The turn ends exactly as
		// if the model had stopped calling tools on its own — same code path
		// as the `len(final.GetToolCalls()) == 0` exit above.
		turnCtl := &toolseam.TurnControl{}
		roundCtx := toolseam.WithTurnControl(ctx, turnCtl)
		// Execute in stable priority order (lower first; no priority = 0), but
		// append tool messages in the model's original call order.
		validCalls := make([]*llmv1.ToolCall, 0, len(final.GetToolCalls()))
		for _, tc := range final.GetToolCalls() {
			if tc == nil || tc.GetId() == "" || strings.TrimSpace(tc.GetName()) == "" {
				continue
			}
			validCalls = append(validCalls, tc)
		}
		execOrder := make([]int, len(validCalls))
		for k := range execOrder {
			execOrder[k] = k
		}
		callPriority := func(tc *llmv1.ToolCall) int32 {
			if d := by[strings.TrimSpace(tc.GetName())]; d != nil && d.Priority != nil {
				return d.GetPriority()
			}
			return 0
		}
		sort.SliceStable(execOrder, func(a, b int) bool {
			return callPriority(validCalls[execOrder[a]]) < callPriority(validCalls[execOrder[b]])
		})
		toolMsgs := make([]*llmv1.Message, len(validCalls))
		appendToolMsgs := func() {
			for _, m := range toolMsgs {
				if m == nil {
					continue
				}
				messages = append(messages, m)
				if roundSeq > 0 {
					msgSeq[m] = roundSeq
				}
			}
		}
		for _, idx := range execOrder {
			tc := validCalls[idx]
			name := strings.TrimSpace(tc.GetName())
			// `by` is the session's EFFECTIVE set, so a tool this session may
			// not use is already absent and would fall through as a nil def.
			// Log the refusal explicitly rather than letting it read as a
			// generic "tool is not registered": a model can hallucinate a name
			// and a replayed frame can carry one, and an operator needs to see
			// which tool a session was blocked from and why.
			if _, permitted := by[name]; !permitted {
				llmdlog.Stdout("server", llmdlog.LevelWarn,
					"ChatStream refused tool call: tool not in session's effective tool set: tool=%q session=%q turn=%q reason=%s",
					name, sid, turnID, "unknown_disabled_or_not_permitted_by_tool_policy")
			}
			content, success, err := s.executeToolForStream(withPolicyRound(roundCtx, &policyRound{
				round: uint32(i), allowed: policyAllowed, providerID: providerID, providerType: policyProviderType,
				model: strings.TrimSpace(opt.GetModel()), messages: backendSlice,
			}), aid, sid, turnID, by[name], tc, readOnly, send, sessionCompressionSettings)
			if err != nil {
				// Keep results collected so far, in original call order.
				appendToolMsgs()
				return err
			}
			// Compression chokepoint — runs on all execution paths (inline + delegated).
			content = s.ingestToolResult(ctx, sid, turnID, tc, content, success)
			toolMsgs[idx] = &llmv1.Message{
				Role:       "tool",
				ToolCallId: tc.GetId(),
				Content:    content,
			}
		}
		appendToolMsgs()
		if stateful {
			watermark = len(messages)
		}
		if turnCtl.EndRequested() {
			for _, r := range turnCtl.Requests() {
				llmdlog.Stdout("server", llmdlog.LevelInfo,
					"ChatStream ending turn at tool request: session=%q turn=%q tool=%q tool_call=%q source=%s",
					sid, turnID, r.ToolName, r.ToolCallID, r.Source)
			}
			// Same as the no-tools exit: roundAsst is already in messages.
			finalAlreadyInMessages = true
			break
		}
	}

	if hist != nil && sid != "" {
		// Stamp each persisted row with the turn_seq active when it was produced
		// (msgSeq), so a round-1 tool result keeps seq 1 even after later rounds
		// advance the register. curSeq is the last (current) turn; incoming user
		// messages belong to the first turn of this send. gh != nil && seq > 0
		// opts into StoreInTurn; otherwise fall back to the turn_seq-less Store.
		var curSeq int64
		if s.TurnRegister != nil {
			curSeq = s.TurnRegister.Current(sc, sid)
		}
		gh, _ := hist.(*store.History)
		storeRow := func(m *llmv1.Message, seq int64) {
			if gh != nil && seq > 0 {
				gh.StoreInTurn(sid, seq, m)
				return
			}
			hist.Store(sid, m)
		}
		// The incoming user messages were persisted at turn start (see the round
		// loop); only what the turn produced is committed here.
		if !incomingPersisted {
			// No round ever started (e.g. max_tool_rounds 0): keep the old
			// guarantee that what the user sent is recorded.
			incomingSeq := firstTurnSeq
			if incomingSeq == 0 {
				incomingSeq = curSeq
			}
			for _, m := range incoming {
				storeRow(m, incomingSeq)
			}
			if workspaceErr == nil {
				s.persistIncomingAttachments(commitCtx, sc, sid, incoming)
			}
		}
		for _, m := range messages[persistCut:] {
			if m == nil {
				continue
			}
			seq := msgSeq[m]
			if seq == 0 {
				seq = curSeq
			}
			storeRow(m, seq)
		}
		if final != nil && !finalAlreadyInMessages {
			am := assistantMessageFromReply(final)
			if am != nil && strings.TrimSpace(am.GetThinkingContent()) == "" && thinkingAcc.Len() > 0 {
				am.ThinkingContent = thinkingAcc.String()
			}
			if am != nil && strings.TrimSpace(am.GetActivityHeadline()) == "" && lastActivity != "" {
				am.ActivityHeadline = lastActivity
			}
			storeRow(am, curSeq)
		}
		llmdlog.Stdout("server", llmdlog.LevelInfo,
			"ChatStream persisted: scope=%s session=%q total_rows_now=%d", sc.String(), sid, len(hist.History(sid, 0)))
	}
	if stateful {
		synced := 0
		if hist != nil {
			synced = len(hist.History(sid, 0))
		}
		if err := s.ProviderSessionSync.Upsert(commitCtx, &store.ProviderSessionSync{
			Scope:              sc,
			AgentID:            aid,
			SessionID:          sid,
			ProviderID:         providerID,
			BackendSessionID:   resumeID,
			SystemFingerprint:  sysFP,
			SyncedMessageCount: synced,
		}); err != nil {
			return status.Errorf(codes.Internal, "provider session sync upsert: %v", err)
		}
	}
	if canceled {
		// Tell every OTHER observer the turn is over. The caller's own stream is
		// already broken — that break is what killed the turn — but the session hub
		// and the session-event log feed things that cancelled nothing: other
		// windows on this session, SubscribeSessionEvents clients, and replay after
		// a reload. With no terminal frame they fold a turn that stays "active"
		// forever and show a spinner that never resolves. Emitted after the commit
		// above, so anyone who reacts by re-reading history already sees the
		// salvaged partial rather than a transcript that is missing it.
		if sid != "" {
			var sink turnFrameSink
			if hub != nil {
				sink = hub.Session(sid)
			}
			s.announceTurnCut(commitCtx, sink, sid, sc, activeTurnID, final.GetId(), canceledReason)
		}
		return canceledErr
	}
	return nil
}

// turnFrameSink is the session hub's fan-out surface, narrowed to what
// announceTurnCut needs (and kept as an interface so it can be faked in tests).
type turnFrameSink interface {
	Dispatch(*llmv1.ConversationStreamFrame)
}

// announceTurnCut publishes the terminal frames for a turn that ended because
// its request context died, to the observers that outlive the request: the
// session hub (live fan-out) and the session-event log (replay). Both a
// turn-finished frame carrying the truncation reason and a conversation-aborted
// frame are emitted — the first resolves the active turn for anyone folding
// turn state, the second is the session-level "this stopped, and not because the
// model was done" signal. ctx must be detached; the request ctx is dead here by
// definition.
func (s *LlmServer) announceTurnCut(
	ctx context.Context,
	sink turnFrameSink,
	sid string,
	sc serverutil.Scope,
	turnID, assistantMsgID, reason string,
) {
	frames := make([]*llmv1.ConversationStreamFrame, 0, 2)
	if turnID != "" {
		fin := &llmv1.EvAssistantTurnFinished{TurnId: turnID, FinishReason: ptrOrNil(reason)}
		if assistantMsgID != "" {
			fin.AssistantMessageId = ptrOrNil(assistantMsgID)
		}
		frames = append(frames, &llmv1.ConversationStreamFrame{
			SchemaVersion: conversationevents.SchemaVersion,
			Frame:         &llmv1.ConversationStreamFrame_AssistantTurnFinished{AssistantTurnFinished: fin},
		})
	}
	frames = append(frames, &llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_ConversationAborted{
			ConversationAborted: &llmv1.EvConversationAborted{Reason: ptrOrNil(reason)},
		},
	})
	for _, f := range frames {
		// Persist BEFORE fanning out. A live listener commonly reacts to a
		// terminal frame by re-reading the session (history, or the event log for
		// replay), and dispatching first lets that read race the write that is
		// supposed to back it — on SQLite the read can even be what makes the
		// write fail with "table is locked", leaving a session whose abort is
		// announced but not durable. Durability first, visibility second.
		if s.SessionEvents != nil && sc.TenantID != "" {
			if eventJSON, merr := protojson.Marshal(f); merr != nil {
				llmdlog.Stdout("server", llmdlog.LevelWarn,
					"failed to marshal terminal frame for cut turn: scope=%s session=%q turn=%q: %v",
					sc.String(), sid, turnID, merr)
			} else if _, aerr := s.SessionEvents.AppendEvent(ctx, sc, sid, string(eventJSON)); aerr != nil {
				llmdlog.Stdout("server", llmdlog.LevelWarn,
					"failed to persist terminal frame for cut turn: scope=%s session=%q turn=%q: %v",
					sc.String(), sid, turnID, aerr)
			}
		}
		if sink != nil {
			sink.Dispatch(f)
		}
	}
}

// executeToolForStream emits tool execution frames when appropriate and runs webhook, inline, unset, or client-delegated execution.
func (s *LlmServer) executeToolForStream(ctx context.Context, aid, sid, turnID string, def *llmv1.ToolDefinition, tc *llmv1.ToolCall, readOnly bool, send func(*llmv1.ConversationStreamFrame) error, compressionSettings toolresult.Settings) (content string, success bool, err error) {
	if tc == nil || tc.GetId() == "" || strings.TrimSpace(tc.GetName()) == "" {
		return "", true, nil
	}
	toolStarted := time.Now()
	defer func() {
		s.RecordToolUsage(ctx, tc.GetName(), ToolUsageSourceChat, sid, success && err == nil, time.Since(toolStarted))
	}()
	var toolSpan trace.Span
	ctx, toolSpan = s.startSpan(ctx, "tool.execute",
		attribute.String("llmd.tool.name", strings.TrimSpace(tc.GetName())),
		attribute.String("llmd.session.id", sid))
	defer func() { endSpan(toolSpan, ctx, err) }()
	inv := toolexec.Invocation{
		ToolCallID:    tc.GetId(),
		Name:          tc.GetName(),
		ArgumentsJSON: tc.GetArgumentsJson(),
		AgentID:       aid,
		SessionID:     sid,
		ReadOnly:      readOnly,
	}
	if readOnly && toolmerge.IsMutationTool(def) {
		content = toolexec.ReadOnlyMutationError
		return content, false, nil
	}
	if denial, denied := s.policyToolDenial(ctx, aid, sid, turnID, def, tc, readOnly); denied {
		llmdlog.Stdout("server", llmdlog.LevelWarn, "tool call denied by policy: tool=%q session=%q turn=%q reason=%q", tc.GetName(), sid, turnID, denial)
		if turnID != "" || (def != nil && def.GetDelegateToClient() != nil) {
			if err = emitDeniedToolFrames(turnID, tc, denial, send); err != nil {
				return "", false, err
			}
		}
		return denial, false, nil
	}
	delegate := def != nil && def.GetDelegateToClient() != nil
	emit := turnID != "" || delegate
	var errMsg string

	if delegate {
		sc, ierr := s.scope(ctx)
		if ierr != nil {
			return "", false, ierr
		}
		hubReady := s.ToolProviderHub != nil && s.ToolProviderHub.HasProvider(sc)
		if s.ToolWaits == nil && !hubReady {
			content = "client-delegated tool execution is not configured on this server"
			success = false
			errMsg = "tool_waits not configured"
			if emit && send != nil {
				started := toolExecutionStarted(turnID, tc)
				if err = send(&llmv1.ConversationStreamFrame{
					SchemaVersion: conversationevents.SchemaVersion,
					Frame:         &llmv1.ConversationStreamFrame_ToolExecutionStarted{ToolExecutionStarted: started},
				}); err != nil {
					return "", false, err
				}
				fin := &llmv1.EvToolExecutionFinished{
					TurnId: turnID, ToolCallId: tc.GetId(), Name: tc.GetName(), Success: false,
					ErrorMessage: proto.String(errMsg),
				}
				if err = send(&llmv1.ConversationStreamFrame{
					SchemaVersion: conversationevents.SchemaVersion,
					Frame:         &llmv1.ConversationStreamFrame_ToolExecutionFinished{ToolExecutionFinished: fin},
				}); err != nil {
					return content, success, err
				}
			}
			return content, success, nil
		}
		if hubReady {
			// sc was resolved and validated by s.scope above.
			dur := toolexec.DelegateWaitDuration(def)
			// Captured at invocation start (before Invoke dispatches the tool) so
			// the envelope reflects the turn active when this tool BEGAN, even if
			// TurnRegister.Advance runs again while the tool is still in-flight.
			var turnSeq int64
			if s.TurnRegister != nil {
				turnSeq = s.TurnRegister.Current(sc, sid)
			}
			if emit && send != nil {
				if ChatDebugEnabled() {
					debugLogf("tool_call exec session_id=%q turn_id=%q tool_call_id=%q name=%q phase=start delegate=stream",
						sid, turnID, tc.GetId(), tc.GetName())
				}
				started := toolExecutionStarted(turnID, tc)
				if err = send(&llmv1.ConversationStreamFrame{
					SchemaVersion: conversationevents.SchemaVersion,
					Frame:         &llmv1.ConversationStreamFrame_ToolExecutionStarted{ToolExecutionStarted: started},
				}); err != nil {
					return "", false, err
				}
			}
			c, isErr, ddk, gp, herr := s.ToolProviderHub.Invoke(ctx, sc, aid, sid, sc.TenantID, "", tc.GetName(), tc.GetArgumentsJson(), dur)
			if herr != nil {
				success = false
				errMsg = strings.TrimSpace(herr.Error())
				if errMsg == "" {
					errMsg = "tool provider execution failed"
				}
				content = errMsg
				if emit && send != nil {
					fin := &llmv1.EvToolExecutionFinished{
						TurnId: turnID, ToolCallId: tc.GetId(), Name: tc.GetName(), Success: false,
						ErrorMessage: proto.String(errMsg),
					}
					if err = send(&llmv1.ConversationStreamFrame{
						SchemaVersion: conversationevents.SchemaVersion,
						Frame:         &llmv1.ConversationStreamFrame_ToolExecutionFinished{ToolExecutionFinished: fin},
					}); err != nil {
						return "", false, err
					}
				}
				return content, success, herr
			}
			content = c
			success = !isErr
			// Record-early phase (Source=grpc-loop): dedup key + decorations are
			// registered at tool-finish via RecordInvocation so a later result in
			// the same turn can back-reference this invocation (API-provider dedup
			// parity). Compression stays decoupled and runs later at history-append
			// via ingestToolResult -> CompressResult. Decorations inherit
			// turn_seq/turn_id from this same envelope — no separate, turn-less
			// recording call.
			decorations := toDecorations(gp)
			if !isErr && (ddk != "" || len(decorations) > 0) {
				s.sessionService().RecordInvocation(ctx, toolsession.ToolInvocationEnvelope{
					Scope:       sc,
					SessionID:   sid,
					TurnID:      turnID,
					ToolCallID:  tc.GetId(),
					ToolName:    tc.GetName(),
					TurnSeq:     turnSeq,
					Source:      "grpc-loop",
					DedupKey:    ddk,
					Decorations: decorations,
				})
			}
			if isErr {
				errMsg = strings.TrimSpace(content)
				if errMsg == "" {
					errMsg = "tool error"
				}
			}
			if emit && send != nil {
				fin := &llmv1.EvToolExecutionFinished{TurnId: turnID, ToolCallId: tc.GetId(), Name: tc.GetName(), Success: success}
				if errMsg != "" {
					fin.ErrorMessage = proto.String(errMsg)
				}
				if ChatDebugEnabled() {
					debugLogf("tool_call exec session_id=%q turn_id=%q tool_call_id=%q name=%q phase=finish success=%v delegate=stream",
						sid, turnID, tc.GetId(), tc.GetName(), success)
				}
				if err = send(&llmv1.ConversationStreamFrame{
					SchemaVersion: conversationevents.SchemaVersion,
					Frame:         &llmv1.ConversationStreamFrame_ToolExecutionFinished{ToolExecutionFinished: fin},
				}); err != nil {
					return content, success, err
				}
			}
			return content, success, nil
		}
		nonce := uuid.New().String()
		ch := s.ToolWaits.Register(nonce, aid, tc.GetId(), sc)
		if emit && send != nil {
			if ChatDebugEnabled() {
				debugLogf("tool_call exec session_id=%q turn_id=%q tool_call_id=%q name=%q phase=start delegate=1 nonce=%q",
					sid, turnID, tc.GetId(), tc.GetName(), nonce)
			}
			started := toolExecutionStarted(turnID, tc)
			started.ClientWaitNonce = proto.String(nonce)
			if err = send(&llmv1.ConversationStreamFrame{
				SchemaVersion: conversationevents.SchemaVersion,
				Frame:         &llmv1.ConversationStreamFrame_ToolExecutionStarted{ToolExecutionStarted: started},
			}); err != nil {
				s.ToolWaits.Abort(nonce)
				return "", false, err
			}
		}
		res, werr := s.ToolWaits.Wait(ctx, nonce, ch, toolexec.DelegateWaitDuration(def))
		switch {
		case werr == nil:
			content = res.Content
			success = !res.IsError
			if res.EndsTurn && success {
				toolseam.TurnControlFrom(ctx).RequestEnd(toolseam.EndTurnRequest{ToolCallID: tc.GetId(), ToolName: tc.GetName(), Source: "client"})
			}
			if res.IsError {
				errMsg = strings.TrimSpace(content)
				if errMsg == "" {
					errMsg = "tool error"
				}
			}
		case errors.Is(werr, tooldelegate.ErrWaitTimeout):
			content, success, errMsg = "client tool execution timed out", false, "timeout"
		case errors.Is(werr, tooldelegate.ErrWaitAborted):
			content, success, errMsg = "client tool execution timed out or was cancelled", false, "wait aborted"
		default:
			return "", false, werr
		}
		if emit && send != nil {
			fin := &llmv1.EvToolExecutionFinished{TurnId: turnID, ToolCallId: tc.GetId(), Name: tc.GetName(), Success: success}
			if errMsg != "" {
				fin.ErrorMessage = proto.String(errMsg)
			}
			if ChatDebugEnabled() {
				debugLogf("tool_call exec session_id=%q turn_id=%q tool_call_id=%q name=%q phase=finish success=%v delegate=1",
					sid, turnID, tc.GetId(), tc.GetName(), success)
			}
			if err = send(&llmv1.ConversationStreamFrame{
				SchemaVersion: conversationevents.SchemaVersion,
				Frame:         &llmv1.ConversationStreamFrame_ToolExecutionFinished{ToolExecutionFinished: fin},
			}); err != nil {
				return content, success, err
			}
		}
		return content, success, nil
	}

	if emit && turnID != "" && send != nil {
		if ChatDebugEnabled() {
			debugLogf("tool_call exec session_id=%q turn_id=%q tool_call_id=%q name=%q phase=start",
				sid, turnID, tc.GetId(), tc.GetName())
		}
		if err = send(&llmv1.ConversationStreamFrame{
			SchemaVersion: conversationevents.SchemaVersion,
			Frame: &llmv1.ConversationStreamFrame_ToolExecutionStarted{
				ToolExecutionStarted: toolExecutionStarted(turnID, tc),
			},
		}); err != nil {
			return "", false, err
		}
	}
	inv = s.bindWorkspace(ctx, inv)
	inv.ToolContext = toolContextFrom(ctx)
	inv.TurnID = turnID
	inv.Origin = toolseam.OriginChatLoop
	// Session tools (set_status/set_session_title/add_todo/update_todo) push
	// their ConversationStreamFrame live onto this same response stream via
	// this — see internal/toolexec/session_tools.go's emitFrame/
	// WithFrameEmitter doc comments. `send` already persists to
	// SessionEventRepo and dispatches to the session hub for every other
	// frame type; wrapping ctx with it here means those tools get identical
	// treatment without this file needing to know they exist.
	dispatchCtx := toolexec.WithFrameEmitter(ctx, send)
	content, success, errMsg = toolexec.RunWithHandlers(dispatchCtx, def, inv, s.handlerSet())
	if emit && turnID != "" && send != nil {
		fin := &llmv1.EvToolExecutionFinished{TurnId: turnID, ToolCallId: tc.GetId(), Name: tc.GetName(), Success: success}
		if errMsg != "" {
			fin.ErrorMessage = proto.String(errMsg)
		}
		if ChatDebugEnabled() {
			debugLogf("tool_call exec session_id=%q turn_id=%q tool_call_id=%q name=%q phase=finish success=%v",
				sid, turnID, tc.GetId(), tc.GetName(), success)
		}
		if err = send(&llmv1.ConversationStreamFrame{
			SchemaVersion: conversationevents.SchemaVersion,
			Frame:         &llmv1.ConversationStreamFrame_ToolExecutionFinished{ToolExecutionFinished: fin},
		}); err != nil {
			return content, success, err
		}
	}
	return content, success, nil
}

// ingestToolResult runs the compression chokepoint on a tool result regardless of
// how it was executed (inline, hub-delegated, or client-delegated via ToolWaits).
// It returns the (possibly compressed) wire content. Callers must invoke this on
// every successful tool result before appending it to the message history.
// Compress-late counterpart to the RecordInvocation call at tool-finish.
func (s *LlmServer) ingestToolResult(ctx context.Context, sid, turnID string, tc *llmv1.ToolCall, content string, success bool) string {
	if !success {
		return content
	}
	sc, err := s.scope(ctx)
	if err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn,
			"ingestToolResult: session=%q tool_call=%q: no scope, compressing without tenant: %v", sid, tc.GetId(), err)
	}
	result := s.sessionService().CompressResult(ctx, toolsession.ToolInvocationEnvelope{
		Scope:      sc,
		SessionID:  sid,
		TurnID:     turnID,
		ToolCallID: tc.GetId(),
		ToolName:   tc.GetName(),
		Source:     "grpc-loop",
	}, content)
	if result.Compressed {
		s.accTurnCompression(turnID, result.OriginalTokens, result.WireTokens)
	}
	return result.Wire
}

// sessionService returns the shared per-session tool-result Service, lazily
// building one from ToolResultSidecar + SessionRepo when none was injected
// (keeps unit tests that construct LlmServer directly working).
func (s *LlmServer) sessionService() *toolsession.Service {
	s.sessionsOnce.Do(func() {
		if s.Sessions == nil {
			s.Sessions = toolsession.New(s.ToolResultSidecar, s.SessionRepo, s.DecorationsRepo)
		}
	})
	return s.Sessions
}

// agentProfiles returns the profile resolver, lazily building one over
// agentprofile.Default when none was injected (LlmServer is a struct literal
// everywhere, so there is no constructor to set it). The dynamic tier is
// AgentProfileRepo when set; a nil repo yields a nil DynamicStore interface
// (never a typed-nil pointer, which would make Resolver.useDynamic call
// methods on a nil repo).
func (s *LlmServer) agentProfiles() *agentprofile.Resolver {
	s.agentProfilesOnce.Do(func() {
		if s.AgentProfiles == nil {
			var dyn agentprofile.DynamicStore
			if s.AgentProfileRepo != nil {
				dyn = s.AgentProfileRepo
			}
			s.AgentProfiles = agentprofile.NewResolver(agentprofile.Default, dyn)
		}
	})
	return s.AgentProfiles
}

// compressToolResultsForSend runs the compression chokepoint over every role:"tool"
// message in the provider-bound prompt and returns a slice safe to send. Because
// llmd owns history server-side, prior tool results arrive via the canonical store
// (histMsgs), not the latest incoming message — so this must see the whole prompt.
//
// Compression is a SEND-TIME transformation only: the original messages are never
// mutated, because they are persisted to history as the true, uncompressed data.
// Each subsequent send replays that original history through this chokepoint, which
// deterministically rebuilds the (in-memory, non-persistent) dedup map after a
// reload. Persisting compressed back-references instead would leave dangling
// ref_tool_call_id pointers with no first occurrence to resolve against.
//
// The returned slice is the input slice itself when nothing was compressed, or a
// shallow copy with compressed messages replaced by clones (originals untouched).
// Already-compressed content (resent history carrying a banner) is skipped so we
// never double-compress or dedup a result against itself.
func (s *LlmServer) compressToolResultsForSend(ctx context.Context, aid, sid string, msgs []*llmv1.Message, settings toolresult.Settings) []*llmv1.Message {
	if s.ToolResultSidecar == nil || sid == "" || !settings.Enabled {
		return msgs
	}
	sc, err := s.scope(ctx)
	if err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn,
			"compressToolResultsForSend: session=%q: no scope, compressing without tenant: %v", sid, err)
	}
	dedup := s.sessionDedup(sid)
	out := msgs
	cloned := false
	for i, m := range msgs {
		if m == nil || m.GetRole() != "tool" || strings.TrimSpace(m.GetToolCallId()) == "" {
			continue
		}
		content := m.GetContent()
		if content == "" || toolresult.IsCompressed(content) {
			continue
		}
		// Prefer a semantic content-identity key recorded for this tool_call_id (e.g.
		// read_code range coverage) so distinct calls that returned identical content
		// collapse together. Fall back to tool_call_id, which still collapses the
		// SAME result resent each turn (llmd owns history) after the first occurrence.
		dedupKey := m.GetToolCallId()
		if k := s.sessionService().ToolDedupKey(sid, m.GetToolCallId()); k != "" {
			dedupKey = k
		}
		result := toolresult.IngestToolResult(ctx, content, toolresult.IngestIDs{
			Scope:      sc,
			SessionID:  sid,
			ToolCallID: m.GetToolCallId(),
		}, toolresult.Deps{
			Sidecar:      s.ToolResultSidecar,
			Settings:     settings,
			SessionDedup: dedup,
			DedupKey:     dedupKey,
		})
		if !result.Compressed {
			continue
		}
		// Rewrite for the send only — clone so the persisted original is untouched.
		if !cloned {
			out = append([]*llmv1.Message(nil), msgs...)
			cloned = true
		}
		clone := proto.Clone(m).(*llmv1.Message)
		clone.Content = result.Wire
		out[i] = clone
	}

	// Aging pass: collapse tool results older than the most-recent window into
	// compact, expandable stale references. In long sessions llmd resends the full
	// history every turn, so old large tool results (already acted on by the agent)
	// otherwise replay in full each turn. The original stays in the sidecar and is
	// recoverable via expand_tool_result. Send-time only — history stays canonical.
	if keep := int(settings.AgeKeepRecent); keep > 0 {
		var toolIdx []int
		for i, m := range out {
			if m != nil && m.GetRole() == "tool" && strings.TrimSpace(m.GetToolCallId()) != "" {
				toolIdx = append(toolIdx, i)
			}
		}
		if len(toolIdx) > keep {
			for _, i := range toolIdx[:len(toolIdx)-keep] {
				m := out[i]
				content := m.GetContent()
				if content == "" || toolresult.IsCompressed(content) {
					continue
				}
				wire, aged := toolresult.AgeToolResult(ctx, content, toolresult.IngestIDs{
					Scope:      sc,
					SessionID:  sid,
					ToolCallID: m.GetToolCallId(),
				}, toolresult.Deps{Sidecar: s.ToolResultSidecar, Settings: settings})
				if !aged {
					continue
				}
				if !cloned {
					out = append([]*llmv1.Message(nil), msgs...)
					cloned = true
				}
				clone := proto.Clone(out[i]).(*llmv1.Message)
				clone.Content = wire
				out[i] = clone
			}
		}
	}
	return out
}

func lastCommittedUserMessage(msgs []*llmv1.Message) *llmv1.Message {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m == nil {
			continue
		}
		if m.GetRole() == "user" {
			return m
		}
	}
	return nil
}

func assistantMessageFromReply(r *llmv1.AssistantReply) *llmv1.Message {
	if r == nil {
		return nil
	}
	out := &llmv1.Message{
		Role:             "assistant",
		Content:          r.GetContent(),
		ThinkingContent:  r.GetThinkingContent(),
		ActivityHeadline: r.GetActivityHeadline(),
		Id:               r.GetId(),
		// Carry the completion status onto the persisted row. Dropping it used to
		// erase the one durable signal that a reply was cut short: a cancelled
		// partial was stored with an empty finish_reason and came back from
		// GetSessionHistory indistinguishable from a reply the model finished.
		FinishReason: r.GetFinishReason(),
	}
	for _, tc := range r.GetToolCalls() {
		if tc == nil {
			continue
		}
		out.ToolCalls = append(out.ToolCalls, &llmv1.ToolCall{
			Id:               tc.GetId(),
			Name:             tc.GetName(),
			ArgumentsJson:    tc.GetArgumentsJson(),
			ThoughtSignature: append([]byte(nil), tc.GetThoughtSignature()...),
		})
	}
	return out
}

func ptrOrNil(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// finish_reason values llmd stamps itself when a turn does not end on the
// provider's terms (providers supply their own for healthy turns: "end_turn",
// "tool_use", ...). These are persisted on the assistant content row, which is
// what lets a client that reloaded tell a truncated reply from a complete one.
const (
	finishReasonCancelled = "cancelled"
	finishReasonTimedOut  = "timed_out"
)

// cutFinishReason maps the dead context of a cut-short turn to the finish_reason
// stamped on its salvaged partial.
//
// Note what this cannot promise: gRPC does not reliably surface a CLIENT's
// expired deadline as DeadlineExceeded on the server: the transport usually
// cancels the stream context first, so a client timeout arrives here
// indistinguishable from a user pressing stop, and is reported as "cancelled".
// The "timed_out" answer is dependable only for deadlines llmd imposes on the
// turn itself. Either way the partial is kept — which is the guarantee that
// matters; the label is a best effort.
func cutFinishReason(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return finishReasonTimedOut
	}
	return finishReasonCancelled
}

// assistantReplyFromTurnState renders a folded turn state as an AssistantReply.
// Shared by both partial-recovery sources (in-memory fold and raw-chunk replay)
// so they cannot drift apart in what they salvage.
func assistantReplyFromTurnState(state conversationevents.TurnState, finishReason string) *llmv1.AssistantReply {
	reply := &llmv1.AssistantReply{
		Id:               state.AssistantMsgID,
		Content:          state.Assistant,
		ThinkingContent:  state.Thinking,
		ActivityHeadline: state.ActivityHeadline,
		FinishReason:     finishReason,
	}
	for tcID, tc := range state.ToolCalls {
		reply.ToolCalls = append(reply.ToolCalls, &llmv1.ToolCall{
			Id:            tcID,
			Name:          tc.Name,
			ArgumentsJson: tc.ArgumentsJSON,
		})
	}
	// TurnState.ToolCalls is a map, so iteration order is random. Sort by id to
	// keep a salvaged turn byte-identical across recoveries — both so tests are
	// not flaky and so a stateful provider resuming from this row sees a stable
	// transcript.
	sort.Slice(reply.ToolCalls, func(i, j int) bool {
		return reply.ToolCalls[i].GetId() < reply.ToolCalls[j].GetId()
	})
	return reply
}

// partialWorthKeeping reports whether a salvaged turn carries anything the user
// would miss. A turn cancelled before the provider emitted any content should
// leave the transcript with just the user message rather than an empty
// assistant bubble.
func partialWorthKeeping(r *llmv1.AssistantReply) bool {
	if r == nil {
		return false
	}
	return strings.TrimSpace(r.GetContent()) != "" ||
		strings.TrimSpace(r.GetThinkingContent()) != "" ||
		len(r.GetToolCalls()) > 0
}

// reconstructPartialTurn rebuilds the assistant reply that a turn had streamed
// before it was cut short, so the caller can finalize it through the normal
// end-of-turn commit path: the partial becomes a first-class (short) turn,
// persisted to history (visible in the UI) and folded into the provider-sync
// watermark. finishReason is stamped on the result so the truncation survives
// into storage. Returns nil when there is nothing worth keeping.
//
// inMemory (folded by send() as frames were produced) is authoritative; the
// raw-chunk replay is only a fallback for when this process has no fold of its
// own. Preferring memory is what makes recovery independent of both the
// raw-chunk store being configured and the request ctx still being alive — at
// this point it is not, which is precisely when its last inserts were dropped.
// The passed ctx must therefore be a detached one (see commitCtx).
func (s *LlmServer) reconstructPartialTurn(
	ctx context.Context,
	activeTurnID, sid string,
	sc serverutil.Scope,
	inMemory conversationevents.TurnState,
	finishReason string,
) *llmv1.AssistantReply {
	// Memory needs no ids, so it is tried even when the provider never emitted
	// AssistantTurnStarted; only the raw-chunk fallback is keyed by them.
	source := "memory"
	reply := assistantReplyFromTurnState(inMemory, finishReason)
	if !partialWorthKeeping(reply) {
		reply = nil
	}
	if reply == nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn,
			"partial turn recovery: turn had no content worth keeping (memory empty): session=%q turn=%q reason=%q", sid, activeTurnID, finishReason)
		return nil
	}
	if reply.Id == "" {
		reply.Id = uuid.New().String()
	}
	llmdlog.Stdout("server", llmdlog.LevelInfo,
		"recovered partial assistant turn on cancel: scope=%s session=%q turn=%q msg_id=%q source=%q reason=%q content_len=%d",
		sc.String(), sid, activeTurnID, reply.GetId(), source, finishReason, len(reply.GetContent()))
	return reply
}

// compressionSettings returns compression configuration for a session.
// When method is NULL, compression is disabled.
// Defaults: threshold=2000, method="" (disabled).
func (s *LlmServer) compressionSettings(session *store.SessionRow) toolresult.Settings {
	return toolsession.SettingsFor(session)
}

// sessionDedup returns (or lazily creates) the SessionDedupMap for a session.
// Returns nil for an empty session id (dedup disabled, e.g. unary Chat).
func (s *LlmServer) sessionDedup(sessionID string) *toolresult.SessionDedupMap {
	return s.sessionService().Dedup(sessionID)
}

// clearSessionDedup drops the in-memory dedup state for a session (called on
// session history delete so a reused session id starts with a clean slate).
func (s *LlmServer) clearSessionDedup(sessionID string) {
	s.sessionService().ClearDedup(sessionID)
}

// accTurnCompression accumulates compression stats for a turn (for later merge into turn_metadata).
func (s *LlmServer) accTurnCompression(turnID string, origToks, wireToks int64) {
	// TODO: implement turn compression accumulator map
	_ = turnID
	_ = origToks
	_ = wireToks
}

// GetSessionTokenUsage returns aggregated token counts for a session.
func (s *LlmServer) GetSessionTokenUsage(ctx context.Context, req *llmv1.GetSessionTokenUsageRequest) (*llmv1.GetSessionTokenUsageResponse, error) {
	if s.TurnMetadata == nil {
		return &llmv1.GetSessionTokenUsageResponse{}, nil
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	sid := strings.TrimSpace(req.GetSessionId())
	if sid == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is required")
	}
	totals, err := s.TurnMetadata.GetSessionTotals(ctx, sc, sid)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "session totals: %v", err)
	}
	origToks, compToks := totals.TotalOriginal, totals.TotalCompressed
	// Prefer the sidecar rows as the source of truth for compression savings:
	// the turn-metadata compression accumulator is best-effort, but every
	// compressed tool result writes an authoritative sidecar row.
	if s.ToolResultSidecar != nil {
		if sv, sErr := s.ToolResultSidecar.SumSavingsBySession(ctx, sc, sid); sErr == nil && sv.CompressedCount > 0 {
			origToks, compToks = sv.OriginalTokens, sv.WireTokens
		}
	}
	return &llmv1.GetSessionTokenUsageResponse{
		TotalProviderInToks:   totals.TotalProviderIn,
		TotalProviderOutToks:  totals.TotalProviderOut,
		TotalOriginalTokens:   origToks,
		TotalCompressedTokens: compToks,
	}, nil
}

// ListTurnDecorations returns the session's typed turn/tool-result decorations
// (llmd_turn_decorations) in insertion order. Presentation-only metadata: the
// client joins rows onto the transcript by turn_seq (turn-level) or
// tool_call_id (result-level); nothing here ever feeds back into prompt bytes.
func (s *LlmServer) ListTurnDecorations(ctx context.Context, req *llmv1.ListTurnDecorationsRequest) (*llmv1.ListTurnDecorationsResponse, error) {
	sid := strings.TrimSpace(req.GetSessionId())
	if sid == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is required")
	}
	if s.DecorationsRepo == nil {
		return &llmv1.ListTurnDecorationsResponse{}, nil
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := s.DecorationsRepo.ListBySession(ctx, sc, sid)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list decorations: %v", err)
	}
	out := make([]*llmv1.TurnDecoration, 0, len(rows))
	for _, r := range rows {
		var seq int64
		if r.TurnSeq != nil {
			seq = *r.TurnSeq
		}
		out = append(out, &llmv1.TurnDecoration{
			TurnSeq:       seq,
			TurnId:        r.TurnID,
			Target:        r.Target,
			ToolCallId:    r.ToolCallID,
			Kind:          r.Kind,
			Uri:           r.URI,
			CreatedAtUnix: r.CreatedAt.Unix(),
		})
	}
	return &llmv1.ListTurnDecorationsResponse{Decorations: out}, nil
}

// GetSessionTurnBreakdown returns per-message token counts by joining llmd_turn_metadata
// with llmd_chat_messages. It reads directly from the DB — no in-memory agent required.
func (s *LlmServer) GetSessionTurnBreakdown(ctx context.Context, req *llmv1.GetSessionTurnBreakdownRequest) (*llmv1.GetSessionTurnBreakdownResponse, error) {
	if s.TurnMetadata == nil {
		return &llmv1.GetSessionTurnBreakdownResponse{}, nil
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	sid := strings.TrimSpace(req.GetSessionId())
	if sid == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is required")
	}
	entries, err := s.TurnMetadata.ListMessageUsage(ctx, sc, sid)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "list message usage: %v", err)
	}
	var totalIn, totalCacheRead, totalCacheWrite, totalOut int64
	// message_id -> turn_seq (from llmd_chat_messages) so each turn entry carries
	// the canonical turn_seq; the client joins turn-level graph decorations onto
	// the same bubble as the token badge by it.
	seqByMsg, err := s.TurnMetadata.MessageTurnSeqs(ctx, sc, sid)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "message turn seqs: %v", err)
	}
	turns := make([]*llmv1.TurnUsageEntry, 0, len(entries))
	seen := map[string]bool{}
	for _, e := range entries {
		if seen[e.MessageID] {
			continue
		}
		seen[e.MessageID] = true
		var durationMs int64 // 0 = not recorded
		if e.ModelDurationMs != nil {
			durationMs = *e.ModelDurationMs
		}
		turns = append(turns, &llmv1.TurnUsageEntry{
			MessageId:        e.MessageID,
			TurnSeq:          seqByMsg[e.MessageID],
			InputTokens:      e.ProviderInToks,
			OutputTokens:     e.ProviderOutToks,
			CacheReadTokens:  e.ProviderCacheReadToks,
			CacheWriteTokens: e.ProviderCacheWriteToks,
			OriginalTokens:   e.OriginalTokens,
			CompressedTokens: e.CompressedTokens,
			ModelDurationMs:  durationMs,
		})
		totalIn += e.ProviderInToks + e.ProviderCacheReadToks + e.ProviderCacheWriteToks
		totalCacheRead += e.ProviderCacheReadToks
		totalCacheWrite += e.ProviderCacheWriteToks
		totalOut += e.ProviderOutToks
	}
	return &llmv1.GetSessionTurnBreakdownResponse{
		Turns:                 turns,
		TotalInputTokens:      totalIn,
		TotalOutputTokens:     totalOut,
		TotalCacheReadTokens:  totalCacheRead,
		TotalCacheWriteTokens: totalCacheWrite,
	}, nil
}

// GetScopeTokenMetrics returns aggregated token + compression-savings totals
// for the calling scope. When AllScopes is set it sums across every scope of the
// caller's tenant for overview surfaces. Compression savings prefer the
// authoritative sidecar rows over the best-effort turn accumulator.
func (s *LlmServer) GetScopeTokenMetrics(ctx context.Context, req *llmv1.GetScopeTokenMetricsRequest) (*llmv1.GetScopeTokenMetricsResponse, error) {
	if s.TurnMetadata == nil {
		return &llmv1.GetScopeTokenMetricsResponse{}, nil
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	// all_scopes means "every scope of the caller's tenant", never every row.
	all := req.GetAllScopes()
	var totals store.SessionTotals
	if all {
		totals, err = s.TurnMetadata.GetTenantTotals(ctx, sc.TenantID)
	} else {
		totals, err = s.TurnMetadata.GetScopeTotals(ctx, sc)
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "scope totals: %v", err)
	}
	origToks, compToks, count := totals.TotalOriginal, totals.TotalCompressed, int64(0)
	if s.ToolResultSidecar != nil {
		var sv store.SessionCompressionSavings
		if all {
			sv, err = s.ToolResultSidecar.SumSavingsTenant(ctx, sc.TenantID)
		} else {
			sv, err = s.ToolResultSidecar.SumSavingsScope(ctx, sc)
		}
		if err != nil {
			llmdlog.Stdout("server", llmdlog.LevelWarn, "GetScopeTokenMetrics: sidecar savings failed: scope=%+v all=%v err=%v", sc, all, err)
		} else if sv.CompressedCount > 0 {
			origToks, compToks, count = sv.OriginalTokens, sv.WireTokens, sv.CompressedCount
		}
	}
	return &llmv1.GetScopeTokenMetricsResponse{
		TotalProviderInToks:   totals.TotalProviderIn,
		TotalProviderOutToks:  totals.TotalProviderOut,
		TotalOriginalTokens:   origToks,
		TotalCompressedTokens: compToks,
		CompressedResultCount: count,
		TotalCacheReadToks:    totals.TotalCacheRead,
		TotalCacheWriteToks:   totals.TotalCacheWrite,
	}, nil
}

// GetTokenUsageTimeline returns hourly actual provider-token usage buckets
// (in/out/cache) for the calling scope, or every scope of the caller's tenant
// when AllScopes is set. Powers the overview "tokens over time" chart. These are measured
// provider numbers, not estimates.
func (s *LlmServer) GetTokenUsageTimeline(ctx context.Context, req *llmv1.GetTokenUsageTimelineRequest) (*llmv1.GetTokenUsageTimelineResponse, error) {
	if s.TurnMetadata == nil {
		return &llmv1.GetTokenUsageTimelineResponse{}, nil
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	// all_scopes widens to the caller's whole tenant, never every row.
	filter := sc
	if req.GetAllScopes() {
		filter = serverutil.Scope{TenantID: sc.TenantID}
	}
	buckets, err := s.TurnMetadata.ListUsageBuckets(ctx, filter)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "usage timeline: %v", err)
	}
	out := make([]*llmv1.TokenUsageBucket, 0, len(buckets))
	for _, b := range buckets {
		out = append(out, &llmv1.TokenUsageBucket{
			BucketUnix:      b.Bucket.Unix(),
			ProviderInToks:  b.ProviderInToks,
			ProviderOutToks: b.ProviderOutToks,
			CacheReadToks:   b.CacheReadToks,
			CacheWriteToks:  b.CacheWriteToks,
		})
	}
	return &llmv1.GetTokenUsageTimelineResponse{Buckets: out}, nil
}

// GetTokenSavingsTimeline returns daily input-side compression-savings buckets
// (actual vs. counterfactual) for the calling scope, or every scope of the
// caller's tenant when AllScopes is set. Powers the overview "tokens over time" chart.
func (s *LlmServer) GetTokenSavingsTimeline(ctx context.Context, req *llmv1.GetTokenSavingsTimelineRequest) (*llmv1.GetTokenSavingsTimelineResponse, error) {
	if s.ToolResultSidecar == nil {
		return &llmv1.GetTokenSavingsTimelineResponse{}, nil
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	// all_scopes widens to the caller's whole tenant, never every row.
	filter := sc
	if req.GetAllScopes() {
		filter = serverutil.Scope{TenantID: sc.TenantID}
	}
	buckets, err := s.ToolResultSidecar.ListSavingsBuckets(ctx, filter)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "savings timeline: %v", err)
	}
	out := make([]*llmv1.TokenSavingsBucket, 0, len(buckets))
	for _, b := range buckets {
		out = append(out, &llmv1.TokenSavingsBucket{
			DayUnix:          b.Bucket.Unix(),
			OriginalTokens:   b.OriginalTokens,
			CompressedTokens: b.WireTokens,
		})
	}
	return &llmv1.GetTokenSavingsTimelineResponse{Buckets: out}, nil
}

// GetToolResultOriginal returns the original uncompressed content of a tool result.
func (s *LlmServer) GetToolResultOriginal(ctx context.Context, req *llmv1.GetToolResultOriginalRequest) (*llmv1.GetToolResultOriginalResponse, error) {
	if s.ToolResultSidecar == nil {
		return &llmv1.GetToolResultOriginalResponse{Found: false}, nil
	}
	toolCallID := strings.TrimSpace(req.GetToolCallId())
	if toolCallID == "" {
		return nil, status.Error(codes.InvalidArgument, "tool_call_id is required")
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	sid := strings.TrimSpace(req.GetSessionId())
	row, err := s.ToolResultSidecar.GetByToolCallID(ctx, sc, sid, toolCallID)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "sidecar lookup: %v", err)
	}
	if row.ID == 0 {
		// Row not found
		return &llmv1.GetToolResultOriginalResponse{Found: false}, nil
	}
	return &llmv1.GetToolResultOriginalResponse{
		OriginalContent: row.Original,
		Found:           true,
	}, nil
}

// isNotFound reports whether err indicates a missing record (GORM ErrRecordNotFound or nil row).
func isNotFound(err error) bool {
	return errors.Is(err, gorm.ErrRecordNotFound)
}

// UpdateSessionCompressionSettings updates per-session compression configuration.
func (s *LlmServer) UpdateSessionCompressionSettings(ctx context.Context, req *llmv1.UpdateSessionCompressionSettingsRequest) (*llmv1.UpdateSessionCompressionSettingsResponse, error) {
	sid := strings.TrimSpace(req.GetSessionId())
	if sid == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is required")
	}

	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}

	// Master enable flag + minify/dedup mechanism flag (explicit when provided;
	// nil leaves the existing stored value untouched).
	var enabledPtr *bool
	if req.Enabled != nil {
		v := req.GetEnabled()
		enabledPtr = &v
	}
	var minifyPtr *bool
	if req.MinifyDedup != nil {
		v := req.GetMinifyDedup()
		minifyPtr = &v
	}

	threshold := req.GetThreshold()
	if threshold == 0 {
		threshold = 2000 // default
	}
	if threshold < 100 {
		return nil, status.Error(codes.InvalidArgument, "threshold must be >= 100")
	}

	// Aging window: explicit (including 0 = disabled) when provided; nil leaves the default.
	var ageKeepPtr *int64
	if req.AgeKeep != nil {
		v := req.GetAgeKeep()
		if v < 0 || v > 1000 {
			return nil, status.Error(codes.InvalidArgument, "age_keep must be between 0 and 1000")
		}
		ageKeepPtr = &v
	}

	// Search-summary flag: explicit when provided; nil leaves existing value.
	var extractiveSearchPtr *bool
	if req.ExtractiveSearch != nil {
		v := req.GetExtractiveSearch()
		extractiveSearchPtr = &v
	}

	// Update session compression settings via the session repo.
	if s.SessionRepo == nil {
		return nil, status.Error(codes.FailedPrecondition, "session persistence not configured")
	}

	// Get the current session — it may not exist yet if the user enabled compression
	// before sending the first message (session is created lazily on first EnsureSession).
	// In that case we write a stub row with just the compression settings; when the real
	// EnsureSession fires it uses EnsureSessionRow (INSERT ON CONFLICT DO UPDATE on runtime
	// cols only) and preserves what we set here.
	thresholdPtr := proto.Int64(threshold)

	session, err := s.SessionRepo.GetSession(ctx, sc, sid)
	if err != nil && !isNotFound(err) {
		return nil, status.Errorf(codes.Internal, "failed to get session: %v", err)
	}

	if session == nil {
		// Session doesn't exist yet — write a stub so compression is ready on first turn.
		// No FK/workspace-row prerequisite: llmd_sessions has no foreign key on
		// workspace_id (nothing in the schema references llmd_users, which no
		// longer exists).
		stub := store.SessionRow{
			TenantID:                       sc.TenantID,
			ScopeID:                        sc.ScopeID,
			SessionID:                      sid,
			ToolResultCompressionEnabled:   enabledPtr,
			ToolResultMinifyDedup:          minifyPtr,
			ToolResultCompressionThreshold: thresholdPtr,
			ToolResultAgeKeep:              ageKeepPtr,
			ToolResultExtractiveSearch:     extractiveSearchPtr,
		}
		if err := s.SessionRepo.UpsertSession(ctx, stub); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to create session stub: %v", err)
		}
		llmdlog.Stdout("server", llmdlog.LevelInfo,
			"compression stub created: session=%q enabled=%v threshold=%d", sid, enabledPtr != nil && *enabledPtr, threshold)
	} else {
		if enabledPtr != nil {
			session.ToolResultCompressionEnabled = enabledPtr
		}
		if minifyPtr != nil {
			session.ToolResultMinifyDedup = minifyPtr
		}
		session.ToolResultCompressionThreshold = thresholdPtr
		if ageKeepPtr != nil {
			session.ToolResultAgeKeep = ageKeepPtr
		}
		if extractiveSearchPtr != nil {
			session.ToolResultExtractiveSearch = extractiveSearchPtr
		}
		if err := s.SessionRepo.UpsertSession(ctx, *session); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to update session: %v", err)
		}
		llmdlog.Stdout("server", llmdlog.LevelInfo,
			"compression settings updated: session=%q enabled=%v threshold=%d", sid, enabledPtr != nil && *enabledPtr, threshold)
	}

	return &llmv1.UpdateSessionCompressionSettingsResponse{
		Success:          true,
		Enabled:          enabledPtr,
		Threshold:        thresholdPtr,
		AgeKeep:          ageKeepPtr,
		ExtractiveSearch: extractiveSearchPtr,
		MinifyDedup:      minifyPtr,
	}, nil
}
