package toolexec

// add_todo / update_todo / list_todos / set_status / set_session_title, in
// process, as first-class llmd builtin tools rather than something a host
// (the client) bolts on separately.
//
// Persistence:
//   - Todos live in llmd_sessions.todos_json (store.SessionRow.TodosJSON),
//     the same column SessionMeta.todos_json already exposes read-only via
//     GetSession — these tools are simply the first writer of it.
//   - Title lives in llmd_sessions.title (store.SessionRow.Title), same
//     column SetSessionMeta lets a client/UI set; these tools are an
//     agent-authored writer of the same column.
//   - Status lives in the session cache (SessionCacheRepo.SetTurnStatus), not
//     on the session row: it is turn-scoped. ChatStream clears it when a user
//     turn starts and ends, and a client that (re)connects seeds the current
//     turn's headline from GetSession, like title and todos.
//
// Delivery to the frontend: every handler here, on success, calls emitFrame
// with the corresponding ConversationStreamFrame (EvTodosUpdated /
// EvSessionStatus / EvSessionTitleChanged / EvAttachmentUpserted). emitFrame delivers it two ways
// depending on transport (see its doc comment): live, through the ChatStream
// tool loop's own response stream when dispatched there (via
// WithFrameEmitter), or persisted directly to SessionEventRepo for replay
// when dispatched via MCP tools/call (CLI-driven agentic providers have no
// live stream to push onto).
//
// Turn tracking: CreatedTurnSeq/StartedTurnSeq/CompletedTurnSeq on each
// TodoItem are read from SessionToolsDeps.Turns (store.TurnRegister), the
// same turn_seq counter store.TurnDecorationRepo and toolsession's envelope
// use — so a todo's lifecycle lines up with the same turn numbering
// everything else in a session is keyed by.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
	"github.com/johnewart/pyxis/coretools"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/llmdlog"
	"github.com/johnewart/pyxis/toolregistry"
	"google.golang.org/protobuf/encoding/protojson"
	"gorm.io/gorm"
)

// Session tool names. Aliases of internal/coretools, which is the single
// source of the core set (toolmerge needs the same names to keep an agent
// from shadowing them, and cannot import this package).
const (
	ToolAddTodo         = coretools.AddTodo
	ToolUpdateTodo      = coretools.UpdateTodo
	ToolListTodos       = coretools.ListTodos
	ToolSetStatus       = coretools.SetStatus
	ToolSetSessionTitle = coretools.SetSessionTitle
	// ToolAskUser asks the human a structured question via llmd's first-class
	// AskCard vocabulary (see askUserHandler): an inline builtin, like every
	// other session tool here — it persists an ask_card attachment and
	// returns immediately (no blocking wait). The existing, already-shipped
	// AskCard UI (packages/react's AskCardView, rendered via ChatPanel's
	// attachmentIndex/onAskCardSubmit) picks it up and the user answers
	// whenever, via the existing UpsertSessionAttachment RPC — exactly the
	// same round trip a manually-created ask card already uses.
	ToolAskUser = coretools.AskUser
)

// SessionToolsDeps are the persistence seams the session tool handlers close
// over at registration. Sessions is required for every tool here (todos and
// title both live on the session row); Events and Turns may be nil, in which
// case event emission / turn-seq stamping degrade to no-ops rather than
// failing the tool call — a session tool should never fail just because
// replay delivery or turn numbering isn't wired in a given deployment (e.g.
// unit tests, or a minimal embedding of llmd). Attachments is required only
// for ask_user (see askUserHandler); nil disables that one tool's handler
// (it errors cleanly) without affecting the other five. Cache holds the
// turn status set_status writes; nil means it is only emitted, not stored
// (a reconnecting client then can't seed it), the same degradation as Events.
type SessionToolsDeps struct {
	Sessions    *store.SessionRepo
	Events      *store.SessionEventRepo
	Turns       *store.TurnRegister
	Attachments *store.ChatAttachmentRepo
	Cache       *store.SessionCacheRepo
}

// RegisterSessionTools registers add_todo/update_todo/list_todos/set_status/
// set_session_title into reg and handlers. deps.Sessions is required; a nil
// value is a configuration error (returned), not a silently-broken tool.
func RegisterSessionTools(reg *toolregistry.Registry, handlers *Handlers, deps SessionToolsDeps) error {
	if reg == nil {
		return fmt.Errorf("toolregistry is required")
	}
	if handlers == nil {
		return fmt.Errorf("handler set is required")
	}
	if deps.Sessions == nil {
		return fmt.Errorf("SessionToolsDeps.Sessions is required")
	}

	defs := []struct {
		name    string
		desc    string
		schema  string
		class   llmv1.ToolClassification
		handler Handler
	}{
		{
			ToolAddTodo,
			"Add an item to this session's todo list, so your own multi-step plan is visible to the user as you work (rendered live in the UI). Use for a task you intend to come back to, not for a single immediate action.",
			`{"type":"object","properties":{"description":{"type":"string"}},"required":["description"]}`,
			llmv1.ToolClassification_TOOL_CLASSIFICATION_MUTATION,
			addTodoHandler(deps),
		},
		{
			ToolUpdateTodo,
			"Update the status of one of your own todo items (from add_todo/list_todos) to in_progress or completed. Mark a todo in_progress when you start it and completed when you finish it, so the user can follow your progress.",
			`{"type":"object","properties":{"id":{"type":"string"},"status":{"type":"string","enum":["pending","in_progress","completed"]}},"required":["id","status"]}`,
			llmv1.ToolClassification_TOOL_CLASSIFICATION_MUTATION,
			updateTodoHandler(deps),
		},
		{
			ToolListTodos,
			"List this session's current todo items and their statuses (pending/in_progress/completed).",
			`{"type":"object","properties":{}}`,
			llmv1.ToolClassification_TOOL_CLASSIFICATION_QUERY,
			listTodosHandler(deps),
		},
		{
			ToolSetStatus,
			"Set a short, human-readable status headline shown live in the UI (e.g. \"Looking for a fish...\") describing what you're currently doing. Call this before a step that will take a while, so the user isn't left wondering if you're stuck. It has no effect on the conversation itself.",
			`{"type":"object","properties":{"description":{"type":"string"}},"required":["description"]}`,
			llmv1.ToolClassification_TOOL_CLASSIFICATION_MUTATION,
			setStatusHandler(deps),
		},
		{
			ToolSetSessionTitle,
			"Rename this chat session to a short, descriptive title. Use once you understand what the conversation is actually about; call again later if the topic changes substantially.",
			`{"type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`,
			llmv1.ToolClassification_TOOL_CLASSIFICATION_MUTATION,
			setSessionTitleHandler(deps),
		},
		{
			ToolAskUser,
			"Ask the user a structured question (free text, a single/multi-select choice, or a file upload) and render it as an interactive card in the chat UI. Calling this tool ENDS YOUR CURRENT TURN IMMEDIATELY, automatically, right after the card is shown — you do not get to call any more tools or produce any more text this turn, and you do NOT get the user's answer back as part of this call. This is enforced, not a suggestion: only call ask_user once you're actually ready to stop and wait. The user's answer will arrive as a new message in a future turn, same as any other reply. " +
				"Keep `text` to one or two sentences framing the question. For a choice, prefer `options` over `choices`: give each option a one-line `description` of what will happen if it is picked, so the user does not have to map a paragraph back onto the options. If you favour one option, set `recommendation` on that option only, with a one-line reason; it is shown right under that option.",
			`{"type":"object","properties":{` +
				`"title":{"type":"string"},` +
				`"text":{"type":"string","description":"One or two sentences framing the question. Put per-option detail in options[].description."},` +
				`"type":{"type":"string","enum":["text","choice","file"]},` +
				`"choices":{"type":"array","items":{"type":"string"},"description":"Plain option labels. Ignored when options is given."},` +
				`"options":{"type":"array","description":"Choice options with explanations (type=choice). Takes precedence over choices.","items":{"type":"object","properties":{` +
				`"label":{"type":"string","description":"Short option label; this is what the answer reports as selected."},` +
				`"description":{"type":"string","description":"One line: what happens if the user picks this."},` +
				`"recommendation":{"type":"string","description":"Set on at most ONE option: a one-line reason you recommend it. Marks the option as recommended."}` +
				`},"required":["label"]}},` +
				`"allow_multiple":{"type":"boolean"},` +
				`"allow_additional_text":{"type":"boolean"},` +
				`"accepted_mime_types":{"type":"array","items":{"type":"string"}}` +
				`},"required":["title","type"]}`,
			llmv1.ToolClassification_TOOL_CLASSIFICATION_QUERY,
			askUserHandler(deps),
		},
	}

	registered := make(map[string]struct{}, len(defs))
	for _, d := range defs {
		def := &llmv1.ToolDefinition{
			Name:                 d.name,
			Description:          d.desc,
			ParametersSchemaJson: d.schema,
			Classification:       d.class,
			Execution: &llmv1.ToolDefinition_InlineBuiltin{
				InlineBuiltin: &llmv1.ToolExecutionInlineBuiltin{},
			},
		}
		if err := reg.Register(def); err != nil {
			return fmt.Errorf("register %s: %w", d.name, err)
		}
		if err := handlers.Register(d.name, d.handler); err != nil {
			return fmt.Errorf("register %s handler: %w", d.name, err)
		}
		registered[d.name] = struct{}{}
	}
	// Fail loudly if this table and the canonical set have drifted. A core tool
	// named in coretools but missing here would be protected from shadowing and
	// promised to every agent while never actually being registered — an agent
	// would be told it exists and get "unknown tool" on use. Startup is the
	// right place to find that, not a user's session.
	for _, name := range coretools.Names() {
		if _, ok := registered[name]; !ok {
			return fmt.Errorf(
				"core tool %q is in coretools.Names() but was not registered here; add it to RegisterSessionTools's defs table", name)
		}
	}
	return nil
}

// frameEmitterKey is the context key WithFrameEmitter/frameEmitterFrom share.
// Unexported: the only way to set or read it is through those two funcs.
type frameEmitterKey struct{}

// WithFrameEmitter attaches a live ConversationStreamFrame sink to ctx for the
// duration of one tool dispatch. The gRPC ChatStream tool loop is the only
// caller with anywhere to push a frame live (its own response stream, plus
// the session hub and SessionEventRepo — see internal/server/server.go's
// `send` closure, which already does all three for every other frame type);
// it wraps ctx with this right before dispatching an inline builtin. The MCP
// tools/call transport (CLI-driven agentic providers) has no live stream to
// push onto and never sets this — emitFrame then falls back to persisting
// directly via SessionEventRepo, so a reconnecting/polling client still sees
// the event via replay, just not live.
func WithFrameEmitter(ctx context.Context, emit func(*llmv1.ConversationStreamFrame) error) context.Context {
	if emit == nil {
		return ctx
	}
	return context.WithValue(ctx, frameEmitterKey{}, emit)
}

func frameEmitterFrom(ctx context.Context) func(*llmv1.ConversationStreamFrame) error {
	fn, _ := ctx.Value(frameEmitterKey{}).(func(*llmv1.ConversationStreamFrame) error)
	return fn
}

// FrameEmitterFrom is frameEmitterFrom for the server's card sink, which
// delivers tool-returned cards the same way session tools deliver frames.
func FrameEmitterFrom(ctx context.Context) func(*llmv1.ConversationStreamFrame) error {
	return frameEmitterFrom(ctx)
}

// emitFrame is the single chokepoint every session-tool handler uses to
// deliver its ConversationStreamFrame: live via WithFrameEmitter's sink when
// present (ChatStream — the sink itself also persists to SessionEventRepo,
// so this does NOT double-append in that case), otherwise persisted directly
// via SessionEventRepo (MCP, or any other transport with no live stream).
// Best-effort either way: a failure here is never surfaced as a tool error
// (the mutation itself already succeeded), mirroring
// IngestDelegatedResult/AddDecoration's best-effort posture elsewhere in this
// codebase.
func emitFrame(ctx context.Context, events *store.SessionEventRepo, sc serverutil.Scope, sessionID string, frame *llmv1.ConversationStreamFrame) {
	if frame == nil {
		return
	}
	if live := frameEmitterFrom(ctx); live != nil {
		if err := live(frame); err != nil {
			llmdlog.Stdout("toolexec", llmdlog.LevelWarn,
				"live frame emit failed %s session=%q: %v", sc.String(), sessionID, err)
		}
		return
	}
	if events == nil || strings.TrimSpace(sc.TenantID) == "" || strings.TrimSpace(sessionID) == "" {
		return
	}
	b, err := protojson.Marshal(frame)
	if err != nil {
		llmdlog.Stdout("toolexec", llmdlog.LevelWarn,
			"marshal session frame failed %s session=%q: %v", sc.String(), sessionID, err)
		return
	}
	if _, err := events.AppendEvent(ctx, sc, sessionID, string(b)); err != nil {
		llmdlog.Stdout("toolexec", llmdlog.LevelWarn,
			"append session event failed %s session=%q: %v", sc.String(), sessionID, err)
	}
}

func requireSessionScope(inv Invocation) (serverutil.Scope, string, error) {
	sc := serverutil.Scope{TenantID: strings.TrimSpace(inv.Scope.TenantID), ScopeID: strings.TrimSpace(inv.Scope.ScopeID)}
	sessionID := strings.TrimSpace(inv.SessionID)
	if sc.TenantID == "" || sessionID == "" {
		return serverutil.Scope{}, "", fmt.Errorf("no session bound to this invocation (tenant/session_id required)")
	}
	return sc, sessionID, nil
}

func loadTodos(ctx context.Context, deps SessionToolsDeps, sc serverutil.Scope, sessionID string) ([]store.TodoItem, *store.SessionRow, error) {
	row, err := deps.Sessions.GetSession(ctx, sc, sessionID)
	if err != nil {
		// No row yet: an agent calling add_todo before any turn has
		// persisted the session shouldn't fail — start from an empty list
		// and let the save path create the row.
		return nil, nil, nil
	}
	return store.ParseTodos(row.TodosJSON), row, nil
}

func saveTodos(ctx context.Context, deps SessionToolsDeps, sc serverutil.Scope, sessionID string, items []store.TodoItem) error {
	wire := store.MarshalTodos(items)
	err := deps.Sessions.UpdateSession(ctx, sc, sessionID, map[string]interface{}{"todos_json": wire})
	if errors.Is(err, gorm.ErrRecordNotFound) {
		if cerr := deps.Sessions.CreateSessionRowIfMissing(ctx, sc, sessionID); cerr != nil {
			return cerr
		}
		return deps.Sessions.UpdateSession(ctx, sc, sessionID, map[string]interface{}{"todos_json": wire})
	}
	return err
}

func todosUpdatedFrame(sessionID string, items []store.TodoItem) *llmv1.ConversationStreamFrame {
	return &llmv1.ConversationStreamFrame{
		SchemaVersion: 2,
		Frame: &llmv1.ConversationStreamFrame_TodosUpdated{
			TodosUpdated: &llmv1.EvTodosUpdated{SessionId: sessionID, TodosJson: store.MarshalTodos(items)},
		},
	}
}

// addTodoHandler appends a new pending todo, stamped with the session's
// current turn_seq (0 when no TurnRegister is wired). Returns the FULL
// current todo list (not just the added item) — the same shape list_todos
// and update_todo return, so a caller (or a UI reading the tool result
// directly) never has to distinguish "one item" from "the whole list" across
// these three tools.
func addTodoHandler(deps SessionToolsDeps) Handler {
	return func(ctx context.Context, args json.RawMessage, inv Invocation) (Result, error) {
		sc, sid, err := requireSessionScope(inv)
		if err != nil {
			return Result{}, err
		}
		var parsed struct {
			Description string `json:"description"`
		}
		if len(args) > 0 {
			if err := json.Unmarshal(args, &parsed); err != nil {
				return Result{}, fmt.Errorf("bad arguments: %w", err)
			}
		}
		desc := strings.TrimSpace(parsed.Description)
		if desc == "" {
			return Result{}, fmt.Errorf("description is required")
		}

		items, _, err := loadTodos(ctx, deps, sc, sid)
		if err != nil {
			return Result{}, err
		}
		item := store.TodoItem{
			ID:             uuid.New().String(),
			Description:    desc,
			Status:         store.TodoPending,
			CreatedTurnSeq: deps.Turns.Current(sc, sid),
		}
		items = append(items, item)
		if err := saveTodos(ctx, deps, sc, sid, items); err != nil {
			return Result{}, fmt.Errorf("save todos: %w", err)
		}
		emitFrame(ctx, deps.Events, sc, sid, todosUpdatedFrame(sid, items))
		return Result{Content: store.MarshalTodos(items)}, nil
	}
}

// updateTodoHandler transitions one todo's status, stamping the turn_seq the
// transition happened on (started for in_progress, completed for completed).
func updateTodoHandler(deps SessionToolsDeps) Handler {
	return func(ctx context.Context, args json.RawMessage, inv Invocation) (Result, error) {
		sc, sid, err := requireSessionScope(inv)
		if err != nil {
			return Result{}, err
		}
		var parsed struct {
			ID     string `json:"id"`
			Status string `json:"status"`
		}
		if len(args) > 0 {
			if err := json.Unmarshal(args, &parsed); err != nil {
				return Result{}, fmt.Errorf("bad arguments: %w", err)
			}
		}
		id := strings.TrimSpace(parsed.ID)
		status := store.TodoStatus(strings.TrimSpace(parsed.Status))
		if id == "" {
			return Result{}, fmt.Errorf("id is required")
		}
		switch status {
		case store.TodoPending, store.TodoInProgress, store.TodoCompleted:
		default:
			return Result{}, fmt.Errorf("status must be one of pending, in_progress, completed (got %q)", parsed.Status)
		}

		items, _, err := loadTodos(ctx, deps, sc, sid)
		if err != nil {
			return Result{}, err
		}
		found := false
		seq := deps.Turns.Current(sc, sid)
		for i := range items {
			if items[i].ID != id {
				continue
			}
			found = true
			items[i].Status = status
			switch status {
			case store.TodoInProgress:
				if items[i].StartedTurnSeq == 0 {
					items[i].StartedTurnSeq = seq
				}
			case store.TodoCompleted:
				if items[i].CompletedTurnSeq == 0 {
					items[i].CompletedTurnSeq = seq
				}
			}
			break
		}
		if !found {
			return Result{}, fmt.Errorf("no todo with id %q", id)
		}
		if err := saveTodos(ctx, deps, sc, sid, items); err != nil {
			return Result{}, fmt.Errorf("save todos: %w", err)
		}
		emitFrame(ctx, deps.Events, sc, sid, todosUpdatedFrame(sid, items))
		return Result{Content: store.MarshalTodos(items)}, nil
	}
}

// listTodosHandler returns the session's current todo list verbatim.
func listTodosHandler(deps SessionToolsDeps) Handler {
	return func(ctx context.Context, _ json.RawMessage, inv Invocation) (Result, error) {
		sc, sid, err := requireSessionScope(inv)
		if err != nil {
			return Result{}, err
		}
		items, _, err := loadTodos(ctx, deps, sc, sid)
		if err != nil {
			return Result{}, err
		}
		return Result{Content: store.MarshalTodos(items)}, nil
	}
}

// setStatusHandler stores the current turn's agent-authored status headline
// ("Looking for a fish...") and emits the matching frame.
func setStatusHandler(deps SessionToolsDeps) Handler {
	return func(ctx context.Context, args json.RawMessage, inv Invocation) (Result, error) {
		sc, sid, err := requireSessionScope(inv)
		if err != nil {
			return Result{}, err
		}
		var parsed struct {
			Description string `json:"description"`
		}
		if len(args) > 0 {
			if err := json.Unmarshal(args, &parsed); err != nil {
				return Result{}, fmt.Errorf("bad arguments: %w", err)
			}
		}
		headline := strings.TrimSpace(parsed.Description)
		if headline == "" {
			return Result{}, fmt.Errorf("description is required")
		}
		if err := deps.Cache.SetTurnStatus(ctx, sc, sid, headline); err != nil {
			return Result{}, fmt.Errorf("update session status: %w", err)
		}
		emitFrame(ctx, deps.Events, sc, sid, &llmv1.ConversationStreamFrame{
			SchemaVersion: 2,
			Frame: &llmv1.ConversationStreamFrame_SessionStatus{
				SessionStatus: &llmv1.EvSessionStatus{SessionId: sid, Headline: headline},
			},
		})
		return Result{Content: "ok"}, nil
	}
}

// setSessionTitleHandler updates the session's title column and emits the
// matching frame so a listening client renames its tab/list entry live.
func setSessionTitleHandler(deps SessionToolsDeps) Handler {
	return func(ctx context.Context, args json.RawMessage, inv Invocation) (Result, error) {
		sc, sid, err := requireSessionScope(inv)
		if err != nil {
			return Result{}, err
		}
		var parsed struct {
			Title string `json:"title"`
		}
		if len(args) > 0 {
			if err := json.Unmarshal(args, &parsed); err != nil {
				return Result{}, fmt.Errorf("bad arguments: %w", err)
			}
		}
		title := strings.TrimSpace(parsed.Title)
		if title == "" {
			return Result{}, fmt.Errorf("title is required")
		}
		err = deps.Sessions.UpdateSession(ctx, sc, sid, map[string]interface{}{"title": title})
		if errors.Is(err, gorm.ErrRecordNotFound) {
			if cerr := deps.Sessions.CreateSessionRowIfMissing(ctx, sc, sid); cerr != nil {
				return Result{}, fmt.Errorf("create session row: %w", cerr)
			}
			err = deps.Sessions.UpdateSession(ctx, sc, sid, map[string]interface{}{"title": title})
		}
		if err != nil {
			return Result{}, fmt.Errorf("update session title: %w", err)
		}
		emitFrame(ctx, deps.Events, sc, sid, &llmv1.ConversationStreamFrame{
			SchemaVersion: 2,
			Frame: &llmv1.ConversationStreamFrame_SessionTitleChanged{
				SessionTitleChanged: &llmv1.EvSessionTitleChanged{SessionId: sid, Title: title},
			},
		})
		return Result{Content: "ok"}, nil
	}
}

// askCardChoice/askCardFile/askCardPayload mirror packages/core/src/askCard.ts's
// AskCard JSON shape exactly (camelCase field names, decodeAskCard's expected
// keys) — this is the ONE place llmd constructs that wire shape in Go.
// `status: "pending"` is what packages/core's attachmentIsPendingCard checks
// to give a still-unanswered card a visible spot in the transcript even
// before any assistant text references it (see appendAnchorlessCardMessages).
type askCardChoice struct {
	// Choices is always the option labels, even when Options is set, so a
	// client that predates Options still renders (and answers) the card.
	Choices       []string `json:"choices"`
	AllowMultiple bool     `json:"allowMultiple"`
	// Options carries per-option explanations; same order as Choices.
	Options []askCardOption `json:"options,omitempty"`
}

// askCardOption is one explained choice. A non-empty Recommendation marks
// the option as recommended and says why; at most one option carries it.
type askCardOption struct {
	Label          string `json:"label"`
	Description    string `json:"description,omitempty"`
	Recommendation string `json:"recommendation,omitempty"`
}

// normalizeAskOptions trims every field and checks that labels are present
// and unique and that at most one option is recommended. It returns the
// cleaned options and their labels (the card's Choices). Errors are phrased
// for the model, which gets them back as the tool result and can retry.
func normalizeAskOptions(in []askCardOption) ([]askCardOption, []string, error) {
	out := make([]askCardOption, 0, len(in))
	labels := make([]string, 0, len(in))
	seen := make(map[string]struct{}, len(in))
	recommended := ""
	for i, o := range in {
		label := strings.TrimSpace(o.Label)
		if label == "" {
			return nil, nil, fmt.Errorf("options[%d].label is required", i)
		}
		if _, dup := seen[label]; dup {
			return nil, nil, fmt.Errorf("options[%d].label %q is a duplicate; labels must be unique", i, label)
		}
		seen[label] = struct{}{}
		rec := strings.TrimSpace(o.Recommendation)
		if rec != "" {
			if recommended != "" {
				return nil, nil, fmt.Errorf("only one option may have a recommendation (both %q and %q do)", recommended, label)
			}
			recommended = label
		}
		out = append(out, askCardOption{Label: label, Description: strings.TrimSpace(o.Description), Recommendation: rec})
		labels = append(labels, label)
	}
	return out, labels, nil
}

type askCardFile struct {
	AcceptedMimeTypes []string `json:"acceptedMimeTypes"`
}

type askCardPayload struct {
	ID                  string         `json:"id"`
	Title               string         `json:"title"`
	Text                string         `json:"text"`
	Type                string         `json:"type"`
	AllowAdditionalText bool           `json:"allowAdditionalText"`
	Choice              *askCardChoice `json:"choice,omitempty"`
	File                *askCardFile   `json:"file,omitempty"`
	Status              string         `json:"status"`
}

// askUserHandler asks the human a structured question by persisting an
// ask_card attachment (llmd's existing, already-shipped AskCard primitive —
// see packages/core/src/askCard.ts and packages/react's AskCardView) and
// returning immediately. There is no wait, no nonce, no SubmitToolResults:
// the model's tool call finishes as soon as the card exists, exactly like
// any other inline builtin. The user answers whenever they want, via the
// same UpsertSessionAttachment RPC a manually-created ask card already uses
// (packages/react/e2e/askCard.e2e.test.tsx); llmd never blocks a turn on it.
func askUserHandler(deps SessionToolsDeps) Handler {
	return func(ctx context.Context, args json.RawMessage, inv Invocation) (Result, error) {
		sc, sid, err := requireSessionScope(inv)
		if err != nil {
			return Result{}, err
		}
		if deps.Attachments == nil {
			return Result{}, fmt.Errorf("ask_user is not configured (no attachment store)")
		}
		var parsed struct {
			Title               string   `json:"title"`
			Text                string   `json:"text"`
			Type                string   `json:"type"`
			Choices             []string        `json:"choices"`
			Options             []askCardOption `json:"options"`
			AllowMultiple       bool            `json:"allow_multiple"`
			AllowAdditionalText bool            `json:"allow_additional_text"`
			AcceptedMimeTypes   []string        `json:"accepted_mime_types"`
		}
		if len(args) > 0 {
			if err := json.Unmarshal(args, &parsed); err != nil {
				return Result{}, fmt.Errorf("bad arguments: %w", err)
			}
		}
		title := strings.TrimSpace(parsed.Title)
		if title == "" {
			return Result{}, fmt.Errorf("title is required")
		}
		cardType := strings.TrimSpace(parsed.Type)
		switch cardType {
		case "text", "choice", "file":
		default:
			return Result{}, fmt.Errorf("type must be one of text, choice, file (got %q)", parsed.Type)
		}

		cardID := uuid.New().String()
		payload := askCardPayload{
			ID:                  cardID,
			Title:               title,
			Text:                parsed.Text,
			Type:                cardType,
			AllowAdditionalText: parsed.AllowAdditionalText,
			Status:              "pending",
		}
		switch cardType {
		case "choice":
			choice := &askCardChoice{Choices: parsed.Choices, AllowMultiple: parsed.AllowMultiple}
			if len(parsed.Options) > 0 {
				opts, labels, err := normalizeAskOptions(parsed.Options)
				if err != nil {
					return Result{}, err
				}
				choice.Options, choice.Choices = opts, labels
			}
			payload.Choice = choice
		case "file":
			payload.File = &askCardFile{AcceptedMimeTypes: parsed.AcceptedMimeTypes}
		}
		payloadJSON, err := json.Marshal(payload)
		if err != nil {
			return Result{}, fmt.Errorf("encode ask card: %w", err)
		}

		toolCallID := strings.TrimSpace(inv.ToolCallID)
		// The anchor need not resolve to a real message: ChatAttachmentRepo
		// falls back to the latest assistant turn when it doesn't (see
		// resolveAnchorTurn), which is exactly right here — the assistant
		// message THIS tool call belongs to may not be flushed to storage
		// yet at dispatch time.
		if err := deps.Attachments.Upsert(ctx, sc, sid, toolCallID, store.AttachmentInput{
			ID:              cardID,
			Kind:            "ask_card",
			PayloadJSON:     string(payloadJSON),
			AfterToolCallID: toolCallID,
		}); err != nil {
			return Result{}, fmt.Errorf("persist ask card: %w", err)
		}
		// Tell watching clients the card exists. Without this the card sits in
		// storage and only appears on a session reload. message_id is left
		// empty: the anchor may not be flushed yet, so clients render it as an
		// anchorless card (positioned via after_tool_call_id when possible).
		emitFrame(ctx, deps.Events, sc, sid, &llmv1.ConversationStreamFrame{
			SchemaVersion: 2,
			Frame: &llmv1.ConversationStreamFrame_AttachmentUpserted{
				AttachmentUpserted: &llmv1.EvAttachmentUpserted{
					SessionId:       sid,
					AttachmentId:    cardID,
					Kind:            "ask_card",
					AfterToolCallId: toolCallID,
					PayloadJson:     string(payloadJSON),
				},
			},
		})
		return Result{Content: fmt.Sprintf(
			"Asked the user (card %s). Your turn has ended automatically; do not generate further output now. "+
				"Their response will arrive as a new message once they answer.", cardID),
			// The card is up and the handler doesn't block, so the loop must
			// not re-query the model; only this success path ends the turn —
			// on any error above no card was shown and the model may explain.
			EndsTurn: true}, nil
	}
}
