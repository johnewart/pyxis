package claudecodeclillm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"time"
)

type streamJSONLine struct {
	line []byte
	err  error
}

func startStreamJSONLineReader(log cliLog, reader *bufio.Reader) <-chan streamJSONLine {
	ch := make(chan streamJSONLine, 8)
	go func() {
		defer close(ch)
		for {
			line, err := readStreamJSONLine(reader)
			// No debug gate: stdoutLine always emits init/result at INFO.
			if len(line) > 0 {
				log.stdoutLine(string(line))
			}
			ch <- streamJSONLine{line: line, err: err}
			if err != nil {
				return
			}
		}
	}()
	return ch
}

// maybeCaptureClaudeBackendSessionID records session_id from system/init when present.
// Init may arrive before or after control_response, and often only after the first user frame.
func maybeCaptureClaudeBackendSessionID(ctx context.Context, line []byte) {
	if len(line) == 0 {
		return
	}
	var obj map[string]any
	if json.Unmarshal(line, &obj) != nil {
		return
	}
	typ, _ := obj["type"].(string)
	if typ != "system" {
		return
	}
	subtype, _ := obj["subtype"].(string)
	if subtype != "init" {
		return
	}
	sid, ok := obj["session_id"].(string)
	if !ok || strings.TrimSpace(sid) == "" {
		return
	}
	if sink := backendSessionSinkFromContext(ctx); sink != nil {
		sink(strings.TrimSpace(sid))
	}
}

func trimClaudeStreamJSONLine(line []byte) []byte {
	line = bytes.TrimSpace(line)
	if len(line) >= 3 && line[0] == 0xef && line[1] == 0xbb && line[2] == 0xbf {
		line = line[3:]
	}
	return line
}

// isClaudeControlResponseLine recognizes the initialize catalog without decoding megabyte JSON.
func isClaudeControlResponseLine(line []byte) bool {
	line = trimClaudeStreamJSONLine(line)
	if len(line) < 20 {
		return false
	}
	if bytes.Contains(line, []byte(`"type":"control_response"`)) ||
		bytes.Contains(line, []byte(`"type": "control_response"`)) {
		return true
	}
	var header struct {
		Type string `json:"type"`
	}
	return json.Unmarshal(line, &header) == nil && header.Type == "control_response"
}

func claudeStreamInitFromLine(ctx context.Context, line []byte) bool {
	if len(line) == 0 {
		return false
	}
	maybeCaptureClaudeBackendSessionID(ctx, line)
	return isClaudeControlResponseLine(line)
}

func waitForClaudeStreamInit(
	ctx context.Context,
	lines <-chan streamJSONLine,
	stderrSnapshot func() string,
	procDone <-chan struct{},
) error {
	return waitForClaudeStreamInitFromSource(ctx, newStreamJSONLineSource(lines), stderrSnapshot, procDone)
}

func waitForClaudeStreamInitFromSource(
	ctx context.Context,
	src *streamJSONLineSource,
	stderrSnapshot func() string,
	procDone <-chan struct{},
) error {
	// Preserve early system/init or status frames that can arrive before control_response.
	return src.waitForLine(ctx, isClaudeControlResponseLine, stderrSnapshot, procDone, "initialize")
}

// waitForLine scans forward until a line satisfies match (returning nil) or the
// stream ends. It first walks any lines already buffered for replay, then reads
// new lines directly from the channel. Non-matching lines are kept in src.buf in
// arrival order for the downstream consumer; the matching line is consumed. Using
// a scan cursor (rather than pop-then-reappend via src.read) is what prevents the
// buffer from being re-scanned forever when a non-matching line arrives first.
func (s *streamJSONLineSource) waitForLine(
	ctx context.Context,
	match func(line []byte) bool,
	stderrSnapshot func() string,
	procDone <-chan struct{},
	waitingFor string,
) error {
	failf := func(fallback string) error {
		msg := strings.TrimSpace(stderrSnapshot())
		if msg == "" {
			msg = fallback
		}
		// A CLI that cannot authenticate dies here: it exits before emitting the
		// frame we are waiting for, leaving the reason only in stderr. Classify
		// that text so the failure surfaces as "sign in again" rather than as a
		// handshake timeout.
		return annotateClaudeAuthError(
			fmt.Errorf("claude-code-cli-v2: waiting for %s: %s", waitingFor, msg), msg)
	}
	closedMsg := fmt.Sprintf("stdout closed before %s", waitingFor)
	exitMsg := fmt.Sprintf("claude CLI process exited before %s", waitingFor)

	scanned := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-procDone:
			return failf(exitMsg)
		default:
		}

		// Walk already-buffered lines by index before pulling new ones from the channel.
		if scanned < len(s.buf) {
			sl := s.buf[scanned]
			maybeCaptureClaudeBackendSessionID(ctx, sl.line)
			if match(sl.line) {
				s.buf = append(s.buf[:scanned], s.buf[scanned+1:]...)
				return nil
			}
			scanned++
			continue
		}

		sl, err := s.readFromChannel(ctx)
		if err == io.EOF {
			return failf(closedMsg)
		}
		if err != nil {
			return failf(err.Error())
		}
		if sl.err == io.EOF {
			return failf(closedMsg)
		}
		if sl.err != nil {
			return failf(sl.err.Error())
		}
		maybeCaptureClaudeBackendSessionID(ctx, sl.line)
		if match(sl.line) {
			return nil
		}
		s.buf = append(s.buf, sl)
		scanned++
	}
}

// kickClaudeStreamJSONHandshake writes the SDK initialize control frame on stdin and waits
// for control_response on stdout. The stdout line source must already be running; stdin
// must be an open pipe (not /dev/null) or the CLI exits immediately.
func kickClaudeStreamJSONHandshake(
	ctx context.Context,
	stdin io.Writer,
	lineSrc *streamJSONLineSource,
	stderrSnapshot func() string,
	procDone <-chan struct{},
) error {
	if err := writeInitializeRequest(stdin, newStreamRequestID()); err != nil {
		return fmt.Errorf("claude-code-cli-v2: initialize stdin: %w", err)
	}
	return waitForClaudeStreamInitFromSource(ctx, lineSrc, stderrSnapshot, procDone)
}

func nextStreamJSONLine(ctx context.Context, lines <-chan streamJSONLine) ([]byte, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case sl, ok := <-lines:
		if !ok {
			return nil, io.EOF
		}
		if sl.err != nil {
			return nil, sl.err
		}
		return sl.line, nil
	}
}

func drainStreamJSONLineChannel(lines <-chan streamJSONLine) {
	for range lines {
	}
}

// streamJSONLineSource buffers stdout lines so bootstrap waits can peek ahead without losing events.
type streamJSONLineSource struct {
	ch  <-chan streamJSONLine
	buf []streamJSONLine
}

func newStreamJSONLineSource(ch <-chan streamJSONLine) *streamJSONLineSource {
	return &streamJSONLineSource{ch: ch}
}

func (s *streamJSONLineSource) read(ctx context.Context) (streamJSONLine, error) {
	if len(s.buf) > 0 {
		sl := s.buf[0]
		s.buf = s.buf[1:]
		return sl, nil
	}
	return s.readFromChannel(ctx)
}

// readFromChannel reads the next line directly from the underlying channel,
// bypassing the replay buffer. Used by waitForLine so buffered lines are scanned
// by cursor instead of popped and re-appended.
func (s *streamJSONLineSource) readFromChannel(ctx context.Context) (streamJSONLine, error) {
	select {
	case <-ctx.Done():
		return streamJSONLine{}, ctx.Err()
	case sl, ok := <-s.ch:
		if !ok {
			return streamJSONLine{}, io.EOF
		}
		return sl, nil
	}
}

func isClaudeSystemInitLine(line []byte) bool {
	line = trimClaudeStreamJSONLine(line)
	if len(line) < 20 {
		return false
	}
	if bytes.Contains(line, []byte(`"type":"system"`)) &&
		(bytes.Contains(line, []byte(`"subtype":"init"`)) || bytes.Contains(line, []byte(`"subtype": "init"`))) {
		return true
	}
	var header struct {
		Type    string `json:"type"`
		Subtype string `json:"subtype"`
	}
	return json.Unmarshal(line, &header) == nil && header.Type == "system" && header.Subtype == "init"
}

func lineSrcHasBufferedSystemInit(src *streamJSONLineSource) bool {
	for _, sl := range src.buf {
		if isClaudeSystemInitLine(sl.line) {
			return true
		}
	}
	return false
}

// waitForClaudeSystemInitLine blocks until system/init (or ctx cancel) so MCP servers can connect
// before the first user frame is sent. Non-init lines are buffered for the stream consumer.
func waitForClaudeSystemInitLine(
	ctx context.Context,
	src *streamJSONLineSource,
	stderrSnapshot func() string,
	procDone <-chan struct{},
) error {
	return src.waitForLine(ctx, isClaudeSystemInitLine, stderrSnapshot, procDone, "system/init")
}

func nextStreamJSONLineFromSource(ctx context.Context, src *streamJSONLineSource) ([]byte, error) {
	sl, err := src.read(ctx)
	if err != nil {
		return nil, err
	}
	if sl.err != nil {
		return nil, sl.err
	}
	return sl.line, nil
}

// drainStreamJSONLineSource discards buffered stdout until the reader closes or the
// CLI process exits. Blocking on context.Background() after result previously hung
// tests and real turns because the CLI stays alive while stdin remains open.
func drainStreamJSONLineSource(src *streamJSONLineSource, procDone <-chan struct{}) {
	for {
		select {
		case <-procDone:
			return
		default:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 250*time.Millisecond)
		_, err := src.read(ctx)
		cancel()
		if err != nil {
			return
		}
	}
}
