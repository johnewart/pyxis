package server

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/store"
	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/grpc/metadata"
)

// serverQueueOwner is the claim owner llmd uses when it drains a queue itself.
const serverQueueOwner = "llmd-server"

// queueHeartbeatInterval is how often the server drainer extends its claim
// while the queued turn runs. Well inside store.DefaultClaimLease so one
// missed beat never lets another drainer reclaim live work.
const queueHeartbeatInterval = store.DefaultClaimLease / 4

// Server-side queue draining.
//
// The queue exists so someone can line up "keep going" messages and walk away:
// nothing has to stay open for them to run. So llmd drains it itself whenever a
// session goes idle — after a turn ends, when something is enqueued on an idle
// session, when a queue is resumed, and once at host startup.
//
// The one case it must not drain is a session with client-owned tools
// (delegate_to_client): a turn llmd starts on its own has no client attached to
// execute them, so the model could call a tool nobody will ever answer. Those
// sessions are left to a client drainer, which the claim/lease contract in
// store.SessionQueueRepo already makes safe to run alongside this one.
//
// A server-started turn runs through ChatStream exactly like a client turn, with
// a stream that has no client on the other end. It gets the session's defaults
// (bound model, persisted system prompt sections) — not anything a client would
// have sent per request, such as tracked documents.

type queueDrainKey struct {
	scope serverutil.Scope
	sid   string
}

// queueDrainer guarantees at most one drain loop per session. A kick that
// arrives while a loop runs is folded into it (the loop re-checks before
// exiting), so kicks are cheap and can be issued liberally.
type queueDrainer struct {
	mu     sync.Mutex
	active map[queueDrainKey]bool
	again  map[queueDrainKey]bool
	wg     sync.WaitGroup
}

func newQueueDrainer() *queueDrainer {
	return &queueDrainer{active: map[queueDrainKey]bool{}, again: map[queueDrainKey]bool{}}
}

func (s *LlmServer) queueDrainerInstance() *queueDrainer {
	s.queueDrainOnce.Do(func() { s.queueDrain = newQueueDrainer() })
	return s.queueDrain
}

// kickQueueDrain asks for sid's queue to be drained if the server is allowed
// to. Non-blocking; safe to call from any RPC path.
func (s *LlmServer) kickQueueDrain(sc serverutil.Scope, sessionID string) {
	if s == nil || s.SessionQueue == nil || s.DisableServerQueueDrain {
		return
	}
	sid := strings.TrimSpace(sessionID)
	if sid == "" || sc.TenantID == "" {
		return
	}
	d := s.queueDrainerInstance()
	k := queueDrainKey{scope: sc, sid: sid}
	d.mu.Lock()
	if d.active[k] {
		d.again[k] = true
		d.mu.Unlock()
		return
	}
	d.active[k] = true
	d.wg.Add(1)
	d.mu.Unlock()

	go func() {
		defer d.wg.Done()
		for {
			for s.drainQueueStep(sc, sid) {
			}
			d.mu.Lock()
			if d.again[k] {
				d.again[k] = false
				d.mu.Unlock()
				continue
			}
			delete(d.active, k)
			d.mu.Unlock()
			return
		}
	}()
}

// WaitForQueueDrains blocks until every running drain loop has exited, or
// timeout. Test and shutdown helper; reports whether all loops finished.
func (s *LlmServer) WaitForQueueDrains(timeout time.Duration) bool {
	d := s.queueDrainerInstance()
	done := make(chan struct{})
	go func() {
		d.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}

// SweepQueuesOnStartup deals with every session that still has queued work
// from before a restart. llmdhost.New calls it once.
//
// Leftover queues are handled by age: if the newest entry is younger than
// resumeWindow the queue drains as usual; otherwise it is PAUSED, never
// dropped, so a stale "keep going" doesn't fire days later on context that has
// moved on. The user resumes it from the queue UI when they want it. A
// negative resumeWindow pauses every leftover queue.
func (s *LlmServer) SweepQueuesOnStartup(ctx context.Context, resumeWindow time.Duration) {
	if s == nil || s.SessionQueue == nil || s.DisableServerQueueDrain {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	sessions, err := s.SessionQueue.SessionsWithQueuedWork(ctx)
	if err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn, "queue drain: startup sweep failed: %v", err)
		return
	}
	now := time.Now()
	for _, q := range sessions {
		rows, lerr := s.SessionQueue.List(ctx, q.Scope, q.SessionID)
		if lerr != nil {
			llmdlog.Stdout("server", llmdlog.LevelWarn,
				"queue drain: startup sweep list failed: %s session=%q: %v", q.Scope.String(), q.SessionID, lerr)
			continue
		}
		var newest time.Time
		for _, r := range rows {
			if r.CreatedAt.After(newest) {
				newest = r.CreatedAt
			}
		}
		if resumeWindow < 0 || now.Sub(newest) > resumeWindow {
			if perr := s.SessionQueue.SetPaused(ctx, q.Scope, q.SessionID, true); perr != nil {
				llmdlog.Stdout("server", llmdlog.LevelWarn,
					"queue drain: startup pause failed: %s session=%q: %v", q.Scope.String(), q.SessionID, perr)
				continue
			}
			llmdlog.Stdout("server", llmdlog.LevelInfo,
				"queue drain: paused stale leftover queue (%d entries, newest %s ago): %s session=%q",
				len(rows), now.Sub(newest).Round(time.Minute), q.Scope.String(), q.SessionID)
			s.announceQueueChanged(q.Scope.IncomingContext(ctx), q.Scope, q.SessionID)
			continue
		}
		llmdlog.Stdout("server", llmdlog.LevelInfo,
			"queue drain: resuming leftover queue (%d entries): %s session=%q", len(rows), q.Scope.String(), q.SessionID)
		s.kickQueueDrain(q.Scope, q.SessionID)
	}
}

// drainQueueStep runs at most one queued entry. Reports whether it did work
// (so the caller should look again); false means "nothing to do right now".
func (s *LlmServer) drainQueueStep(sc serverutil.Scope, sid string) bool {
	base := s.BaseContext
	if base == nil {
		base = context.Background()
	}
	if base.Err() != nil {
		return false
	}
	ctx := sc.IncomingContext(base)

	// A running turn will kick us again when it ends; claiming now would just
	// park the entry behind it.
	if s.turnRunner().running(sid) {
		return false
	}
	if err := s.ensureKnownSession(ctx, sid); err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn, "queue drain: %s session=%q not drainable: %v", sc.String(), sid, err)
		return false
	}
	if tool := s.firstClientOwnedTool(ctx, sid); tool != "" {
		llmdlog.Stdout("server", llmdlog.LevelInfo,
			"queue drain: %s session=%q has client-owned tool %q; leaving queue to a client drainer", sc.String(), sid, tool)
		return false
	}

	entry, err := s.SessionQueue.ClaimNext(ctx, sc, sid, serverQueueOwner)
	if errors.Is(err, store.ErrQueuePaused) {
		return false
	}
	if err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn, "queue drain: claim failed: %s session=%q: %v", sc.String(), sid, err)
		return false
	}
	if entry == nil {
		return false
	}
	s.announceQueueChanged(ctx, sc, sid)
	llmdlog.Stdout("server", llmdlog.LevelInfo, "queue drain: running entry=%q %s session=%q", entry.ID, sc.String(), sid)

	started, turnErr := s.runQueuedEntry(ctx, sc, sid, entry)

	if !started {
		// Never reached the model: hand it back at its original position. Don't
		// report work done, so a failing session does not spin; the next kick
		// (next turn, next enqueue, next restart) retries it.
		if rerr := s.SessionQueue.Release(ctx, sc, sid, entry.ID, serverQueueOwner); rerr != nil {
			llmdlog.Stdout("server", llmdlog.LevelWarn,
				"queue drain: release after failed start failed: entry=%q %s session=%q: %v", entry.ID, sc.String(), sid, rerr)
		}
		llmdlog.Stdout("server", llmdlog.LevelWarn,
			"queue drain: entry=%q never started a turn; released: %s session=%q: %v", entry.ID, sc.String(), sid, turnErr)
		s.announceQueueChanged(ctx, sc, sid)
		return false
	}
	if turnErr != nil {
		// The message was delivered (the turn exists in history, partial or not),
		// so it is retired rather than re-run — re-running would duplicate it.
		llmdlog.Stdout("server", llmdlog.LevelWarn,
			"queue drain: entry=%q turn ended with error (retiring anyway): %s session=%q: %v", entry.ID, sc.String(), sid, turnErr)
	}
	if rerr := s.SessionQueue.Retire(ctx, sc, sid, entry.ID, serverQueueOwner); rerr != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn,
			"queue drain: retire failed: entry=%q %s session=%q: %v", entry.ID, sc.String(), sid, rerr)
	}
	s.announceQueueChanged(ctx, sc, sid)
	s.trimEventsWhenIdle(ctx, sc, sid)
	return true
}

// trimEventsWhenIdle drops every event but the newest once a drained entry's
// turn is over and its retire announcement is logged. It holds the session's
// turn gate so it cannot delete frames of a turn that is just starting; when
// the gate is taken, that turn trims the log itself when it ends.
func (s *LlmServer) trimEventsWhenIdle(ctx context.Context, sc serverutil.Scope, sid string) {
	if s.SessionEvents == nil {
		return
	}
	release, ok := s.sessionTurnGate().tryAcquire(sid)
	if !ok {
		return
	}
	defer release()
	if _, err := s.SessionEvents.TrimToNewest(ctx, sc, sid); err != nil {
		llmdlog.Stdout("server", llmdlog.LevelWarn, "queue drain: trim events failed: %s session=%q: %v", sc.String(), sid, err)
	}
}

// runQueuedEntry sends entry as a user turn and holds the claim while it runs.
// started reports whether the turn reached the model (AssistantTurnStarted).
func (s *LlmServer) runQueuedEntry(ctx context.Context, sc serverutil.Scope, sid string, entry *store.SessionQueuedMessageRow) (started bool, err error) {
	var (
		mu         sync.Mutex
		didStart   bool
		markedTurn string
	)
	stream := &serverDrainStream{ctx: ctx, onFrame: func(f *llmv1.ConversationStreamFrame) {
		ts := f.GetAssistantTurnStarted()
		if ts == nil {
			return
		}
		mu.Lock()
		first := !didStart
		didStart = true
		mu.Unlock()
		if !first || strings.TrimSpace(ts.GetTurnId()) == "" {
			return
		}
		// Record delivery before the turn finishes: if llmd dies mid-turn, the
		// next claimer retires this entry instead of sending it twice.
		if merr := s.SessionQueue.MarkStarted(ctx, sc, sid, entry.ID, serverQueueOwner, ts.GetTurnId()); merr != nil {
			llmdlog.Stdout("server", llmdlog.LevelWarn,
				"queue drain: mark started failed: entry=%q turn=%q %s session=%q: %v", entry.ID, ts.GetTurnId(), sc.String(), sid, merr)
			return
		}
		mu.Lock()
		markedTurn = ts.GetTurnId()
		mu.Unlock()
	}}

	stopHB := make(chan struct{})
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		t := time.NewTicker(queueHeartbeatInterval)
		defer t.Stop()
		for {
			select {
			case <-stopHB:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				if herr := s.SessionQueue.Heartbeat(ctx, sc, sid, entry.ID, serverQueueOwner); herr != nil {
					llmdlog.Stdout("server", llmdlog.LevelWarn,
						"queue drain: heartbeat failed: entry=%q %s session=%q: %v", entry.ID, sc.String(), sid, herr)
				}
			}
		}
	}()

	atts, aerr := entry.Attachments()
	if aerr != nil {
		// Run the message anyway: dropping the user's text because its
		// attachments are corrupt would lose more than it saves.
		llmdlog.Stdout("server", llmdlog.LevelWarn,
			"queue drain: entry=%q attachments undecodable, sending without them: %s session=%q: %v", entry.ID, sc.String(), sid, aerr)
	}
	req := &llmv1.ChatStreamRequest{
		SessionId: &sid,
		Messages: []*llmv1.Message{{
			Role: "user", Content: entry.Content, Id: "queued_" + entry.ID,
			Attachments: queuedAttachmentsToProto(atts),
		}},
	}
	if entry.TurnOptions().DisableTools {
		req.TurnOptions = &llmv1.TurnOptions{DisableTools: true}
	}
	err = s.ChatStream(req, stream)
	close(stopHB)
	<-hbDone

	mu.Lock()
	defer mu.Unlock()
	if didStart && markedTurn == "" {
		llmdlog.Stdout("server", llmdlog.LevelDebug, "queue drain: entry=%q started without a recorded turn id", entry.ID)
	}
	return didStart, err
}

// firstClientOwnedTool returns the name of a delegate_to_client tool the model
// would see on sid, or "" when there is none.
func (s *LlmServer) firstClientOwnedTool(ctx context.Context, sid string) string {
	for _, td := range s.mergedToolDefinitions(ctx, sid) {
		if td.GetDelegateToClient() != nil {
			return td.GetName()
		}
	}
	return ""
}

// serverDrainStream is the ChatStream "client" for a server-started turn:
// nobody is listening, so frames are only observed (for delivery bookkeeping)
// and dropped. Persistence and hub fan-out happen inside ChatStream regardless,
// so open windows still see the turn live.
type serverDrainStream struct {
	ctx     context.Context
	onFrame func(*llmv1.ConversationStreamFrame)
}

func (d *serverDrainStream) Send(f *llmv1.ConversationStreamFrame) error {
	if d.onFrame != nil && f != nil {
		d.onFrame(f)
	}
	return nil
}
func (d *serverDrainStream) Context() context.Context     { return d.ctx }
func (d *serverDrainStream) SetHeader(metadata.MD) error  { return nil }
func (d *serverDrainStream) SendHeader(metadata.MD) error { return nil }
func (d *serverDrainStream) SetTrailer(metadata.MD)       {}
func (d *serverDrainStream) SendMsg(m any) error {
	if f, ok := m.(*llmv1.ConversationStreamFrame); ok {
		return d.Send(f)
	}
	return nil
}
func (d *serverDrainStream) RecvMsg(any) error { return nil }
