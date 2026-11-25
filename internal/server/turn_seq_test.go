package server

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/session"
	"github.com/johnewart/pyxis/internal/llm/providerkind"
	"github.com/johnewart/pyxis/internal/llm/providers"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/toolregistry"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
)

// TestTurnSeq verifies TurnRegister.Advance is invoked exactly once per
// ChatStream turn-start (EvAssistantTurnStarted) and that the minted turn_seq
// is stamped onto the outbound wire frame before it is streamed to the client.
func TestTurnSeq(t *testing.T) {
	t.Run("ChatStreamAdvancesAndStampsWireFrame", func(t *testing.T) {
		ctx := testScope.OutgoingContext(context.Background())

		be := &stubBackend{}
		reg, err := providers.NewRegistryFromEntries("stub_turnseq", map[string]*providers.Entry{
			"stub_turnseq": {
				ID:           "stub_turnseq",
				Kind:         providerkind.OpenAI,
				Backend:      be,
				DefaultModel: "gpt-4o-mini",
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		comp := providers.NewComposite(reg, nil)
		mgr, db := sessionManagerWithSQLite(t, session.HubOptions{KeepEventLog: true})
		if err := mgr.EnsureSession(ctx, &llmv1.EnsureSessionRequest{
			SessionId:     "sess_turnseq",
			LlmProviderId: strPtr("stub_turnseq"),
		}, testScope); err != nil {
			t.Fatal(err)
		}

		turnReg := store.NewTurnRegister(db)

		lis := bufconn.Listen(bufSize)
		srv := grpc.NewServer()
		llmv1.RegisterLlmServiceServer(srv, &LlmServer{
			Providers:       comp,
			Tools:           toolregistry.New(),
			SessionRuntimes: mgr,
			TurnRegister:    turnReg,
		})
		go func() { _ = srv.Serve(lis) }()
		t.Cleanup(func() { srv.Stop() })

		conn, err := grpc.DialContext(ctx, "bufnet",
			grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) { return lis.Dial() }),
			grpc.WithTransportCredentials(insecure.NewCredentials()))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = conn.Close() })
		client := llmv1.NewLlmServiceClient(conn)

		collect := func(t *testing.T, st llmv1.LlmService_ChatStreamClient) []*llmv1.ConversationStreamFrame {
			t.Helper()
			var frames []*llmv1.ConversationStreamFrame
			for {
				f, err := st.Recv()
				if err == io.EOF {
					return frames
				}
				if err != nil {
					t.Fatal(err)
				}
				frames = append(frames, f)
			}
		}
		turnSeqOf := func(t *testing.T, frames []*llmv1.ConversationStreamFrame) int64 {
			t.Helper()
			for _, f := range frames {
				if ts := f.GetAssistantTurnStarted(); ts != nil {
					return ts.GetTurnSeq()
				}
			}
			t.Fatal("no assistant_turn_started frame observed")
			return 0
		}

		st1, err := client.ChatStream(ctx, &llmv1.ChatStreamRequest{
			Messages:       []*llmv1.Message{{Role: "user", Content: "one", Id: "u1"}},
			SessionId:      strPtr("sess_turnseq"),
			PersistSession: false,
		})
		if err != nil {
			t.Fatal(err)
		}
		if seq := turnSeqOf(t, collect(t, st1)); seq != 1 {
			t.Fatalf("first turn-start: want turn_seq=1, got %d", seq)
		}

		st2, err := client.ChatStream(ctx, &llmv1.ChatStreamRequest{
			Messages:       []*llmv1.Message{{Role: "user", Content: "two", Id: "u2"}},
			SessionId:      strPtr("sess_turnseq"),
			PersistSession: false,
		})
		if err != nil {
			t.Fatal(err)
		}
		if seq := turnSeqOf(t, collect(t, st2)); seq != 2 {
			t.Fatalf("second turn-start: want turn_seq=2, got %d", seq)
		}
	})

	// LateFinishRetainsIssuingTurnSeq proves the register semantics tool
	// invocation stamping (phase-2c) will rely on: a value captured from
	// TurnRegister.Current at a tool's invocation time reflects the turn that
	// was active then, and is NOT retroactively bumped by a later Advance for
	// a subsequent turn observed before the tool finishes executing.
	t.Run("LateFinishRetainsIssuingTurnSeq", func(t *testing.T) {
		db := store.NewTestSQLite(t)
		reg := store.NewTurnRegister(db)
		ctx := context.Background()

		firstSeq, err := reg.Advance(ctx, serverutil.Scope{}, "sess_late", time.Now().UTC(), "turn_1")
		if err != nil {
			t.Fatalf("advance turn 1: %v", err)
		}
		if firstSeq != 1 {
			t.Fatalf("expected turn 1 seq=1, got %d", firstSeq)
		}

		// Simulate a tool invocation starting during turn 1: it captures the
		// current seq at invocation time (what env.TurnSeq stamping will do).
		toolIssuingSeq := reg.Current(serverutil.Scope{}, "sess_late")
		if toolIssuingSeq != 1 {
			t.Fatalf("tool issuing seq: want 1, got %d", toolIssuingSeq)
		}

		// A second turn-start is observed (e.g. next ChatStream call) before
		// the in-flight tool from turn 1 finishes executing.
		secondSeq, err := reg.Advance(ctx, serverutil.Scope{}, "sess_late", time.Now().UTC(), "turn_2")
		if err != nil {
			t.Fatalf("advance turn 2: %v", err)
		}
		if secondSeq != 2 {
			t.Fatalf("expected turn 2 seq=2, got %d", secondSeq)
		}

		// The late-finishing tool's captured seq must still reflect its
		// issuing turn (1), never the now-current turn (2).
		if toolIssuingSeq != 1 {
			t.Fatalf("late-finishing tool must retain issuing turn seq=1, got %d", toolIssuingSeq)
		}
		if got := reg.Current(serverutil.Scope{}, "sess_late"); got != 2 {
			t.Fatalf("register current seq after turn 2 advance: want 2, got %d", got)
		}
	})
}
