package toolexec

import (
	"context"
	"encoding/json"
	"github.com/johnewart/pyxis/internal/serverutil"
	"strings"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/tenantscope"
	"github.com/johnewart/pyxis/toolregistry"
	"google.golang.org/protobuf/encoding/protojson"
	"gorm.io/gorm"
)

func newSessionToolsHarness(t *testing.T) (*toolregistry.Registry, *Handlers, SessionToolsDeps) {
	reg, handlers, deps, _ := newSessionToolsHarnessWithDB(t)
	return reg, handlers, deps
}

func newSessionToolsHarnessWithDB(t *testing.T) (*toolregistry.Registry, *Handlers, SessionToolsDeps, *gorm.DB) {
	t.Helper()
	db := store.NewTestSQLite(t)
	deps := SessionToolsDeps{
		Sessions:    store.NewSessionRepo(db),
		Events:      store.NewSessionEventRepo(db),
		Turns:       store.NewTurnRegister(db),
		Attachments: store.NewChatAttachmentRepo(db),
		Cache:       store.NewSessionCacheRepo(db),
	}
	reg := toolregistry.New()
	handlers := NewHandlers()
	if err := RegisterSessionTools(reg, handlers, deps); err != nil {
		t.Fatal(err)
	}
	return reg, handlers, deps, db
}

// TestRegisterSessionTools_AllHaveDescriptions pins that every session tool
// carries model-facing usage guidance in ToolDefinition.Description, not
// just a name + JSON schema. This matters most for ask_user: its
// description is the ONLY place the model is told the call returns
// immediately (no answer yet) and that it should end its turn if it needs
// the answer before continuing — get that wrong and the model plows ahead
// as if it already has a response.
func TestRegisterSessionTools_AllHaveDescriptions(t *testing.T) {
	reg, _, _ := newSessionToolsHarness(t)
	for _, name := range []string{
		ToolAddTodo, ToolUpdateTodo, ToolListTodos, ToolSetStatus, ToolSetSessionTitle, ToolAskUser,
	} {
		var def *llmv1.ToolDefinition
		for _, d := range reg.Snapshot() {
			if d.GetName() == name {
				def = d
			}
		}
		if def == nil {
			t.Fatalf("%s not registered", name)
		}
		if strings.TrimSpace(def.GetDescription()) == "" {
			t.Fatalf("%s has no Description", name)
		}
	}
	// ask_user's description must actually say what a model needs to know:
	// the call doesn't block, and ending the turn is how it waits.
	var askUser *llmv1.ToolDefinition
	for _, d := range reg.Snapshot() {
		if d.GetName() == ToolAskUser {
			askUser = d
		}
	}
	desc := strings.ToLower(askUser.GetDescription())
	if !strings.Contains(desc, "ends your") || !strings.Contains(desc, "turn") {
		t.Fatalf("ask_user description doesn't tell the model its turn ends automatically: %q", askUser.GetDescription())
	}
}

// TestAskUser_FirstTurnOfSessionWithNoPriorHistory pins the case
// ChatAttachmentRepo.ensureAnchorTurn exists for: an agent calling ask_user
// as the very first tool call of a brand-new session, before any assistant
// turn has ever been persisted. Upsert must synthesize an honest, empty
// assistant placeholder row to anchor to rather than failing — this used to
// return ErrAttachmentAnchorNotFound.
func TestAskUser_FirstTurnOfSessionWithNoPriorHistory(t *testing.T) {
	_, handlers, deps, db := newSessionToolsHarnessWithDB(t)
	const ws, sid = "ws-ask-first-turn", "sess-ask-first-turn"

	var priorRows int64
	if err := db.Model(&store.ChatMessageRow{}).
		Where("tenant_id = ? AND scope_id = ? AND session_id = ?", ws, "", sid).
		Count(&priorRows).Error; err != nil {
		t.Fatal(err)
	}
	if priorRows != 0 {
		t.Fatalf("test setup: expected zero prior rows, got %d", priorRows)
	}

	_, err := callSessionTool(t, handlers, ToolAskUser, ws, sid,
		`{"title":"Should I proceed?","type":"text"}`)
	if err != nil {
		t.Fatalf("ask_user on a session with no prior history: %v", err)
	}

	rows, err := deps.Attachments.LoadRows(context.Background(), serverutil.Scope{TenantID: ws}, sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 attachment row, got %d", len(rows))
	}
}

func callSessionTool(t *testing.T, handlers *Handlers, name, ws, sid, argsJSON string) (string, error) {
	t.Helper()
	fn, ok := handlers.Lookup(name)
	if !ok {
		t.Fatalf("handler %q not registered", name)
	}
	inv := Invocation{Scope: tenantscope.Scope{TenantID: ws}, SessionID: sid, ToolCallID: "tc-" + name}
	res, err := fn(context.Background(), json.RawMessage(argsJSON), inv)
	return res.Content, err
}

func TestRegisterSessionTools_RegistersAllSix(t *testing.T) {
	reg, handlers, _ := newSessionToolsHarness(t)
	for _, name := range []string{ToolAddTodo, ToolUpdateTodo, ToolListTodos, ToolSetStatus, ToolSetSessionTitle, ToolAskUser} {
		found := false
		for _, d := range reg.Snapshot() {
			if d.GetName() == name {
				found = true
				if d.GetInlineBuiltin() == nil {
					t.Fatalf("%s registered but not as inline builtin", name)
				}
			}
		}
		if !found {
			t.Fatalf("%s not registered in registry", name)
		}
		if _, ok := handlers.Lookup(name); !ok {
			t.Fatalf("%s has no handler", name)
		}
	}
}

// TestAskUser_PersistsPendingCardAndReturnsImmediately pins ask_user's
// actual shape: an ordinary inline builtin, like the other five, that
// persists an ask_card attachment (llmd's existing AskCard primitive) and
// returns without waiting on anything — no nonce, no blocking, no
// SubmitToolResults round trip. Deliberately does NOT seed a prior assistant
// turn first — ChatAttachmentRepo.ensureAnchorTurn must handle a completely
// fresh session (see TestAskUser_FirstTurnOfSessionWithNoPriorHistory for
// the same case named explicitly).
func TestAskUser_PersistsPendingCardAndReturnsImmediately(t *testing.T) {
	_, handlers, deps, _ := newSessionToolsHarnessWithDB(t)
	const ws, sid = "ws-ask", "sess-ask"

	_, err := callSessionTool(t, handlers, ToolAskUser, ws, sid,
		`{"title":"Should I proceed?","text":"Pick one.","type":"choice","choices":["Yes","No"]}`)
	if err != nil {
		t.Fatalf("ask_user: %v", err)
	}

	rows, err := deps.Attachments.LoadRows(context.Background(), serverutil.Scope{TenantID: ws}, sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 attachment row, got %d", len(rows))
	}
	var payload struct {
		Kind        string `json:"kind"`
		PayloadJSON string `json:"payload_json"`
	}
	if err := json.Unmarshal([]byte(rows[0].Content), &payload); err != nil {
		t.Fatalf("decode attachment row: %v", err)
	}
	if payload.Kind != "ask_card" {
		t.Fatalf("attachment kind = %q, want ask_card", payload.Kind)
	}
	var card struct {
		Title  string `json:"title"`
		Type   string `json:"type"`
		Status string `json:"status"`
		Choice struct {
			Choices []string `json:"choices"`
		} `json:"choice"`
	}
	if err := json.Unmarshal([]byte(payload.PayloadJSON), &card); err != nil {
		t.Fatalf("decode ask card payload: %v", err)
	}
	if card.Title != "Should I proceed?" || card.Type != "choice" || card.Status != "pending" {
		t.Fatalf("unexpected ask card: %+v", card)
	}
	if len(card.Choice.Choices) != 2 {
		t.Fatalf("expected 2 choices, got %+v", card.Choice.Choices)
	}
}

// TestRegisterSessionTools_SchemasAreValidJSON: the schemas are hand-built
// string literals, so a missing brace or comma would otherwise only show up
// when a provider rejects the tool list.
func TestRegisterSessionTools_SchemasAreValidJSON(t *testing.T) {
	reg, _, _ := newSessionToolsHarness(t)
	for _, d := range reg.Snapshot() {
		var schema map[string]any
		if err := json.Unmarshal([]byte(d.GetParametersSchemaJson()), &schema); err != nil {
			t.Errorf("%s: parameters schema is not valid JSON: %v", d.GetName(), err)
		}
	}
	// ask_user's options items must declare label as required.
	for _, d := range reg.Snapshot() {
		if d.GetName() != ToolAskUser {
			continue
		}
		var schema struct {
			Properties struct {
				Options struct {
					Items struct {
						Required []string `json:"required"`
					} `json:"items"`
				} `json:"options"`
			} `json:"properties"`
		}
		_ = json.Unmarshal([]byte(d.GetParametersSchemaJson()), &schema)
		if req := schema.Properties.Options.Items.Required; len(req) != 1 || req[0] != "label" {
			t.Fatalf("ask_user options.items.required = %v, want [label]", req)
		}
	}
}

// askUserCardPayload runs ask_user and decodes the single ask_card it stored.
func askUserCardPayload(t *testing.T, args string) map[string]any {
	t.Helper()
	_, handlers, deps, _ := newSessionToolsHarnessWithDB(t)
	const ws, sid = "ws-opts", "sess-opts"
	if _, err := callSessionTool(t, handlers, ToolAskUser, ws, sid, args); err != nil {
		t.Fatalf("ask_user: %v", err)
	}
	rows, err := deps.Attachments.LoadRows(context.Background(), serverutil.Scope{TenantID: ws}, sid)
	if err != nil || len(rows) != 1 {
		t.Fatalf("want 1 attachment row, got %d (%v)", len(rows), err)
	}
	var row struct {
		PayloadJSON string `json:"payload_json"`
	}
	if err := json.Unmarshal([]byte(rows[0].Content), &row); err != nil {
		t.Fatal(err)
	}
	var card map[string]any
	if err := json.Unmarshal([]byte(row.PayloadJSON), &card); err != nil {
		t.Fatal(err)
	}
	return card
}

func TestAskUser_OptionsCarryDescriptionsAndRecommendation(t *testing.T) {
	card := askUserCardPayload(t, `{"title":"Pick DB","type":"choice",
		"choices":["ignored"],
		"options":[
			{"label":" Postgres ","description":" Uses the existing pool. "},
			{"label":"SQLite","description":"Single file.","recommendation":" Matches session storage. "}
		]}`)
	choice, _ := card["choice"].(map[string]any)
	if choice == nil {
		t.Fatalf("no choice body: %v", card)
	}
	// choices = the option labels (trimmed), so pre-options clients still work;
	// options wins over a choices list given alongside it.
	if got, _ := json.Marshal(choice["choices"]); string(got) != `["Postgres","SQLite"]` {
		t.Fatalf("choices = %s, want the option labels", got)
	}
	raw, _ := json.Marshal(choice["options"])
	var got []askCardOption
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	want := []askCardOption{
		{Label: "Postgres", Description: "Uses the existing pool."},
		{Label: "SQLite", Description: "Single file.", Recommendation: "Matches session storage."},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("options = %+v\nwant      %+v", got, want)
	}
}

func TestAskUser_PlainChoicesStillWorkWithoutOptions(t *testing.T) {
	card := askUserCardPayload(t, `{"title":"Proceed?","type":"choice","choices":["Yes","No"]}`)
	choice, _ := card["choice"].(map[string]any)
	if _, has := choice["options"]; has {
		t.Fatalf("options should be omitted when not given: %v", choice)
	}
	if got, _ := json.Marshal(choice["choices"]); string(got) != `["Yes","No"]` {
		t.Fatalf("choices = %s", got)
	}
}

func TestAskUser_RejectsBadOptions(t *testing.T) {
	// The DB harness, so the attachment store exists and any error comes from
	// option validation, not from ask_user being unconfigured.
	_, handlers, deps, _ := newSessionToolsHarnessWithDB(t)
	defer func() {
		rows, _ := deps.Attachments.LoadRows(context.Background(), serverutil.Scope{TenantID: "ws"}, "sess")
		if len(rows) != 0 {
			t.Errorf("rejected cards must not be stored, got %d rows", len(rows))
		}
	}()
	cases := map[string]string{
		"empty label":         `[{"label":"  "}]`,
		"duplicate label":     `[{"label":"A"},{"label":"A"}]`,
		"two recommendations": `[{"label":"A","recommendation":"x"},{"label":"B","recommendation":"y"}]`,
	}
	for name, opts := range cases {
		args := `{"title":"q","type":"choice","options":` + opts + `}`
		if _, err := callSessionTool(t, handlers, ToolAskUser, "ws", "sess", args); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestAskUser_RequiresTitleAndValidType(t *testing.T) {
	_, handlers, _ := newSessionToolsHarness(t)
	if _, err := callSessionTool(t, handlers, ToolAskUser, "ws", "sess", `{"type":"text"}`); err == nil {
		t.Fatal("expected error for missing title")
	}
	if _, err := callSessionTool(t, handlers, ToolAskUser, "ws", "sess", `{"title":"x","type":"nope"}`); err == nil {
		t.Fatal("expected error for invalid type")
	}
}

func TestAddTodo_ListTodos_RoundTrip(t *testing.T) {
	_, handlers, deps := newSessionToolsHarness(t)
	const ws, sid = "ws-1", "sess-1"

	// Advance to turn 2 before adding, so CreatedTurnSeq is observable.
	if _, err := deps.Turns.Advance(context.Background(), serverutil.Scope{TenantID: ws}, sid, time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := deps.Turns.Advance(context.Background(), serverutil.Scope{TenantID: ws}, sid, time.Now(), ""); err != nil {
		t.Fatal(err)
	}

	out, err := callSessionTool(t, handlers, ToolAddTodo, ws, sid, `{"description":"find a fish"}`)
	if err != nil {
		t.Fatalf("add_todo: %v", err)
	}
	// add_todo returns the FULL current list (same shape as list_todos/
	// update_todo), not just the item it added.
	afterAdd := store.ParseTodos(out)
	if len(afterAdd) != 1 {
		t.Fatalf("add_todo result: got %d items, want 1 (%s)", len(afterAdd), out)
	}
	added := afterAdd[0]
	if added.Description != "find a fish" || added.Status != store.TodoPending {
		t.Fatalf("unexpected added todo: %#v", added)
	}
	if added.CreatedTurnSeq != 2 {
		t.Fatalf("CreatedTurnSeq = %d, want 2", added.CreatedTurnSeq)
	}

	listed, err := callSessionTool(t, handlers, ToolListTodos, ws, sid, `{}`)
	if err != nil {
		t.Fatalf("list_todos: %v", err)
	}
	items := store.ParseTodos(listed)
	if len(items) != 1 || items[0].ID != added.ID {
		t.Fatalf("list_todos mismatch: %#v", items)
	}

	// Different session must not see this session's todos.
	otherListed, err := callSessionTool(t, handlers, ToolListTodos, ws, "sess-other", `{}`)
	if err != nil {
		t.Fatalf("list_todos (other session): %v", err)
	}
	if len(store.ParseTodos(otherListed)) != 0 {
		t.Fatalf("todos leaked across sessions: %s", otherListed)
	}
}

func TestUpdateTodo_TracksStartedAndCompletedTurn(t *testing.T) {
	_, handlers, deps := newSessionToolsHarness(t)
	const ws, sid = "ws-2", "sess-2"

	out, err := callSessionTool(t, handlers, ToolAddTodo, ws, sid, `{"description":"contemplate the universe"}`)
	if err != nil {
		t.Fatalf("add_todo: %v", err)
	}
	addedList := store.ParseTodos(out)
	if len(addedList) != 1 {
		t.Fatalf("add_todo result: got %d items, want 1 (%s)", len(addedList), out)
	}
	added := addedList[0]

	// Advance turn, then mark in_progress.
	if _, err := deps.Turns.Advance(context.Background(), serverutil.Scope{TenantID: ws}, sid, time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := callSessionTool(t, handlers, ToolUpdateTodo, ws, sid,
		`{"id":"`+added.ID+`","status":"in_progress"}`); err != nil {
		t.Fatalf("update_todo(in_progress): %v", err)
	}

	// Advance again, then mark completed.
	if _, err := deps.Turns.Advance(context.Background(), serverutil.Scope{TenantID: ws}, sid, time.Now(), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := callSessionTool(t, handlers, ToolUpdateTodo, ws, sid,
		`{"id":"`+added.ID+`","status":"completed"}`); err != nil {
		t.Fatalf("update_todo(completed): %v", err)
	}

	listed, err := callSessionTool(t, handlers, ToolListTodos, ws, sid, `{}`)
	if err != nil {
		t.Fatal(err)
	}
	items := store.ParseTodos(listed)
	if len(items) != 1 {
		t.Fatalf("expected 1 todo, got %d", len(items))
	}
	got := items[0]
	if got.Status != store.TodoCompleted {
		t.Fatalf("status = %q, want completed", got.Status)
	}
	if got.StartedTurnSeq != 1 {
		t.Fatalf("StartedTurnSeq = %d, want 1", got.StartedTurnSeq)
	}
	if got.CompletedTurnSeq != 2 {
		t.Fatalf("CompletedTurnSeq = %d, want 2", got.CompletedTurnSeq)
	}
}

func TestUpdateTodo_UnknownID(t *testing.T) {
	_, handlers, _ := newSessionToolsHarness(t)
	if _, err := callSessionTool(t, handlers, ToolUpdateTodo, "ws-3", "sess-3",
		`{"id":"nope","status":"completed"}`); err == nil {
		t.Fatal("expected error for unknown todo id")
	}
}

// TestAskUser_EmitsAttachmentUpsertedFrame pins live delivery of a
// tool-created ask card. Before this frame existed the card was persisted
// but no client learned about it until a session reload — agents reported
// "I asked" and the user saw nothing.
func TestAskUser_EmitsAttachmentUpsertedFrame(t *testing.T) {
	_, handlers, deps := newSessionToolsHarness(t)
	const ws, sid = "ws-ask-frame", "sess-ask-frame"

	// Replay path (MCP / no live stream): persisted to SessionEventRepo.
	if _, err := callSessionTool(t, handlers, ToolAskUser, ws, sid, `{"title":"Proceed?","type":"text"}`); err != nil {
		t.Fatalf("ask_user: %v", err)
	}
	rows, err := deps.Events.GetAllEvents(context.Background(), serverutil.Scope{TenantID: ws}, sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 event, got %d", len(rows))
	}
	frame := &llmv1.ConversationStreamFrame{}
	if err := protojson.Unmarshal([]byte(rows[0].EventJSON), frame); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	up := frame.GetAttachmentUpserted()
	if up == nil {
		t.Fatalf("frame has no attachment_upserted: %s", rows[0].EventJSON)
	}
	if up.GetSessionId() != sid || up.GetKind() != "ask_card" || up.GetAttachmentId() == "" ||
		up.GetAfterToolCallId() != "tc-"+ToolAskUser {
		t.Fatalf("unexpected attachment_upserted: %+v", up)
	}
	var card struct {
		ID     string `json:"id"`
		Title  string `json:"title"`
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(up.GetPayloadJson()), &card); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if card.ID != up.GetAttachmentId() || card.Title != "Proceed?" || card.Status != "pending" {
		t.Fatalf("unexpected card payload: %+v", card)
	}

	// Live path (ChatStream): goes to the emitter, not straight to the repo.
	fn, _ := handlers.Lookup(ToolAskUser)
	var live []*llmv1.ConversationStreamFrame
	ctx := WithFrameEmitter(context.Background(), func(f *llmv1.ConversationStreamFrame) error {
		live = append(live, f)
		return nil
	})
	inv := Invocation{Scope: tenantscope.Scope{TenantID: ws}, SessionID: sid, ToolCallID: "tc-live"}
	if _, err := fn(ctx, json.RawMessage(`{"title":"Again?","type":"text"}`), inv); err != nil {
		t.Fatalf("ask_user (live): %v", err)
	}
	if len(live) != 1 || live[0].GetAttachmentUpserted() == nil {
		t.Fatalf("expected one live attachment_upserted frame, got %+v", live)
	}
}

func TestSetStatus_EmitsSessionStatusFrame(t *testing.T) {
	_, handlers, deps := newSessionToolsHarness(t)
	const ws, sid = "ws-4", "sess-4"

	if _, err := callSessionTool(t, handlers, ToolSetStatus, ws, sid, `{"description":"Looking for a fish..."}`); err != nil {
		t.Fatalf("set_status: %v", err)
	}

	rows, err := deps.Events.GetAllEvents(context.Background(), serverutil.Scope{TenantID: ws}, sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 event, got %d", len(rows))
	}
	frame := &llmv1.ConversationStreamFrame{}
	if err := protojson.Unmarshal([]byte(rows[0].EventJSON), frame); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	status := frame.GetSessionStatus()
	if status == nil {
		t.Fatalf("frame has no session_status: %s", rows[0].EventJSON)
	}
	if status.GetHeadline() != "Looking for a fish..." || status.GetSessionId() != sid {
		t.Fatalf("unexpected session_status frame: %+v", status)
	}
}

// set_status stores the headline in the session cache (so a reconnecting
// client can seed it), and clearing empties it again.
func TestSetStatus_PersistsAndClears(t *testing.T) {
	_, handlers, deps := newSessionToolsHarness(t)
	const ws, sid = "ws-4b", "sess-4b"
	ctx := context.Background()
	sc := serverutil.Scope{TenantID: ws}

	// Clearing when nothing is stored is a no-op, not an error.
	if err := deps.Cache.SetTurnStatus(ctx, sc, sid, ""); err != nil {
		t.Fatalf("clear with nothing stored: %v", err)
	}

	if _, err := callSessionTool(t, handlers, ToolSetStatus, ws, sid, `{"description":"Grepping for falafel"}`); err != nil {
		t.Fatalf("set_status: %v", err)
	}
	got, err := deps.Cache.TurnStatus(ctx, sc, sid)
	if err != nil {
		t.Fatal(err)
	}
	if got != "Grepping for falafel" {
		t.Fatalf("status = %q, want the set_status headline", got)
	}

	if err := deps.Cache.SetTurnStatus(ctx, sc, sid, ""); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if got, err = deps.Cache.TurnStatus(ctx, sc, sid); err != nil || got != "" {
		t.Fatalf("status after clear = %q (err %v), want empty", got, err)
	}
}

func TestSetSessionTitle_UpdatesRowAndEmitsFrame(t *testing.T) {
	_, handlers, deps := newSessionToolsHarness(t)
	const ws, sid = "ws-5", "sess-5"

	if _, err := callSessionTool(t, handlers, ToolSetSessionTitle, ws, sid, `{"title":"Debugging the fish finder"}`); err != nil {
		t.Fatalf("set_session_title: %v", err)
	}

	row, err := deps.Sessions.GetSession(context.Background(), serverutil.Scope{TenantID: ws}, sid)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if row.Title != "Debugging the fish finder" {
		t.Fatalf("session title = %q, want %q", row.Title, "Debugging the fish finder")
	}

	rows, err := deps.Events.GetAllEvents(context.Background(), serverutil.Scope{TenantID: ws}, sid)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("expected 1 event, got %d", len(rows))
	}
	frame := &llmv1.ConversationStreamFrame{}
	if err := protojson.Unmarshal([]byte(rows[0].EventJSON), frame); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	changed := frame.GetSessionTitleChanged()
	if changed == nil || changed.GetTitle() != "Debugging the fish finder" {
		t.Fatalf("unexpected session_title_changed frame: %+v", changed)
	}
}

func TestAddTodo_RequiresSessionScope(t *testing.T) {
	_, handlers, _ := newSessionToolsHarness(t)
	fn, _ := handlers.Lookup(ToolAddTodo)
	_, err := fn(context.Background(), json.RawMessage(`{"description":"x"}`), Invocation{})
	if err == nil {
		t.Fatal("expected error with no workspace/session bound")
	}
	if !strings.Contains(err.Error(), "session") {
		t.Fatalf("unexpected error: %v", err)
	}
}
