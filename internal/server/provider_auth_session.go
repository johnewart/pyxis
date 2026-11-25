package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/creack/pty"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// providerAuthSessionIdleTimeout bounds how long a started-but-abandoned auth
// session may hold a live PTY.
//
// This is the whole risk of turning a single bidi call into three independent
// ones: with bidi, the process dies when the stream drops. Here nothing ties
// the PTY's lifetime to a connection, so a browser tab closed mid-login would
// otherwise leave `claude login` running on the llmd host forever. The reaper
// below is not optional bookkeeping -- without it this design leaks processes.
const providerAuthSessionIdleTimeout = 15 * time.Minute

// providerAuthOutputCap bounds the replay buffer per session. Login flows emit
// a few KB; the cap exists so a CLI that decides to stream forever cannot grow
// llmd's heap without bound.
const providerAuthOutputCap = 256 * 1024

// providerAuthSession is one live interactive auth PTY.
type providerAuthSession struct {
	id      string
	cmd     *exec.Cmd
	ptyFile *os.File

	mu sync.Mutex
	// buf retains output for replay. Between StartProviderAuthSession returning
	// and the client attaching its output stream, the CLI has usually already
	// printed the login URL -- the single most important thing in the flow. With
	// no buffer those bytes are simply gone and the user sees a blank terminal.
	buf []byte
	// subs are attached output streams, fed by the single PTY reader goroutine.
	subs map[chan []byte]struct{}
	// done closes when the process has exited and the reader has finished.
	done     chan struct{}
	exitCode int
	exited   bool

	lastSeen time.Time
}

// providerAuthSessions is the process-wide registry of live auth PTYs.
type providerAuthSessions struct {
	mu       sync.Mutex
	sessions map[string]*providerAuthSession
	reaping  bool
}

var authSessions = &providerAuthSessions{sessions: map[string]*providerAuthSession{}}

func newProviderAuthSessionID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().UTC().String()))
	}
	return hex.EncodeToString(b[:])
}

func (r *providerAuthSessions) add(s *providerAuthSession) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sessions[s.id] = s
	if !r.reaping {
		r.reaping = true
		go r.reapLoop()
	}
}

func (r *providerAuthSessions) get(id string) (*providerAuthSession, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sessions[strings.TrimSpace(id)]
	if ok {
		s.touch()
	}
	return s, ok
}

func (r *providerAuthSessions) remove(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.sessions, id)
}

// reapLoop kills sessions nobody has touched within the idle timeout.
func (r *providerAuthSessions) reapLoop() {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for range t.C {
		now := time.Now()
		r.mu.Lock()
		var stale []*providerAuthSession
		for id, s := range r.sessions {
			s.mu.Lock()
			idle := now.Sub(s.lastSeen)
			s.mu.Unlock()
			if idle > providerAuthSessionIdleTimeout {
				stale = append(stale, s)
				delete(r.sessions, id)
			}
		}
		r.mu.Unlock()
		for _, s := range stale {
			llmdlog.Stdout(providerLogModule, llmdlog.LevelWarn,
				"auth session %s abandoned for %s; killing its CLI process",
				s.id, providerAuthSessionIdleTimeout)
			s.kill()
		}
	}
}

func (s *providerAuthSession) touch() {
	s.mu.Lock()
	s.lastSeen = time.Now()
	s.mu.Unlock()
}

// publish appends to the replay buffer and fans out to attached streams.
func (s *providerAuthSession) publish(chunk []byte) {
	s.mu.Lock()
	if len(s.buf) < providerAuthOutputCap {
		s.buf = append(s.buf, chunk...)
	}
	subs := make([]chan []byte, 0, len(s.subs))
	for c := range s.subs {
		subs = append(subs, c)
	}
	s.mu.Unlock()
	for _, c := range subs {
		// Never block the PTY reader on a slow consumer: a stalled browser must
		// not be able to wedge the subprocess.
		select {
		case c <- chunk:
		default:
		}
	}
}

func (s *providerAuthSession) subscribe() (chan []byte, []byte) {
	ch := make(chan []byte, 256)
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.subs == nil {
		s.subs = map[chan []byte]struct{}{}
	}
	s.subs[ch] = struct{}{}
	replay := append([]byte(nil), s.buf...)
	return ch, replay
}

func (s *providerAuthSession) unsubscribe(ch chan []byte) {
	s.mu.Lock()
	delete(s.subs, ch)
	s.mu.Unlock()
}

func (s *providerAuthSession) kill() {
	if s.ptyFile != nil {
		_ = s.ptyFile.Close()
	}
	if s.cmd != nil && s.cmd.Process != nil {
		_ = s.cmd.Process.Kill()
	}
}

// StartProviderAuthSession spawns an interactive auth CLI under a PTY and keeps
// it alive, addressable by the returned session id. See the proto comment for
// why this exists next to RunProviderAuthCommand.
func (s *LlmServer) StartProviderAuthSession(ctx context.Context, req *llmv1.StartProviderAuthSessionRequest) (*llmv1.StartProviderAuthSessionResponse, error) {
	spec, err := providerAuthCommandSpecFor(req.GetProviderType())
	if err != nil {
		return nil, status.Errorf(codes.InvalidArgument, "%v", err)
	}
	binary, err := resolveProviderAuthBinary(req.GetBinary(), spec.envVar, spec.defaultName)
	if err != nil {
		return nil, status.Errorf(codes.FailedPrecondition, "resolve auth command binary: %v", err)
	}

	cmd := exec.Command(binary, spec.args...)
	if cd := strings.TrimSpace(req.GetConfigDir()); cd != "" {
		cmd.Env = append(os.Environ(), "CLAUDE_CONFIG_DIR="+cd)
	}
	cols, rows := req.GetCols(), req.GetRows()
	if cols == 0 {
		cols = 80
	}
	if rows == 0 {
		rows = 24
	}
	ptyFile, err := pty.StartWithSize(cmd, &pty.Winsize{Cols: uint16(cols), Rows: uint16(rows)})
	if err != nil {
		return nil, status.Errorf(codes.Internal, "start %s in pty: %v", binary, err)
	}

	sess := &providerAuthSession{
		id:       newProviderAuthSessionID(),
		cmd:      cmd,
		ptyFile:  ptyFile,
		subs:     map[chan []byte]struct{}{},
		done:     make(chan struct{}),
		lastSeen: time.Now(),
	}
	authSessions.add(sess)
	llmdlog.Stdout(providerLogModule, llmdlog.LevelInfo,
		"auth session %s started: %s %s (config_dir=%q)",
		sess.id, binary, strings.Join(spec.args, " "), req.GetConfigDir())

	// One reader drains the PTY into the replay buffer and all subscribers,
	// regardless of whether anyone is currently attached. That is what makes
	// the login URL survive a client that attaches late.
	go func() {
		defer close(sess.done)
		buf := make([]byte, 32*1024)
		for {
			n, readErr := ptyFile.Read(buf)
			if n > 0 {
				chunk := make([]byte, n)
				copy(chunk, buf[:n])
				sess.publish(chunk)
			}
			if readErr != nil {
				break
			}
		}
		waitErr := cmd.Wait()
		code := 0
		var exitErr *exec.ExitError
		if waitErr != nil {
			if errors.As(waitErr, &exitErr) {
				code = exitErr.ExitCode()
			} else {
				code = -1
			}
		}
		sess.mu.Lock()
		sess.exitCode, sess.exited = code, true
		sess.mu.Unlock()
		llmdlog.Stdout(providerLogModule, llmdlog.LevelInfo,
			"auth session %s process exited with code %d", sess.id, code)
	}()

	return &llmv1.StartProviderAuthSessionResponse{SessionId: sess.id}, nil
}

// StreamProviderAuthOutput streams PTY output for a session until the process
// exits or the client disconnects. Server-streaming, so gRPC-Web can carry it.
func (s *LlmServer) StreamProviderAuthOutput(req *llmv1.StreamProviderAuthOutputRequest, srv llmv1.LlmService_StreamProviderAuthOutputServer) error {
	sess, ok := authSessions.get(req.GetSessionId())
	if !ok {
		return status.Errorf(codes.NotFound, "unknown auth session %q", req.GetSessionId())
	}
	ch, replay := sess.subscribe()
	defer sess.unsubscribe(ch)

	if err := srv.Send(&llmv1.ProviderAuthCommandOutput{
		Frame: &llmv1.ProviderAuthCommandOutput_Started{Started: true},
	}); err != nil {
		return err
	}
	if req.GetReplay() && len(replay) > 0 {
		if err := srv.Send(&llmv1.ProviderAuthCommandOutput{
			Frame: &llmv1.ProviderAuthCommandOutput_Output{Output: replay},
		}); err != nil {
			return err
		}
	}

	for {
		select {
		case <-srv.Context().Done():
			// The client went away. Deliberately do NOT kill the process: a
			// browser reload should be able to re-attach to a login already in
			// progress. The idle reaper is what stops that leaking.
			return nil
		case chunk := <-ch:
			if err := srv.Send(&llmv1.ProviderAuthCommandOutput{
				Frame: &llmv1.ProviderAuthCommandOutput_Output{Output: chunk},
			}); err != nil {
				return err
			}
		case <-sess.done:
			// Drain anything buffered between exit and this branch firing.
			for {
				select {
				case chunk := <-ch:
					if err := srv.Send(&llmv1.ProviderAuthCommandOutput{
						Frame: &llmv1.ProviderAuthCommandOutput_Output{Output: chunk},
					}); err != nil {
						return err
					}
					continue
				default:
				}
				break
			}
			sess.mu.Lock()
			code := sess.exitCode
			sess.mu.Unlock()
			return srv.Send(&llmv1.ProviderAuthCommandOutput{
				Frame: &llmv1.ProviderAuthCommandOutput_ExitCode{ExitCode: int32(code)},
			})
		}
	}
}

// SendProviderAuthInput writes keystrokes (or a resize) to a session's PTY.
func (s *LlmServer) SendProviderAuthInput(ctx context.Context, req *llmv1.SendProviderAuthInputRequest) (*llmv1.SendProviderAuthInputResponse, error) {
	sess, ok := authSessions.get(req.GetSessionId())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "unknown auth session %q", req.GetSessionId())
	}
	switch frame := req.GetFrame().(type) {
	case *llmv1.SendProviderAuthInputRequest_Stdin:
		if _, err := sess.ptyFile.Write(frame.Stdin); err != nil {
			return nil, status.Errorf(codes.Internal, "write to pty: %v", err)
		}
	case *llmv1.SendProviderAuthInputRequest_Resize:
		_ = pty.Setsize(sess.ptyFile, &pty.Winsize{
			Cols: uint16(frame.Resize.GetCols()),
			Rows: uint16(frame.Resize.GetRows()),
		})
	default:
		return nil, status.Error(codes.InvalidArgument, "frame must be stdin or resize")
	}
	return &llmv1.SendProviderAuthInputResponse{}, nil
}

// EndProviderAuthSession terminates a session and forgets it. Safe to call on
// an already-exited session; callers should always call it so an abandoned
// login does not have to wait out the idle reaper.
func (s *LlmServer) EndProviderAuthSession(ctx context.Context, req *llmv1.EndProviderAuthSessionRequest) (*llmv1.EndProviderAuthSessionResponse, error) {
	sess, ok := authSessions.get(req.GetSessionId())
	if !ok {
		return nil, status.Errorf(codes.NotFound, "unknown auth session %q", req.GetSessionId())
	}
	authSessions.remove(sess.id)
	sess.mu.Lock()
	exited, code := sess.exited, sess.exitCode
	sess.mu.Unlock()
	if !exited {
		llmdlog.Stdout(providerLogModule, llmdlog.LevelInfo,
			"auth session %s ended by client while still running; killing", sess.id)
		sess.kill()
	}
	return &llmv1.EndProviderAuthSessionResponse{ExitCode: int32(code), Exited: exited}, nil
}
