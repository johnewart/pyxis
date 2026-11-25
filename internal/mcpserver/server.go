package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/johnewart/pyxis/agentpid"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"github.com/johnewart/pyxis/internal/assistant/session"
	"github.com/johnewart/pyxis/internal/policyhook"
	"github.com/johnewart/pyxis/internal/serve"
	"github.com/johnewart/pyxis/internal/server"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/sessionmanager"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/internal/toolargs"
	"github.com/johnewart/pyxis/internal/tooldelegate"
	"github.com/johnewart/pyxis/internal/toolexec"
	"github.com/johnewart/pyxis/toolseam"
	"github.com/johnewart/pyxis/internal/toolmerge"
	"github.com/johnewart/pyxis/internal/toolprovider"
	"github.com/johnewart/pyxis/internal/toolsession"
	"github.com/johnewart/pyxis/llmdlog"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
)

// ContextResourceURI is the MCP resource URI for scoped JSON context (agent tools, memories, optional session history).
const ContextResourceURI = "llmd://context"

// LogModule is the llmdlog `module` field for every structured line emitted by this MCP HTTP server (host diagnostics filter).
const LogModule = "mcpserver"

// DelegatedResultIngestor is the minimal surface the MCP delegated-tool path
// needs from the shared per-session tool-result pipeline: record + compress one
// delegated tool result (keyed by the ToolInvocationEnvelope). *toolsession.Service
// satisfies it in production; tests inject a spy to assert the envelope handed off
// at dispatch (e.g. the stamped turn_seq) without the full compression stack.
type DelegatedResultIngestor interface {
	IngestDelegatedResult(ctx context.Context, env toolsession.ToolInvocationEnvelope, content string, isError bool) string
}

// Config configures the HTTP MCP JSON-RPC handler.
type Config struct {
	DB              *gorm.DB
	SessionRuntimes *sessionmanager.Manager
	LLM             *server.LlmServer
	// Sessions is the shared per-session tool-result pipeline (same instance the
	// gRPC server holds). Delegated tool results are compressed/deduped through it
	// so MCP and ChatStream share dedup state and sidecar persistence.
	Sessions DelegatedResultIngestor
	// AgentPIDRegistry resolves Cursor agent parent PID → chat scope for mcp-stdio-bridge (optional).
	AgentPIDRegistry *agentpid.Registry
	// BearerToken, if non-empty, requires Authorization: Bearer <token> on each request.
	BearerToken string
	// PolicyHooks, if non-nil, is consulted (CheckToolCall) before every tools/call dispatch.
	PolicyHooks policyhook.Hooks
}

// Server serves MCP over HTTP (JSON-RPC 2.0 POST /mcp).
type Server struct {
	cfg Config
}

// New constructs an MCP HTTP handler.
func New(cfg Config) *Server {
	return &Server{cfg: cfg}
}

// Handler returns an http.Handler with MCP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /mcp/session-by-ppid", http.HandlerFunc(s.handleSessionByPPID))
	mux.Handle("POST /mcp", http.HandlerFunc(s.handleMCP))
	mux.Handle("GET /mcp/health", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	return mux
}

// handleSessionByPPID maps the Cursor agent process parent PID (stdio MCP bridge's getppid) to chat scope.
func (s *Server) handleSessionByPPID(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	reg := s.cfg.AgentPIDRegistry
	if reg == nil {
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte(`{"error":"agent pid registry not configured"}`))
		return
	}
	ppidStr := strings.TrimSpace(r.URL.Query().Get("ppid"))
	ppid, err := strconv.Atoi(ppidStr)
	if err != nil || ppid <= 0 {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid or missing ppid"}`))
		return
	}
	run, ok := reg.Lookup(ppid)
	if !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":"unknown agent pid"}`))
		return
	}
	out := map[string]string{
		"sessionId": run.SessionID,
		"tenantId":  run.Scope.TenantID,
		"agentId":   run.AgentID,
	}
	if s := strings.TrimSpace(run.Scope.ScopeID); s != "" {
		out["scopeId"] = s
	}
	if w := strings.TrimSpace(run.ClientWorkspaceID); w != "" {
		out["clientWorkspaceId"] = w
	}
	b, err := json.Marshal(out)
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

type jsonRPCRequest struct {
	JSONRPC string           `json:"jsonrpc"`
	ID      *json.RawMessage `json:"id,omitempty"`
	Method  string           `json:"method"`
	Params  json.RawMessage  `json:"params"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

func (s *Server) handleMCP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	if tok := strings.TrimSpace(s.cfg.BearerToken); tok != "" {
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		want := "Bearer " + tok
		if auth != want {
			writeRPCError(w, nil, -32001, "unauthorized", http.StatusUnauthorized)
			return
		}
	}
	var req jsonRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeRPCError(w, nil, -32700, "parse error", http.StatusBadRequest)
		return
	}
	if req.JSONRPC != "2.0" {
		writeRPCError(w, req.ID, -32600, "invalid request: jsonrpc must be 2.0", http.StatusBadRequest)
		return
	}
	// JSON-RPC notification: no id -> no response body (HTTP 204).
	if req.ID == nil {
		if strings.HasPrefix(req.Method, "notifications/") {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		writeRPCError(w, nil, -32600, "invalid request: missing id", http.StatusBadRequest)
		return
	}
	ctx := r.Context()
	switch req.Method {
	case "initialize":
		s.handleInitialize(w, r, req)
	case "tools/list":
		s.handleToolsList(ctx, w, r, req)
	case "tools/call":
		s.handleToolsCall(ctx, w, r, req)
	case "resources/list":
		s.handleResourcesList(ctx, w, r, req)
	case "resources/read":
		s.handleResourcesRead(ctx, w, r, req)
	case "session/advanceTurn":
		s.handleAdvanceTurn(ctx, w, r, req)
	default:
		writeRPCError(w, req.ID, -32601, "method not found: "+req.Method, http.StatusNotFound)
	}
}

func (s *Server) handleInitialize(w http.ResponseWriter, r *http.Request, req jsonRPCRequest) {
	if serve.DebugEnabled() {
		sc, agentID, sessionID, err := s.resolveAgentScope(r.Context(), r)
		var payload map[string]any
		if err != nil {
			payload = map[string]any{
				"event":          "mcp_initialize",
				"remote_addr":    r.RemoteAddr,
				"scope_resolved": false,
				"scope_error":    err.Error(),
			}
		} else {
			payload = map[string]any{
				"event":          "mcp_initialize",
				"remote_addr":    r.RemoteAddr,
				"scope_resolved": true,
				"scope":          sc.String(),
				"agent_id":       agentID,
				"session_id":     sessionID,
			}
		}
		if b, jerr := json.Marshal(payload); jerr != nil {
			llmdlog.Stdout(LogModule, llmdlog.LevelWarn, "initialize: marshal debug payload failed: session=%q err=%v", sessionID, jerr)
		} else {
			llmdlog.Stdout(LogModule, llmdlog.LevelDebug, "%s", string(b))
		}
	}
	resp := map[string]any{
		"protocolVersion": "2024-11-05",
		"capabilities": map[string]any{
			"tools":     map[string]any{},
			"resources": map[string]any{},
		},
		"serverInfo": map[string]any{
			"name":    "llmd",
			"version": "0.0.1",
		},
	}
	writeRPCResult(w, req.ID, resp)
}

func (s *Server) handleToolsList(ctx context.Context, w http.ResponseWriter, r *http.Request, req jsonRPCRequest) {
	sc, agentID, sessionID, err := s.resolveAgentScope(ctx, r)
	if err != nil {
		writeScopeError(w, req.ID, err)
		return
	}
	ma, ok := s.cfg.SessionRuntimes.Get(sc, agentID)
	if !ok {
		writeRPCError(w, req.ID, -32002, "agent not loaded", http.StatusNotFound)
		return
	}
	if ma.Scope != sc {
		writeRPCError(w, req.ID, -32002, "agent not found for user", http.StatusNotFound)
		return
	}
	// Policy FilterTools is intentionally not applied to tools/list: this list
	// is built from the session's effective tool set, which is not the per-round
	// offered set the agent loop passes to FilterTools. handleToolsCall enforces
	// policy per call with CheckToolCall (entry path MCP_SERVER) instead, so a
	// tool a hook forbids is denied at dispatch rather than hidden here.
	tools := toolmerge.RedactSecretsForList(s.effectiveTools(ma))
	if s.readOnlyForMCP(r) {
		tools = toolmerge.FilterForReadOnly(tools)
	}
	if s.toolsDisabledForTurn(sc, agentID, sessionID) {
		llmdlog.Stdout(LogModule, llmdlog.LevelInfo, "tools/list: tools disabled for this turn; advertising none: session=%q", sessionID)
		tools = nil
	}
	if serve.DebugEnabled() {
		names := make([]string, 0, len(tools))
		for _, t := range tools {
			if t == nil {
				continue
			}
			if n := strings.TrimSpace(t.GetName()); n != "" {
				names = append(names, n)
			}
		}
		payload := map[string]any{
			"event":                 "mcp_tools_list",
			"remote_addr":           r.RemoteAddr,
			"user_id":               sc.TenantID, // wire key renamed in stage 4
			"agent_id":              agentID,
			"agent_display_name":    strings.TrimSpace(ma.DisplayName),
			"session_id":            sessionID,
			"advertised_tool_count": len(names),
			"tools":                 names,
		}
		if b, jerr := json.Marshal(payload); jerr != nil {
			llmdlog.Stdout(LogModule, llmdlog.LevelWarn, "tools/list: marshal debug payload failed: session=%q err=%v", sessionID, jerr)
		} else {
			llmdlog.Stdout(LogModule, llmdlog.LevelDebug, "%s", string(b))
		}
	}
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		if t == nil {
			continue
		}
		schema := map[string]any{"type": "object"}
		if j := strings.TrimSpace(t.GetParametersSchemaJson()); j != "" {
			var parsed any
			if err := json.Unmarshal([]byte(j), &parsed); err == nil {
				if m, ok := parsed.(map[string]any); ok {
					schema = m
				}
			}
		}
		out = append(out, map[string]any{
			"name":        t.GetName(),
			"description": t.GetDescription(),
			"inputSchema": schema,
		})
	}
	writeRPCResult(w, req.ID, map[string]any{"tools": out})
}

type toolsCallParams struct {
	Name      string         `json:"name"`
	Arguments map[string]any `json:"arguments"`
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

// publishToolEventFrame gives MCP-dispatched tool calls (Claude Code CLI,
// Cursor CLI, and any other agentic driver reaching llmd only via
// tools/call — see handleToolsCall's module doc) the same ToolExecution
// visibility native/OpenAI-style backends get for free from ChatStream's own
// send closure (internal/server/server.go): it multicasts the frame to any
// live subscriber of this session (hub.Dispatch — the same *session.Hub
// ChatStream reads via SessionRuntimes.Get(agentID).Core) and persists it
// for replay/SubscribeSessionEvents (SessionEvents.AppendEvent), exactly
// mirroring the two side effects ChatStream's frame loop performs per frame.
// Best-effort: a nil hub/SessionEvents (not fully configured, e.g. some
// tests) silently no-ops rather than failing the tool call itself — the
// dispatched-tool-result path (respond/IngestDelegatedResult) is the actual
// source of truth here, this is purely additive UI/observability.
func (s *Server) publishToolEventFrame(ctx context.Context, agent *session.Agent, sc serverutil.Scope, sessionID string, frame *llmv1.ConversationStreamFrame) {
	if frame == nil {
		return
	}
	// Append first, then poke: subscribers re-read the event log when the hub
	// pokes them, so the row has to exist by then (see internal/sessionevents).
	if s.cfg.LLM != nil && s.cfg.LLM.SessionEvents != nil && sessionID != "" && sc.TenantID != "" {
		if eventJSON, err := protojson.Marshal(frame); err != nil {
			llmdlog.Stdout("mcpserver", llmdlog.LevelWarn,
				"tool event frame marshal failed %s session=%q: %v", sc.String(), sessionID, err)
		} else if _, err := s.cfg.LLM.SessionEvents.AppendEvent(ctx, sc, sessionID, string(eventJSON)); err != nil {
			llmdlog.Stdout("mcpserver", llmdlog.LevelWarn,
				"tool event frame persist failed %s session=%q: %v", sc.String(), sessionID, err)
		}
	}
	if agent != nil && sessionID != "" {
		agent.Session(sessionID).Dispatch(frame)
	}
}

// handleToolsCall runs registered tools using the same execution layer as gRPC (webhook, delegate_to_client via SubmitToolResults, etc.).
func (s *Server) handleToolsCall(ctx context.Context, w http.ResponseWriter, r *http.Request, req jsonRPCRequest) {
	var p toolsCallParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			writeRPCError(w, req.ID, -32602, "invalid params: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	if strings.TrimSpace(p.Name) == "" {
		writeRPCError(w, req.ID, -32602, "tool name is required", http.StatusBadRequest)
		return
	}
	// tool.execute span: tool name only, never arguments or results. The
	// tracer is nil when tracing is off.
	if s.cfg.LLM != nil && s.cfg.LLM.Tracer != nil {
		var span trace.Span
		ctx, span = s.cfg.LLM.Tracer.Start(ctx, "tool.execute",
			trace.WithAttributes(attribute.String("llmd.tool.name", strings.TrimSpace(p.Name))))
		defer span.End()
	}
	sc, agentID, sessionID, err := s.resolveAgentScope(ctx, r)
	if err != nil {
		writeScopeError(w, req.ID, err)
		return
	}
	ma, ok := s.cfg.SessionRuntimes.Get(sc, agentID)
	if !ok {
		writeRPCError(w, req.ID, -32002, "agent not loaded", http.StatusNotFound)
		return
	}
	if ma.Scope != sc {
		writeRPCError(w, req.ID, -32002, "agent not found for user", http.StatusNotFound)
		return
	}
	// Resolving the call against the EFFECTIVE set is what makes this an
	// enforcement point: a tool the session may not use is absent here, so
	// naming it anyway is refused rather than dispatched.
	if s.toolsDisabledForTurn(sc, agentID, sessionID) {
		llmdlog.Stdout(LogModule, llmdlog.LevelWarn,
			"tools/call refused: tools disabled for this turn: tool=%q session=%q", strings.TrimSpace(p.Name), sessionID)
		writeToolCallContent(w, req.ID, server.NoToolsTurnRefusal, true)
		return
	}
	merged := s.effectiveTools(ma)
	def := mergedToolByName(merged, p.Name)
	if def == nil {
		writeRPCError(w, req.ID, -32602, "unknown tool: "+strings.TrimSpace(p.Name), http.StatusBadRequest)
		return
	}
	if s.readOnlyForMCP(r) && toolmerge.IsMutationTool(def) {
		writeToolCallContent(w, req.ID, toolexec.ReadOnlyMutationError, true)
		return
	}
	args := p.Arguments
	if args == nil {
		args = map[string]any{}
	}
	argsJSON, err := json.Marshal(args)
	if err != nil {
		writeRPCError(w, req.ID, -32602, "invalid arguments", http.StatusBadRequest)
		return
	}
	if s.cfg.PolicyHooks != nil {
		polTurnID := ""
		if s.cfg.LLM != nil && s.cfg.LLM.TurnRegister != nil {
			polTurnID = s.cfg.LLM.TurnRegister.CurrentTurnID(sc, sessionID)
		}
		verdict, herr := s.cfg.PolicyHooks.CheckToolCall(ctx, policyhook.ToolCallRequest{
			AgentID:       agentID,
			SessionID:     sessionID,
			TurnID:        polTurnID,
			ReadOnly:      s.readOnlyForMCP(r),
			Name:          def.GetName(),
			ArgumentsJSON: string(argsJSON),
			Tool:          def,
			Mutation:      toolmerge.IsMutationTool(def),
			Delegated:     def.GetDelegateToClient() != nil,
			EntryPath:     llmv1.PolicyEntryPath_POLICY_ENTRY_PATH_MCP_SERVER,
		})
		if herr != nil {
			llmdlog.Stdout(LogModule, llmdlog.LevelWarn,
				"policy CheckToolCall failed, denying: tool=%q session=%q: %v", def.GetName(), sessionID, herr)
			writeToolCallContent(w, req.ID, "tool call denied: policy check failed: "+herr.Error(), true)
			return
		}
		if verdict.Decision == policyhook.DecisionDeny {
			msg := verdict.ReplacementToolResult
			if msg == "" {
				msg = verdict.Reason
			}
			if msg == "" {
				msg = "tool call denied by policy"
			}
			writeToolCallContent(w, req.ID, msg, true)
			return
		}
	}
	// Captured at invocation start (before any tool dispatch below) so the
	// envelope reflects the turn active when this call BEGAN, even if
	// TurnRegister.Advance runs again while the tool is still in-flight.
	var turnSeq int64
	if s.cfg.LLM != nil && s.cfg.LLM.TurnRegister != nil {
		turnSeq = s.cfg.LLM.TurnRegister.Current(sc, sessionID)
	}
	toolCallID := "mcp-" + uuid.New().String()

	// Same ToolExecutionStarted/Finished visibility native/OpenAI-style
	// backends get for free from ChatStream's own send closure (see
	// publishToolEventFrame's doc) — MCP-dispatched tool calls (Claude Code
	// CLI, Cursor CLI, ...) previously never emitted these frames at all,
	// so a client subscribed to ChatStream/SubscribeSessionEvents saw
	// thinking/text deltas but no sign a tool ran. turnID comes from
	// TurnRegister.CurrentTurnID, populated by ChatStream's own turn
	// advance or the MCP-transport handleAdvanceTurn fallback (see its
	// module doc) — "" if neither has run yet for this session.
	var turnID string
	if s.cfg.LLM != nil && s.cfg.LLM.TurnRegister != nil {
		turnID = s.cfg.LLM.TurnRegister.CurrentTurnID(sc, sessionID)
	}
	agent := ma.Core // ma is non-nil here: checked by the `!ok` return above
	// Arguments ride along so the UI can describe the call, not just name it.
	// This frame is the ONLY one carrying them for MCP-dispatched tools: the
	// model-side tool call happened inside the CLI, so llmd never sees (and
	// never emits) the EvToolCallStreamFinished that native backends produce.
	startedArgs, argsTruncated := toolargs.ForDisplay(string(argsJSON))
	started := &llmv1.EvToolExecutionStarted{
		TurnId:     turnID,
		ToolCallId: toolCallID,
		Name:       def.GetName(),
	}
	if startedArgs != "" {
		started.ArgumentsJson = proto.String(startedArgs)
		if argsTruncated {
			started.ArgumentsTruncated = proto.Bool(true)
		}
	}
	s.publishToolEventFrame(ctx, agent, sc, sessionID, &llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_ToolExecutionStarted{
			ToolExecutionStarted: started,
		},
	})

	inv := toolexec.Invocation{
		ToolCallID:    toolCallID,
		Name:          def.GetName(),
		ArgumentsJSON: string(argsJSON),
		AgentID:       agentID,
		SessionID:     sessionID,
		// Scope must be stamped here: RunWithHandlers overwrites inv with
		// exactly this struct, and inline-builtin handlers resolve their
		// per-scope dependency solely from inv.Scope. sc is the binding
		// resolveAgentScope already validated above — leaving this unset
		// silently starves every MCP-dispatched tool of its scope.
		Scope: sc,
	}
	// respond routes successful tool results through the single
	// IngestDelegatedResult chokepoint (dedup-key recording + compression)
	// before returning them to the caller. CLI/agentic providers reach tools
	// only via this MCP path (never the gRPC tool loop), so this is the
	// chokepoint for their results. Keyed by sessionID, so dedup state is
	// shared with ChatStream. Errors pass through untouched. source
	// identifies which MCP exit point produced the result ("mcp-hub",
	// "mcp-http", or "mcp-inline" for locally-executed, non-delegated tools).
	toolStarted := time.Now()
	toolOK := false
	defer func() {
		s.cfg.LLM.RecordToolUsageScoped(ctx, sc, def.GetName(), server.ToolUsageSourceMCP, sessionID, toolOK, time.Since(toolStarted))
	}()
	respond := func(text string, isErr bool, dedupKey, dedupSummary, source string, decorations []toolsession.Decoration) {
		toolOK = !isErr
		if !isErr && s.cfg.Sessions != nil {
			text = s.cfg.Sessions.IngestDelegatedResult(ctx, toolsession.ToolInvocationEnvelope{
				Scope:        sc,
				SessionID:    sessionID,
				ToolCallID:   toolCallID,
				ToolName:     p.Name,
				TurnSeq:      turnSeq,
				Source:       source,
				DedupKey:     dedupKey,
				DedupSummary: dedupSummary,
				Decorations:  decorations,
			}, text, isErr)
		}
		// ToolExecutionFinished pairs with the ToolExecutionStarted emitted
		// above, on every exit path (all of them funnel through respond).
		// errorMessage mirrors the native tool loop's shape
		// (internal/server/server.go): only set when isErr, and it's the
		// tool's own error text, not the (possibly rewritten-by-compression)
		// success text.
		finished := &llmv1.EvToolExecutionFinished{
			TurnId:     turnID,
			ToolCallId: toolCallID,
			Name:       def.GetName(),
			Success:    !isErr,
		}
		if isErr {
			finished.ErrorMessage = proto.String(text)
		}
		s.publishToolEventFrame(ctx, agent, sc, sessionID, &llmv1.ConversationStreamFrame{
			SchemaVersion: conversationevents.SchemaVersion,
			Frame:         &llmv1.ConversationStreamFrame_ToolExecutionFinished{ToolExecutionFinished: finished},
		})
		writeToolCallContent(w, req.ID, text, isErr)
	}

	var text string
	var isError bool
	if def.GetDelegateToClient() != nil {
		wsID := strings.TrimSpace(r.Header.Get("X-Client-Workspace-Id"))
		if wsID == "" && s.cfg.AgentPIDRegistry != nil {
			if ppidStr := strings.TrimSpace(r.Header.Get("X-Llmd-Agent-Ppid")); ppidStr != "" {
				if ppid, aerr := strconv.Atoi(ppidStr); aerr == nil && ppid > 0 {
					if run, ok := s.cfg.AgentPIDRegistry.Lookup(ppid); ok {
						wsID = strings.TrimSpace(run.ClientWorkspaceID)
					}
				}
			}
		}
		if wsID != "" && s.cfg.LLM != nil && s.cfg.LLM.ToolProviderHub != nil && s.cfg.LLM.ToolProviderHub.HasProvider(sc) {
			dur := toolexec.DelegateWaitDuration(def)
			txt, isErr, dk, atts, herr := s.cfg.LLM.ToolProviderHub.Invoke(ctx, sc, agentID, sessionID, wsID, "", p.Name, string(argsJSON), dur)
			if herr == nil {
				// hub.Invoke returns no dedupSummary (not an accidental drop) — the
				// explicit "" carries that intent. Attachments ride the envelope so
				// they're recorded through the same IngestDelegatedResult
				// chokepoint and inherit turnSeq — no separate, turn-less call.
				respond(txt, isErr, dk, "", "mcp-hub", toDecorations(atts))
				return
			}
			if !errors.Is(herr, toolprovider.ErrNoProvider) {
				writeRPCError(w, req.ID, -32000, herr.Error(), http.StatusInternalServerError)
				return
			}
		}
		tw := s.cfg.LLM.ToolWaits
		if tw == nil {
			writeToolCallContent(w, req.ID, "client-delegated tools are not enabled (ToolWaits not configured)", true)
			return
		}
		nonce := uuid.New().String()
		ch := tw.Register(nonce, agentID, toolCallID, sc)
		res, werr := tw.Wait(ctx, nonce, ch, toolexec.DelegateWaitDuration(def))
		switch {
		case werr == nil:
			text = res.Content
			isError = res.IsError
		case errors.Is(werr, tooldelegate.ErrWaitTimeout):
			text, isError = "client tool execution timed out", true
		case errors.Is(werr, tooldelegate.ErrWaitAborted):
			text, isError = "client tool execution was cancelled", true
		default:
			writeRPCError(w, req.ID, -32000, werr.Error(), http.StatusInternalServerError)
			return
		}
	} else {
		var ok bool
		var em string
		// Dispatch through the LlmServer's own explicit handler set — never a
		// process-wide global — so this MCP-proxied path and the gRPC tool
		// loop can never observe different registrations.
		var handlers *toolexec.Handlers
		if s.cfg.LLM != nil {
			handlers = s.cfg.LLM.Handlers
		}
		if handlers == nil {
			handlers = toolexec.NewHandlers()
		}
		// Session tools (set_session_title/add_todo/set_status, see
		// internal/toolexec/session_tools.go's emitFrame) only reach the
		// live session hub when a frame emitter is on ctx — without this,
		// they'd fall back to persisting straight to SessionEventRepo with
		// no hub.Dispatch, so a live SubscribeSessionEvents subscriber
		// would miss them entirely (replay-only, same gap
		// publishToolEventFrame above already closes for
		// ToolExecutionStarted/Finished). Wiring the same
		// hub.Dispatch+persist behavior in here makes the hub authoritative
		// for every frame this MCP dispatch produces, not just tool-execution
		// ones.
		dispatchCtx := toolexec.WithFrameEmitter(ctx, func(frame *llmv1.ConversationStreamFrame) error {
			s.publishToolEventFrame(ctx, agent, sc, sessionID, frame)
			return nil
		})
		inv.TurnID = turnID
		inv.Origin = toolseam.OriginMCP
		text, ok, em = toolexec.RunWithHandlers(dispatchCtx, def, inv, handlers)
		isError = !ok
		if em != "" && !ok && text == "" {
			text = em
		}
	}
	respond(text, isError, "", "", "mcp-inline", nil)
}

// handleAdvanceTurn is the MCP-transport fallback turn boundary: llmd's
// ChatStream is the gRPC turn-start chokepoint, but MCP-driven sessions
// (Claude Code CLI, Cursor CLI, and other agentic drivers reaching llmd only
// via tools/call) run their own conversation loop entirely client-side --
// llmd never observes a persisted user-message append for these sessions (no
// driver reliably calls back into llmd's chat history at turn boundaries).
// Tracing the MCP lifecycle confirmed no such hook exists, so the driver (or
// the MCP bridge wrapping it) calls this scoped RPC once per new user turn
// instead. It shares the SAME TurnRegister instance as the gRPC ChatStream
// chokepoint (via cfg.LLM.TurnRegister) so turn_seq stays gap-free and
// collision-free across both transports for a given session, while remaining
// transport-scoped: nothing here touches store.History.Store or the gRPC
// send closure, so a session driven by both transports can't double-advance
// from this path alone.
type advanceTurnParams struct {
	TurnID string `json:"turnId"`
}

func (s *Server) handleAdvanceTurn(ctx context.Context, w http.ResponseWriter, r *http.Request, req jsonRPCRequest) {
	var p advanceTurnParams
	if len(req.Params) > 0 {
		if err := json.Unmarshal(req.Params, &p); err != nil {
			writeRPCError(w, req.ID, -32602, "invalid params: "+err.Error(), http.StatusBadRequest)
			return
		}
	}
	sc, _, sessionID, err := s.resolveAgentScope(ctx, r)
	if err != nil {
		writeScopeError(w, req.ID, err)
		return
	}
	if strings.TrimSpace(sessionID) == "" {
		writeRPCError(w, req.ID, -32602, "X-Llmd-Session-Id is required to advance a turn", http.StatusBadRequest)
		return
	}
	if s.cfg.LLM == nil || s.cfg.LLM.TurnRegister == nil {
		writeRPCError(w, req.ID, -32000, "turn register not configured", http.StatusInternalServerError)
		return
	}
	seq, aerr := s.cfg.LLM.TurnRegister.Advance(ctx, sc, sessionID, time.Now().UTC(), p.TurnID)
	if aerr != nil {
		writeRPCError(w, req.ID, -32000, aerr.Error(), http.StatusInternalServerError)
		return
	}
	writeRPCResult(w, req.ID, map[string]any{"turnSeq": seq})
}

func mergedToolByName(defs []*llmv1.ToolDefinition, name string) *llmv1.ToolDefinition {
	n := strings.TrimSpace(name)
	for _, d := range defs {
		if d == nil {
			continue
		}
		if strings.TrimSpace(d.GetName()) == n {
			return d
		}
	}
	return nil
}

func writeToolCallContent(w http.ResponseWriter, id *json.RawMessage, text string, isError bool) {
	writeRPCResult(w, id, map[string]any{
		"content": []map[string]any{
			{"type": "text", "text": text},
		},
		"isError": isError,
	})
}

func (s *Server) handleResourcesList(ctx context.Context, w http.ResponseWriter, r *http.Request, req jsonRPCRequest) {
	sc, agentID, sessionID, err := s.resolveAgentScope(ctx, r)
	if err != nil {
		writeScopeError(w, req.ID, err)
		return
	}
	ma, ok := s.cfg.SessionRuntimes.Get(sc, agentID)
	if !ok || ma.Scope != sc {
		writeRPCError(w, req.ID, -32002, "session not found for scope", http.StatusNotFound)
		return
	}
	_ = sessionID // listed resource is always llmd://context; session tail is optional in the payload
	res := []map[string]any{
		{
			"uri":         ContextResourceURI,
			"name":        "llmd session context",
			"mimeType":    "application/json",
			"description": "Session metadata, tools, memories, and optional session chat excerpt for this workspace.",
		},
	}
	writeRPCResult(w, req.ID, map[string]any{"resources": res})
}

type resourcesReadParams struct {
	URI string `json:"uri"`
}

func (s *Server) handleResourcesRead(ctx context.Context, w http.ResponseWriter, r *http.Request, req jsonRPCRequest) {
	var p resourcesReadParams
	if len(req.Params) > 0 {
		_ = json.Unmarshal(req.Params, &p)
	}
	if strings.TrimSpace(p.URI) != ContextResourceURI {
		writeRPCError(w, req.ID, -32602, "unknown resource uri (expected "+ContextResourceURI+")", http.StatusBadRequest)
		return
	}
	sc, agentID, sessionID, err := s.resolveAgentScope(ctx, r)
	if err != nil {
		writeScopeError(w, req.ID, err)
		return
	}
	payload, err := s.buildContextJSON(ctx, sc, agentID, sessionID)
	if err != nil {
		llmdlog.Stdout(LogModule, llmdlog.LevelWarn, "context build: %v", err)
		writeRPCError(w, req.ID, -32000, "internal error building context", http.StatusInternalServerError)
		return
	}
	writeRPCResult(w, req.ID, map[string]any{
		"contents": []map[string]any{
			{
				"uri":      ContextResourceURI,
				"mimeType": "application/json",
				"text":     string(payload),
			},
		},
	})
}

// effectiveTools is the session's effective tool set on the MCP surface: the
// same rule (exclusions applied last, tool policy honoured, core tools always
// present) the gRPC surface applies via LlmServer.mergedToolDefinitions. Both
// surfaces must agree, or a tool hidden from one is reachable through the
// other.
func (s *Server) effectiveTools(ma *sessionmanager.SessionRuntime) []*llmv1.ToolDefinition {
	serverWide := s.cfg.LLM.ServerWideToolDefinitions()
	// Tenant-scoped executor tools, matching LlmServer.effectiveToolRequest.
	if s.cfg.LLM != nil && ma.Scope.TenantID != "" {
		serverWide = append(serverWide, s.cfg.LLM.ToolProviderHub.ExecutorDefinitions(ma.Scope.TenantID)...)
	}
	return toolmerge.EffectiveDefinitions(toolmerge.Request{
		ServerWide: serverWide,
		Session:    ma.Tools.Snapshot(),
		Disabled:   ma.DisabledToolSet(),
		Policy:     toolmerge.ParsePolicy(ma.ToolPolicyName()),
	})
}

// toolsDisabledForTurn reports whether the session's running turn was started
// with TurnOptions.disable_tools. The MCP session may be keyed by agent id or
// session id depending on the bridge, so either matching counts.
func (s *Server) toolsDisabledForTurn(sc serverutil.Scope, agentID, sessionID string) bool {
	if s.cfg.LLM == nil {
		return false
	}
	if s.cfg.LLM.ToolsDisabledForTurn(sc, agentID) {
		return true
	}
	return sessionID != "" && s.cfg.LLM.ToolsDisabledForTurn(sc, sessionID)
}

func (s *Server) buildContextJSON(ctx context.Context, sc serverutil.Scope, agentID, sessionID string) ([]byte, error) {
	ma, ok := s.cfg.SessionRuntimes.Get(sc, agentID)
	if !ok || ma.Scope != sc {
		return nil, errors.New("agent not available")
	}
	tools := toolmerge.RedactSecretsForList(s.effectiveTools(ma))
	toolsJSON := make([]json.RawMessage, 0, len(tools))
	for _, t := range tools {
		if t == nil {
			continue
		}
		b, err := protojson.Marshal(t)
		if err != nil {
			continue
		}
		toolsJSON = append(toolsJSON, json.RawMessage(b))
	}
	type msgDTO struct {
		Role            string `json:"role"`
		Content         string `json:"content,omitempty"`
		ThinkingContent string `json:"thinkingContent,omitempty"`
		Id              string `json:"id,omitempty"`
	}
	var recent []msgDTO
	sid := strings.TrimSpace(sessionID)
	if sid != "" && s.cfg.DB != nil {
		h := store.NewHistory(s.cfg.DB, sc, agentID)
		if h != nil {
			for _, m := range h.History(sid, 40) {
				if m == nil {
					continue
				}
				recent = append(recent, msgDTO{
					Role:            m.GetRole(),
					Content:         m.GetContent(),
					ThinkingContent: m.GetThinkingContent(),
					Id:              m.GetId(),
				})
			}
		}
	}
	body := map[string]any{
		"userId":       sc.TenantID, // wire key renamed in stage 4
		"agentId":      agentID,
		"sessionId":    sid,
		"displayName":  ma.DisplayName,
		"providerId":   ma.ProviderID,
		"defaultModel": ma.DefaultModel,
		"tools":        toolsJSON,
		"memories":     []string{},
		"recentChat":   recent,
	}
	return json.Marshal(body)
}

// resolveAgentScope returns the tenant Scope, agent id, and optional session id from headers.
// Headers:
//   - the scope headers (required; resolved by serverutil.ScopeFromHTTPHeader)
//   - X-Llmd-Agent-Id (optional if X-Llmd-Session-Id resolves via chat history)
//   - X-Llmd-Session-Id (optional; used to resolve agent when agent id omitted, and for chat excerpt)
func (s *Server) resolveAgentScope(ctx context.Context, r *http.Request) (sc serverutil.Scope, agentID, sessionID string, err error) {
	sc, err = serverutil.ScopeFromHTTPHeader(r.Header)
	if err != nil {
		return serverutil.Scope{}, "", "", err
	}
	sessionID = strings.TrimSpace(r.Header.Get("X-Llmd-Session-Id"))
	agentID = strings.TrimSpace(r.Header.Get("X-Llmd-Agent-Id"))
	if agentID == "" {
		if sessionID == "" {
			return sc, "", sessionID, errors.New("set X-Llmd-Agent-Id or X-Llmd-Session-Id (with existing persisted chat for that session)")
		}
		if s.cfg.DB == nil {
			return sc, "", sessionID, errors.New("database required to resolve session to agent")
		}
		sid, rerr := store.ConfirmChatSessionExists(s.cfg.DB, sc, sessionID)
		if rerr != nil {
			if errors.Is(rerr, gorm.ErrRecordNotFound) {
				return sc, "", sessionID, errors.New("no chat history for tenant+session_id; cannot infer agent")
			}
			return sc, "", sessionID, rerr
		}
		agentID = sid
	}
	if s.cfg.SessionRuntimes == nil || s.cfg.LLM == nil {
		return sc, "", sessionID, errors.New("server not configured")
	}
	if err := s.cfg.SessionRuntimes.EnsureLoaded(ctx, sc, agentID); err != nil {
		if errors.Is(err, sessionmanager.ErrUnknownSession) {
			return sc, agentID, sessionID, errors.New("unknown agent_id for tenant")
		}
		return sc, agentID, sessionID, err
	}
	return sc, agentID, sessionID, nil
}

func (s *Server) readOnlyForMCP(r *http.Request) bool {
	if s.cfg.AgentPIDRegistry == nil {
		return false
	}
	ppidStr := strings.TrimSpace(r.Header.Get("X-Llmd-Agent-Ppid"))
	if ppidStr == "" {
		return false
	}
	ppid, err := strconv.Atoi(ppidStr)
	if err != nil || ppid <= 0 {
		return false
	}
	if run, ok := s.cfg.AgentPIDRegistry.Lookup(ppid); ok {
		return run.ReadOnly
	}
	return false
}

func writeRPCResult(w http.ResponseWriter, id *json.RawMessage, result any) {
	b, err := json.Marshal(jsonRPCResponse{JSONRPC: "2.0", ID: *id, Result: result})
	if err != nil {
		http.Error(w, "marshal", http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(b)
}

func writeRPCError(w http.ResponseWriter, id *json.RawMessage, code int, message string, status int) {
	resp := jsonRPCResponse{JSONRPC: "2.0", Error: &jsonRPCError{Code: code, Message: message}}
	if id != nil {
		resp.ID = *id
	}
	b, _ := json.Marshal(resp)
	w.WriteHeader(status)
	_, _ = w.Write(b)
}

func writeScopeError(w http.ResponseWriter, id *json.RawMessage, err error) {
	msg := err.Error()
	code := -32002
	status := http.StatusBadRequest
	if strings.Contains(msg, "unknown agent") {
		status = http.StatusNotFound
	}
	writeRPCError(w, id, code, msg, status)
}
