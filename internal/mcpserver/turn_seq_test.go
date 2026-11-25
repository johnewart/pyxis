package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/server"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
)

// TestTurnSeq verifies the MCP-transport fallback turn boundary
// (session/advanceTurn) advances the SAME TurnRegister instance shared with
// the gRPC ChatStream chokepoint, gap-free and without collision.
func TestTurnSeq(t *testing.T) {
	t.Run("AdvanceTurnRPCAdvancesSharedRegister", func(t *testing.T) {
		mgr, db := testManager(t)
		ctx := context.Background()
		if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{SessionId: "a_turnseq"}, serverutil.Scope{TenantID: testTenant}); err != nil {
			t.Fatal(err)
		}

		turnReg := store.NewTurnRegister(db)
		llm := &server.LlmServer{
			SessionRuntimes: mgr,
			TurnRegister:    turnReg,
		}
		srv := New(Config{
			DB:              db,
			SessionRuntimes: mgr,
			LLM:             llm,
		})
		ht := httptest.NewServer(srv.Handler())
		t.Cleanup(ht.Close)

		advance := func(turnID string) int64 {
			t.Helper()
			body := map[string]any{
				"jsonrpc": "2.0",
				"id":      1,
				"method":  "session/advanceTurn",
				"params":  map[string]any{"turnId": turnID},
			}
			raw, err := json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
			req, err := http.NewRequest(http.MethodPost, ht.URL+"/mcp", bytes.NewReader(raw))
			if err != nil {
				t.Fatal(err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("X-Llmd-Agent-Id", "a_turnseq")
			req.Header.Set("X-Llmd-Session-Id", "sess_mcp_turnseq")
			serverutil.Scope{TenantID: "test_workspace"}.ApplyHTTPHeader(req.Header)
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				t.Fatalf("session/advanceTurn: status %d", resp.StatusCode)
			}
			var out struct {
				Result struct {
					TurnSeq int64 `json:"turnSeq"`
				} `json:"result"`
				Error *jsonRPCError `json:"error"`
			}
			if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
				t.Fatal(err)
			}
			if out.Error != nil {
				t.Fatalf("session/advanceTurn error: %+v", out.Error)
			}
			return out.Result.TurnSeq
		}

		if seq := advance("mcp_turn_1"); seq != 1 {
			t.Fatalf("first append: want turn_seq=1, got %d", seq)
		}
		if seq := advance("mcp_turn_2"); seq != 2 {
			t.Fatalf("second append: want turn_seq=2 (no gap/collision), got %d", seq)
		}

		// A tool call attributed after the advance sees the newly-advanced seq
		// via the same shared register (env.TurnSeq stamping itself is phase-2c;
		// this proves the register state a future tool-invocation site would read).
		if got := turnReg.Current(serverutil.Scope{TenantID: "test_workspace"}, "sess_mcp_turnseq"); got != 2 {
			t.Fatalf("tool call attribution: want current seq=2, got %d", got)
		}
	})

	t.Run("BootstrapAfterRestartNoCollision", func(t *testing.T) {
		db := store.NewTestSQLite(t)
		ctx := context.Background()

		reg1 := store.NewTurnRegister(db)
		for i := 0; i < 2; i++ {
			if _, err := reg1.Advance(ctx, serverutil.Scope{}, "sess_mcp_restart", time.Now().UTC(), ""); err != nil {
				t.Fatalf("advance: %v", err)
			}
		}

		// Simulate an llmd restart: a fresh in-memory register over the same
		// persisted db must Bootstrap from MAX(turn_seq) before advancing again,
		// or it would collide with the PRIMARY KEY(session_id, turn_seq) rows
		// already committed by the pre-restart process.
		reg2 := store.NewTurnRegister(db)
		if _, err := reg2.Bootstrap(ctx, serverutil.Scope{}, "sess_mcp_restart"); err != nil {
			t.Fatalf("bootstrap: %v", err)
		}
		next, err := reg2.Advance(ctx, serverutil.Scope{}, "sess_mcp_restart", time.Now().UTC(), "")
		if err != nil {
			t.Fatalf("advance after bootstrap: %v", err)
		}
		if next != 3 {
			t.Fatalf("expected next seq 3 with no collision, got %d", next)
		}
	})
}
