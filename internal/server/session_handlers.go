package server

import (
	"context"
	"errors"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	claudecodeclillm "github.com/johnewart/pyxis/internal/llm/claudecode"
	llmtools "github.com/johnewart/pyxis/internal/llm/tools"
	"github.com/johnewart/pyxis/internal/sessionmanager"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/internal/toolmerge"
	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"gorm.io/gorm"
)

func (s *LlmServer) EnsureSession(ctx context.Context, req *llmv1.EnsureSessionRequest) (*llmv1.EnsureSessionResponse, error) {
	if s.SessionRuntimes == nil {
		return nil, status.Error(codes.FailedPrecondition, "session manager not configured")
	}
	sid, err := requireSessionID(req.GetSessionId())
	if err != nil {
		return nil, err
	}
	if err := s.validateAgentLlmBinding(ctx, req.GetLlmProviderId()); err != nil {
		return nil, err
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	patched := proto.Clone(req).(*llmv1.EnsureSessionRequest)
	patched.SessionId = sid
	if err := s.SessionRuntimes.EnsureSession(ctx, patched, sc); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	return &llmv1.EnsureSessionResponse{}, nil
}

func (s *LlmServer) UpdateSessionLlm(ctx context.Context, req *llmv1.UpdateSessionLlmRequest) (*llmv1.UpdateSessionLlmResponse, error) {
	if s.SessionRuntimes == nil {
		return nil, status.Error(codes.FailedPrecondition, "session manager not configured")
	}
	sid, err := requireSessionID(req.GetSessionId())
	if err != nil {
		return nil, err
	}
	if err := s.validateAgentLlmBinding(ctx, req.GetLlmProviderId()); err != nil {
		return nil, err
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.SessionRuntimes.UpdateLlm(ctx, sc, sid, req.LlmProviderId, req.DefaultModel); err != nil {
		if errors.Is(err, sessionmanager.ErrUnknownSession) {
			return nil, status.Errorf(codes.NotFound, "unknown session_id %q", sid)
		}
		if errors.Is(err, sessionmanager.ErrWrongScope) {
			return nil, status.Errorf(codes.NotFound, "unknown session_id %q", sid)
		}
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	return &llmv1.UpdateSessionLlmResponse{}, nil
}

func (s *LlmServer) DestroySession(ctx context.Context, req *llmv1.DestroySessionRequest) (*llmv1.DestroySessionResponse, error) {
	if s.SessionRuntimes == nil {
		return nil, status.Error(codes.FailedPrecondition, "session manager not configured")
	}
	sid, err := requireSessionID(req.GetSessionId())
	if err != nil {
		return nil, err
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	if !s.SessionRuntimes.Destroy(ctx, sc, sid) {
		return nil, status.Errorf(codes.NotFound, "unknown session_id %q", sid)
	}
	return &llmv1.DestroySessionResponse{}, nil
}

func (s *LlmServer) RegisterSessionTool(ctx context.Context, req *llmv1.RegisterSessionToolRequest) (*llmv1.RegisterSessionToolResponse, error) {
	sid, err := requireSessionID(req.GetSessionId())
	if err != nil {
		return nil, err
	}
	td := req.GetTool()
	if td == nil {
		return nil, status.Error(codes.InvalidArgument, "tool is required")
	}
	if strings.TrimSpace(td.GetName()) == "" {
		return nil, status.Error(codes.InvalidArgument, "tool name is required")
	}
	if err := llmtools.ValidateParametersJSON(td.GetParametersSchemaJson()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	if wh := td.GetHttpWebhook(); wh != nil {
		if strings.TrimSpace(wh.GetUrl()) == "" {
			return nil, status.Error(codes.InvalidArgument, "http_webhook.url is required")
		}
	}
	if s.SessionRuntimes == nil {
		return nil, status.Error(codes.FailedPrecondition, "session manager not configured")
	}
	if err := s.ensureKnownSession(ctx, sid); err != nil {
		return nil, err
	}
	ma, ok := s.runtimeFor(ctx, sid)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "unknown session_id %q", sid)
	}
	if err := ma.Tools.Register(td); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	ma.Touch()
	_ = s.SessionRuntimes.PersistToolSnapshot(ctx, s.scopeOrZero(ctx), sid)
	return &llmv1.RegisterSessionToolResponse{}, nil
}

func (s *LlmServer) UnregisterSessionTool(ctx context.Context, req *llmv1.UnregisterSessionToolRequest) (*llmv1.UnregisterSessionToolResponse, error) {
	sid, err := requireSessionID(req.GetSessionId())
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(req.GetName()) == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	if s.SessionRuntimes == nil {
		return nil, status.Error(codes.FailedPrecondition, "session manager not configured")
	}
	if err := s.ensureKnownSession(ctx, sid); err != nil {
		return nil, err
	}
	ma, ok := s.runtimeFor(ctx, sid)
	if !ok {
		return nil, status.Errorf(codes.NotFound, "unknown session_id %q", sid)
	}
	name := strings.TrimSpace(req.GetName())
	// Only client-owned tools (execution variant delegate_to_client) may be removed
	// over this RPC. Server-installed tools (agent profile, inline_builtin,
	// http_webhook) are off limits: clients reconcile their own tool set before every
	// turn and would otherwise strip the session back to bare.
	if existing, ok := ma.Tools.Get(name); ok && existing.GetDelegateToClient() == nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn,
			"UnregisterSessionTool: REFUSED removal of server-installed tool: session=%q tool=%q execution=%s (clients may only unregister delegate_to_client tools)",
			sid, name, toolExecutionVariantName(existing))
		// Deliberately a success: registerTools unregisters in a loop before every turn,
		// so returning an error here would break every turn instead of the bug.
		return &llmv1.UnregisterSessionToolResponse{}, nil
	}
	_ = ma.Tools.Unregister(name)
	ma.Touch()
	_ = s.SessionRuntimes.PersistToolSnapshot(ctx, s.scopeOrZero(ctx), sid)
	return &llmv1.UnregisterSessionToolResponse{}, nil
}

// toolExecutionVariantName renders a tool's execution oneof for log messages.
func toolExecutionVariantName(td *llmv1.ToolDefinition) string {
	switch {
	case td == nil:
		return "none"
	case td.GetDelegateToClient() != nil:
		return "delegate_to_client"
	case td.GetInlineBuiltin() != nil:
		return "inline_builtin"
	case td.GetHttpWebhook() != nil:
		return "http_webhook"
	default:
		return "unset"
	}
}

func (s *LlmServer) ListSessionTools(ctx context.Context, req *llmv1.ListSessionToolsRequest) (*llmv1.ListSessionToolsResponse, error) {
	sid, err := requireSessionID(req.GetSessionId())
	if err != nil {
		return nil, err
	}
	if s.SessionRuntimes == nil {
		return nil, status.Error(codes.FailedPrecondition, "session manager not configured")
	}
	if err := s.ensureKnownSession(ctx, sid); err != nil {
		return nil, err
	}
	if _, ok := s.runtimeFor(ctx, sid); !ok {
		return nil, status.Errorf(codes.NotFound, "unknown session_id %q", sid)
	}
	// Same merge the model sees (mergedToolsStream), including the session's
	// disabled-tool exclusions; the Get above guarantees the runtime is loaded.
	merged := s.mergedToolDefinitions(ctx, sid)
	return &llmv1.ListSessionToolsResponse{Tools: toolmerge.RedactSecretsForList(merged)}, nil
}

func (s *LlmServer) GetSession(ctx context.Context, req *llmv1.GetSessionRequest) (*llmv1.GetSessionResponse, error) {
	sid, err := requireSessionID(req.GetSessionId())
	if err != nil {
		return nil, err
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	// GetSession returns empty SessionMeta for unknown sessions (not error)
	if s.SessionRepo == nil {
		return &llmv1.GetSessionResponse{
			Meta: &llmv1.SessionMeta{
				Title:          "",
				TodosJson:      "",
				AttributesJson: "",
				CursorPos:      0,
			},
		}, nil
	}
	row, err := s.SessionRepo.GetSession(ctx, sc, sid)
	if err != nil {
		// Return empty SessionMeta for not found, not an error
		return &llmv1.GetSessionResponse{
			Meta: &llmv1.SessionMeta{
				Title:          "",
				TodosJson:      "",
				AttributesJson: "",
				CursorPos:      0,
			},
		}, nil
	}
	// The turn status lives in the session cache (see store/turn_status.go).
	// A failed read leaves it empty: the next set_status reaches the client live.
	turnStatus, serr := s.SessionCache.TurnStatus(ctx, sc, sid)
	if serr != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn, "GetSession: read turn status failed: session=%q err=%v", sid, serr)
	}
	return &llmv1.GetSessionResponse{
		Meta: &llmv1.SessionMeta{
			Title:          row.Title,
			TodosJson:      row.TodosJSON,
			Status:         turnStatus,
			AttributesJson: row.AttributesJSON,
			CursorPos:      row.CursorPos,
			SessionId:      row.SessionID,
			ChatSurface:    row.ChatSurface,
			CreatedAt:      row.CreatedAt.UnixMilli(),
			UpdatedAt:      row.UpdatedAt.UnixMilli(),
			// Read-only surfacing of the session's persisted model selection so
			// a client can rehydrate provider/model after a restart. Written by
			// UpdateSessionLlm, never by SetSessionMeta.
			ProviderId:   row.ProviderID,
			DefaultModel: row.DefaultModel,
			Tags:         store.DecodeSessionTags(row.TagsJSON),
			// Read-only fork lineage (ForkSession); SetSessionMeta ignores it.
			ForkedFromSessionId: row.ForkedFromSessionID,
			ForkedFromMessageId: row.ForkedFromMessageID,
		},
	}, nil
}

// UpdateSessionTags merges (or replaces) the session's k/v tags. Deliberately a
// separate RPC from SetSessionMeta: a meta write is wholesale, so folding tags
// into it would let a caller updating only a title silently drop tags another
// caller had set. SetSessionMeta therefore ignores SessionMeta.tags entirely.
func (s *LlmServer) UpdateSessionTags(ctx context.Context, req *llmv1.UpdateSessionTagsRequest) (*llmv1.UpdateSessionTagsResponse, error) {
	sid, err := requireSessionID(req.GetSessionId())
	if err != nil {
		return nil, err
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	if s.SessionRepo == nil {
		return nil, status.Error(codes.FailedPrecondition, "session repo not configured")
	}
	// Surface a malformed tag key as InvalidArgument rather than letting it fall
	// out of the repo as Internal.
	if _, err := store.NormalizeSessionTags(req.GetTags()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	// The row may not exist yet (session created but never provisioned), same as
	// SetSessionMeta: create it so llmd is the authority from creation.
	if err := s.SessionRepo.CreateSessionRowIfMissing(ctx, sc, sid); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create session row: %v", err)
	}
	if req.GetReplace() {
		if err := s.SessionRepo.ReplaceSessionTags(ctx, sc, sid, req.GetTags()); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to replace session tags: %v", err)
		}
		tags, err := s.SessionRepo.GetSessionTags(ctx, sc, sid)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to read session tags: %v", err)
		}
		return &llmv1.UpdateSessionTagsResponse{Tags: tags}, nil
	}
	merged, err := s.SessionRepo.UpdateSessionTags(ctx, sc, sid, req.GetTags())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update session tags: %v", err)
	}
	return &llmv1.UpdateSessionTagsResponse{Tags: merged}, nil
}

func (s *LlmServer) SetSessionMeta(ctx context.Context, req *llmv1.SetSessionMetaRequest) (*llmv1.SetSessionMetaResponse, error) {
	sid, err := requireSessionID(req.GetSessionId())
	if err != nil {
		return nil, err
	}
	if req.GetMeta() == nil {
		return nil, status.Error(codes.InvalidArgument, "meta is required")
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	if s.SessionRepo == nil {
		return nil, status.Error(codes.FailedPrecondition, "session repo not configured")
	}
	// NOTE: SessionMeta.tags is intentionally NOT written here — see
	// UpdateSessionTags. This map is wholesale, and proto3 cannot distinguish an
	// omitted map from an empty one, so honouring tags here would make every
	// title-only update a potential tag wipe.
	updates := map[string]interface{}{
		"title":           req.GetMeta().GetTitle(),
		"todos_json":      req.GetMeta().GetTodosJson(),
		"attributes_json": req.GetMeta().GetAttributesJson(),
	}
	// chat_surface is optional on the wire: only overwrite when provided so a
	// meta update that omits it cannot wipe the session's surface.
	if surface := req.GetMeta().GetChatSurface(); surface != "" {
		updates["chat_surface"] = surface
	}
	err = s.SessionRepo.UpdateSession(ctx, sc, sid, updates)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		// No row yet (session created but not provisioned). Create then retry so
		// llmd is the authority from creation, not just from the first turn.
		if cerr := s.SessionRepo.CreateSessionRowIfMissing(ctx, sc, sid); cerr != nil {
			return nil, status.Errorf(codes.Internal, "failed to create session row: %v", cerr)
		}
		err = s.SessionRepo.UpdateSession(ctx, sc, sid, updates)
	}
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to update session: %v", err)
	}
	return &llmv1.SetSessionMetaResponse{Success: true}, nil
}

// sessionSortFieldFromProto maps the wire sort key to the store's. Unspecified
// (and anything unknown from a newer client) falls back to last-message-first.
func sessionSortFieldFromProto(f llmv1.SessionSortField) store.SessionSortField {
	switch f {
	case llmv1.SessionSortField_SESSION_SORT_FIELD_CREATED_AT:
		return store.SessionSortCreatedAt
	case llmv1.SessionSortField_SESSION_SORT_FIELD_UPDATED_AT:
		return store.SessionSortUpdatedAt
	case llmv1.SessionSortField_SESSION_SORT_FIELD_TITLE:
		return store.SessionSortTitle
	default:
		return store.SessionSortLastMessageAt
	}
}

// sessionSortDescending resolves an unspecified order per sort key: newest-first
// for the timestamps (what a session list wants), A→Z for titles.
func sessionSortDescending(f llmv1.SessionSortField, o llmv1.SessionSortOrder) bool {
	switch o {
	case llmv1.SessionSortOrder_SESSION_SORT_ORDER_ASC:
		return false
	case llmv1.SessionSortOrder_SESSION_SORT_ORDER_DESC:
		return true
	default:
		return f != llmv1.SessionSortField_SESSION_SORT_FIELD_TITLE
	}
}

func (s *LlmServer) ListSessions(ctx context.Context, req *llmv1.ListSessionsRequest) (*llmv1.ListSessionsResponse, error) {
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	if s.SessionRepo == nil {
		return &llmv1.ListSessionsResponse{Sessions: []*llmv1.SessionSummary{}}, nil
	}
	offset := int(req.GetOffset())
	if offset < 0 {
		offset = 0
	}
	if _, err := store.NormalizeSessionTags(req.GetTagFilters()); err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	rows, total, err := s.SessionRepo.ListSessions(ctx, sc, store.ListSessionsOptions{
		SortBy:      sessionSortFieldFromProto(req.GetSortBy()),
		Descending:  sessionSortDescending(req.GetSortBy(), req.GetOrder()),
		Limit:       int(req.GetPageSize()),
		Offset:      offset,
		ChatSurface: req.GetChatSurface(),
		Tags:        req.GetTagFilters(),
	})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to list sessions: %v", err)
	}
	sessions := make([]*llmv1.SessionSummary, 0, len(rows))
	for _, row := range rows {
		summary := &llmv1.SessionSummary{
			SessionId:   row.SessionID,
			Title:       row.Title,
			ChatSurface: row.ChatSurface,
			CreatedAt:   row.CreatedAt.UnixMilli(),
			UpdatedAt:   row.UpdatedAt.UnixMilli(),
			CursorPos:   row.CursorPos,
			Tags:        store.DecodeSessionTags(row.TagsJSON),

			ForkedFromSessionId: row.ForkedFromSessionID,
			ForkedFromMessageId: row.ForkedFromMessageID,
		}
		// NULL for a session with no messages yet; stays 0 on the wire.
		if row.LastMessageAt.Valid {
			summary.LastMessageAt = row.LastMessageAt.Time.UnixMilli()
		}
		sessions = append(sessions, summary)
	}
	resp := &llmv1.ListSessionsResponse{Sessions: sessions, TotalCount: total}
	if next := offset + len(sessions); int64(next) < total {
		resp.HasMore = true
		resp.NextOffset = int32(next)
	}
	return resp, nil
}

func (s *LlmServer) SetSessionCursor(ctx context.Context, req *llmv1.SetSessionCursorRequest) (*llmv1.SetSessionCursorResponse, error) {
	sid, err := requireSessionID(req.GetSessionId())
	if err != nil {
		return nil, err
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	if s.SessionRepo == nil {
		return nil, status.Error(codes.FailedPrecondition, "session repo not configured")
	}
	updates := map[string]interface{}{
		"cursor_pos": req.GetCursorPos(),
	}
	err = s.SessionRepo.UpdateSession(ctx, sc, sid, updates)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to set cursor: %v", err)
	}
	return &llmv1.SetSessionCursorResponse{Success: true}, nil
}

func (s *LlmServer) AdvanceSessionCursor(ctx context.Context, req *llmv1.AdvanceSessionCursorRequest) (*llmv1.AdvanceSessionCursorResponse, error) {
	sid, err := requireSessionID(req.GetSessionId())
	if err != nil {
		return nil, err
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	if s.SessionRepo == nil {
		return nil, status.Error(codes.FailedPrecondition, "session repo not configured")
	}
	// Get current cursor position
	row, err := s.SessionRepo.GetSession(ctx, sc, sid)
	if err != nil {
		// If session doesn't exist, set cursor to the delta
		newCursorPos := req.GetDelta()
		updates := map[string]interface{}{
			"cursor_pos": newCursorPos,
		}
		err = s.SessionRepo.UpdateSession(ctx, sc, sid, updates)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to advance cursor: %v", err)
		}
		return &llmv1.AdvanceSessionCursorResponse{NewCursorPos: newCursorPos}, nil
	}
	// Advance existing cursor
	newCursorPos := row.CursorPos + req.GetDelta()
	updates := map[string]interface{}{
		"cursor_pos": newCursorPos,
	}
	err = s.SessionRepo.UpdateSession(ctx, sc, sid, updates)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to advance cursor: %v", err)
	}
	return &llmv1.AdvanceSessionCursorResponse{NewCursorPos: newCursorPos}, nil
}

func (s *LlmServer) DeleteSession(ctx context.Context, req *llmv1.DeleteSessionRequest) (*llmv1.DeleteSessionResponse, error) {
	sid, err := requireSessionID(req.GetSessionId())
	if err != nil {
		return nil, err
	}
	sc, err := s.scope(ctx)
	if err != nil {
		return nil, err
	}
	if s.SessionRepo == nil {
		return nil, status.Error(codes.FailedPrecondition, "session repo not configured")
	}
	// PurgeSession removes chat history, provider sync, memories, and the session
	// row itself — so a deleted session cannot reappear in ListSessions.
	if err := s.SessionRepo.PurgeSession(ctx, sc, sid); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return &llmv1.DeleteSessionResponse{Success: true}, nil
		}
		return nil, status.Errorf(codes.Internal, "failed to delete session: %v", err)
	}
	// Best effort: the CLI transcripts only back --resume for this session. A
	// failure is logged inside PurgeSessionTranscripts and must not fail the delete.
	_ = claudecodeclillm.PurgeSessionTranscripts(sid)
	return &llmv1.DeleteSessionResponse{Success: true}, nil
}
