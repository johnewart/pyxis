package server

import (
	"context"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/sessionmanager"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/internal/toolexec"
	"github.com/johnewart/pyxis/internal/toolresult"
	"github.com/johnewart/pyxis/toolregistry"
	"gorm.io/gorm"
)

func TestRecordToolUsage_WritesRowPerSource(t *testing.T) {
	db := store.NewTestSQLite(t)
	s := &LlmServer{ToolUsage: store.NewToolUsageRepo(db)}
	for _, src := range []string{ToolUsageSourceChat, ToolUsageSourceInvokeTool, ToolUsageSourceMCP} {
		s.RecordToolUsageScoped(context.Background(), testScope, "tool_x", src, "sess-"+src, true, 5*time.Millisecond)
	}
	var rows []store.ToolUsageRow
	if err := db.Order("id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows=%d", len(rows))
	}
	for i, src := range []string{ToolUsageSourceChat, ToolUsageSourceInvokeTool, ToolUsageSourceMCP} {
		r := rows[i]
		if r.Source != src || r.ToolName != "tool_x" || !r.Success || r.DurationMs != 5 || r.TenantID != testScope.TenantID || r.SessionID != "sess-"+src {
			t.Fatalf("row %d: %+v", i, r)
		}
	}
}

func TestRecordToolUsage_FailingStoreDoesNotPanicOrBlock(t *testing.T) {
	db := store.NewTestSQLite(t)
	s := &LlmServer{ToolUsage: store.NewToolUsageRepo(db)}
	if err := db.Migrator().DropTable(&store.ToolUsageRow{}); err != nil {
		t.Fatal(err)
	}
	s.RecordToolUsageScoped(context.Background(), testScope, "tool_x", ToolUsageSourceChat, "", false, time.Millisecond)
	var nilSrv *LlmServer
	nilSrv.RecordToolUsageScoped(context.Background(), testScope, "tool_x", ToolUsageSourceMCP, "", true, 0)
}

// newUsageServer registers host_related (succeeds) and host_fetch (fails for a
// missing key) as inline builtins, with tool usage persisted to the returned db.
func newUsageServer(t *testing.T) (*LlmServer, *gorm.DB) {
	t.Helper()
	db := store.NewTestSQLite(t)
	repo := store.NewToolUsageRepo(db)
	reg := toolregistry.New()
	handlers := toolexec.NewHandlers()
	if _, err := InstallBuiltinTools(reg, handlers,
		registerFakeRelatedTool(map[string]map[string][]string{"ws-a": {"k": {"k"}}}),
		registerFakeFetchTool(map[string]map[string]string{"ws-a": {}})); err != nil {
		t.Fatal(err)
	}
	return &LlmServer{
		Tools:           reg,
		Handlers:        handlers,
		SessionRuntimes: &sessionmanager.Manager{},
		ToolUsage:       repo,
	}, db
}

func toolUsageRows(t *testing.T, db *gorm.DB) []store.ToolUsageRow {
	t.Helper()
	var rows []store.ToolUsageRow
	if err := db.Order("id").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

func assertUsageRow(t *testing.T, r store.ToolUsageRow, tool, source, session string, success bool) {
	t.Helper()
	if r.ToolName != tool || r.Source != source || r.Success != success || r.SessionID != session {
		t.Fatalf("row = %+v, want tool=%s source=%s session=%q success=%v", r, tool, source, session, success)
	}
}

func TestInvokeTool_RecordsUsageRow(t *testing.T) {
	sess := "sess-1"
	s, db := newUsageServer(t)
	ctx := wsScope("ws-a")
	if _, err := s.InvokeTool(ctx, &llmv1.InvokeToolRequest{ToolId: "host_related", SessionId: &sess, ArgumentsJson: `{"key":"k"}`}); err != nil {
		t.Fatal(err)
	}
	resp, err := s.InvokeTool(ctx, &llmv1.InvokeToolRequest{ToolId: "host_fetch", SessionId: &sess, ArgumentsJson: `{"key":"missing"}`})
	if err != nil || !resp.GetIsError() {
		t.Fatalf("expected tool failure, err=%v resp=%v", err, resp)
	}
	rows := toolUsageRows(t, db)
	if len(rows) != 2 {
		t.Fatalf("rows=%d", len(rows))
	}
	assertUsageRow(t, rows[0], "host_related", ToolUsageSourceInvokeTool, "sess-1", true)
	assertUsageRow(t, rows[1], "host_fetch", ToolUsageSourceInvokeTool, "sess-1", false)
}

func TestExecuteToolForStream_RecordsUsageRow(t *testing.T) {
	s, db := newUsageServer(t)
	ctx := wsScope("ws-a")
	run := func(name, args string) {
		def := lookupToolDefinition(s.Tools, name)
		tc := &llmv1.ToolCall{Id: "call-" + name, Name: name, ArgumentsJson: args}
		if _, _, err := s.executeToolForStream(ctx, "", "sess-2", "", def, tc, false, nil, toolresult.Settings{}); err != nil {
			t.Fatal(err)
		}
	}
	run("host_related", `{"key":"k"}`)
	run("host_fetch", `{"key":"missing"}`)
	rows := toolUsageRows(t, db)
	if len(rows) != 2 {
		t.Fatalf("rows=%d", len(rows))
	}
	assertUsageRow(t, rows[0], "host_related", ToolUsageSourceChat, "sess-2", true)
	assertUsageRow(t, rows[1], "host_fetch", ToolUsageSourceChat, "sess-2", false)
}

func TestInvokeTool_FailingUsageStoreDoesNotChangeResult(t *testing.T) {
	good, _ := newUsageServer(t)
	bad, badDB := newUsageServer(t)
	if err := badDB.Migrator().DropTable(&store.ToolUsageRow{}); err != nil {
		t.Fatal(err)
	}
	ctx := wsScope("ws-a")
	for _, req := range []*llmv1.InvokeToolRequest{
		{ToolId: "host_related", ArgumentsJson: `{"key":"k"}`},
		{ToolId: "host_fetch", ArgumentsJson: `{"key":"missing"}`},
	} {
		want, werr := good.InvokeTool(ctx, req)
		got, gerr := bad.InvokeTool(ctx, req)
		if werr != nil || gerr != nil {
			t.Fatalf("errors: good=%v bad=%v", werr, gerr)
		}
		if got.GetResultJson() != want.GetResultJson() || got.GetIsError() != want.GetIsError() ||
			got.GetErrorMessage() != want.GetErrorMessage() {
			t.Fatalf("%s: result changed by failing store: got %v want %v", req.GetToolId(), got, want)
		}
	}
}
