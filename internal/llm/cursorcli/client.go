package cursorclillm

// Implementation aligned with go-agents/cursor-cli-agent.ts (invokeCursorAgent flags,
// stream-json event shapes, and listCursorModels parsing — see scan.go in llmproviders).
// Prompts are written to stdin (not argv) to avoid OS ARG_MAX / exec EINVAL on large messages.

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/agentpid"
	"github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"github.com/johnewart/pyxis/cursorws"
	"github.com/johnewart/pyxis/internal/llm/common"
	"github.com/johnewart/pyxis/internal/llm/common/assistanttext"
	"github.com/johnewart/pyxis/internal/llm/common/memctx"
	"github.com/johnewart/pyxis/internal/llm/common/suffixwire"
	"github.com/johnewart/pyxis/internal/llm/common/toolcapture"
	"github.com/johnewart/pyxis/llmdlog"
	"github.com/johnewart/pyxis/systemprompt"
	"google.golang.org/protobuf/proto"
)

// Config holds Cursor Agent CLI options (YAML-backed in llmproviders).
type Config struct {
	Binary        string
	Cwd           string
	WorkspaceFlag string // passed as --workspace when non-empty
	APIKey        string
	DefaultModel  string
	Mode          string // ask | plan | empty (empty omits --mode)
	Trust         bool
	ApproveMcps   bool
	Force         bool
	SessionID     string
	DefaultMaxTok int
}

// Client implements backend.Backend by shelling out to the Cursor Agent CLI.
type Client struct {
	cfg Config
}

// New builds a client. When cfg.Binary is empty, CURSOR_AGENT_PATH then "agent" is used.
func New(cfg Config) *Client {
	if strings.TrimSpace(cfg.Binary) == "" {
		cfg.Binary = strings.TrimSpace(os.Getenv("CURSOR_AGENT_PATH"))
	}
	if strings.TrimSpace(cfg.Binary) == "" {
		cfg.Binary = "agent"
	}
	if strings.TrimSpace(cfg.DefaultModel) == "" {
		cfg.DefaultModel = "auto"
	}
	if cfg.DefaultMaxTok <= 0 {
		cfg.DefaultMaxTok = common.DefaultMaxTokens
	}
	return &Client{cfg: cfg}
}

func (c *Client) resolvedBinary() (string, error) {
	b := c.cfg.Binary
	if filepath.Base(b) == b {
		path, err := exec.LookPath(b)
		if err != nil {
			return "", fmt.Errorf("cursor-cli: look up %q: %w", b, err)
		}
		return path, nil
	}
	return filepath.Abs(b)
}

const debugMaxPromptRunes = 200

func debugLogCursorCLIExec(bin, cwd string, args []string, prompt string) {
	if !llmdlog.DebugEnabled() {
		return
	}
	var quoted []string
	quoted = append(quoted, strconv.Quote(bin))
	for _, a := range args {
		quoted = append(quoted, strconv.Quote(a))
	}
	line := strings.Join(quoted, " ")
	if p := strings.TrimSpace(prompt); p != "" {
		r := []rune(p)
		if len(r) > debugMaxPromptRunes {
			line += " stdin=" + strconv.Quote(string(r[:debugMaxPromptRunes])) + fmt.Sprintf("…(%d runes total)", len(r))
		} else {
			line += " stdin=" + strconv.Quote(p)
		}
	}
	if t := strings.TrimSpace(cwd); t != "" {
		llmdlog.Stdout("cursor-cli", llmdlog.LevelDebug, "cursor-cli exec: cwd=%s %s", t, line)
		return
	}
	llmdlog.Stdout("cursor-cli", llmdlog.LevelDebug, "cursor-cli exec: %s", line)
}

func mdcSourceLabel(renderedFileCount int) string {
	if renderedFileCount > 0 {
		return "structured"
	}
	return "default"
}

// prepareInvocationWorkspace picks a Cursor --workspace directory and writes .cursor/mcp.json when MCP HTTP is configured.
// When cursor_cli.workspace is set, that path is used as-is (and written into). When it is empty, each run gets
// its own directory (see defaultCursorWorkspace) so sessions can never overwrite each other's rules.
// The returned cleanup must run after the CLI process has exited.
func (c *Client) prepareInvocationWorkspace(ctx context.Context) (ws string, cleanup func(), err error) {
	cleanup = func() {}
	configured := strings.TrimSpace(c.cfg.WorkspaceFlag)
	if configured != "" {
		ws, err = filepath.Abs(configured)
		if err != nil {
			return "", cleanup, err
		}
	} else {
		ws, cleanup, err = defaultCursorWorkspace(ctx)
		if err != nil {
			return "", func() {}, err
		}
	}
	base := cursorws.Default().ResolveHTTPBaseURL()
	bin, exErr := os.Executable()
	if exErr != nil {
		cleanup()
		return "", func() {}, exErr
	}
	mdcFiles := systemprompt.RenderedMDCFilesFromContext(ctx)
	if len(mdcFiles) == 0 {
		mdcFiles = map[string]string{
			systemprompt.WorkspaceMDC: systemprompt.DefaultCursorWorkspaceMDCBody(),
		}
	}
	if cursorws.Default().DiagEnabled() {
		mdcNames := make([]string, 0, len(mdcFiles))
		for name := range mdcFiles {
			mdcNames = append(mdcNames, name)
		}
		llmdlog.Stdout("cursor-cli", llmdlog.LevelDebug,
			"prepareInvocationWorkspace workspace=%q configured_path=%t mdc_source=%s mdc_files=%v mcp_base_url=%q",
			ws,
			configured != "",
			mdcSourceLabel(len(mdcFiles)),
			mdcNames,
			base,
		)
	}
	if base != "" {
		if e := cursorws.Default().Ensure(ws, bin, base, mdcFiles); e != nil {
			cleanup()
			return "", func() {}, fmt.Errorf("cursor-cli: write .cursor/mcp.json: %w", e)
		}
	} else if configured == "" {
		llmdlog.Stdout("cursor-cli", llmdlog.LevelWarn, "cursor-cli: no MCP HTTP base URL (set LLMD_MCP_HTTP_ADDR); workspace %q has no mcp.json llmd bridge", ws)
	}
	return ws, cleanup, nil
}

func (c *Client) baseArgs(ctx context.Context, opt *llmv1.CallOptions, outputFormat string, streamPartial bool, workspace string) ([]string, error) {
	if outputFormat != "json" && outputFormat != "stream-json" {
		return nil, fmt.Errorf("cursor-cli: invalid output format %q", outputFormat)
	}
	args := []string{"--print"}
	if c.cfg.Trust {
		args = append(args, "--trust")
	}
	if c.cfg.ApproveMcps {
		args = append(args, "--approve-mcps")
	}
	if c.cfg.Force {
		args = append(args, "--force")
	}
	if outputFormat == "stream-json" {
		if streamPartial {
			args = append(args, "--stream-partial-output")
		}
		args = append(args, "--output-format", "stream-json")
	} else {
		args = append(args, "--output-format", outputFormat)
	}
	if ws := strings.TrimSpace(workspace); ws != "" {
		args = append(args, "--workspace", ws)
	}
	model := common.PickModel(opt, c.cfg.DefaultModel)
	if strings.TrimSpace(model) != "" {
		args = append(args, "--model", model)
	}
	sid := ResumeSessionIDFromContext(ctx)
	if sid == "" {
		sid = strings.TrimSpace(c.cfg.SessionID)
	}
	if sid != "" {
		args = append(args, "--resume", sid)
	}
	if opt != nil && opt.GetReadOnly() {
		args = append(args, "--mode", "ask")
	} else if m := strings.ToLower(strings.TrimSpace(c.cfg.Mode)); m == "ask" || m == "plan" {
		args = append(args, "--mode", m)
	}
	return args, nil
}

func (c *Client) cmd(ctx context.Context, prompt string, args ...string) (*exec.Cmd, error) {
	bin, err := c.resolvedBinary()
	if err != nil {
		return nil, err
	}
	debugLogCursorCLIExec(bin, c.cfg.Cwd, args, prompt)
	// Use exec.Command (not CommandContext): stdlib cancel only signals the direct child PID.
	// Cancellation is handled cooperatively via ctx + killCursorCLIProcessTree (process group on Unix).
	command := exec.Command(bin, args...)
	if p := strings.TrimSpace(prompt); p != "" {
		command.Stdin = strings.NewReader(p)
	}
	if cd := strings.TrimSpace(c.cfg.Cwd); cd != "" {
		command.Dir = cd
	}
	command.Env = os.Environ()
	if c.cfg.APIKey != "" {
		command.Env = append(command.Env, "CURSOR_API_KEY="+c.cfg.APIKey)
	}
	setCursorCLIProcessGroup(command)
	return command, nil
}

func messagesToPrompt(messages []*llmv1.Message) (string, error) {
	var b strings.Builder
	for _, m := range messages {
		if m == nil {
			continue
		}
		role := strings.TrimSpace(m.GetRole())
		if role == "" {
			continue
		}
		content := strings.TrimSpace(m.GetContent())
		if content == "" {
			continue
		}
		fmt.Fprintf(&b, "### %s\n%s\n\n", strings.ToUpper(role), content)
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "", fmt.Errorf("cursor-cli: no non-empty message content")
	}
	return out, nil
}

type jsonResult struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	IsError   bool   `json:"is_error"`
	Result    string `json:"result"`
	RequestID string `json:"request_id"`
	SessionID string `json:"session_id"`
	Usage     struct {
		InputTokens      int64 `json:"inputTokens"`
		OutputTokens     int64 `json:"outputTokens"`
		CacheReadTokens  int64 `json:"cacheReadTokens"`
		CacheWriteTokens int64 `json:"cacheWriteTokens"`
	} `json:"usage"`
}

func parseJSONResult(stdout []byte, sessionSink func(string)) (*llmv1.AssistantReply, error) {
	lines := bytes.Split(bytes.TrimSpace(stdout), []byte("\n"))
	var last *jsonResult
	for i := len(lines) - 1; i >= 0; i-- {
		line := bytes.TrimSpace(lines[i])
		if len(line) == 0 {
			continue
		}
		var r jsonResult
		if err := json.Unmarshal(line, &r); err != nil {
			continue
		}
		if r.Type == "result" {
			last = &r
			break
		}
	}
	if last == nil {
		return nil, fmt.Errorf("cursor-cli: no result object in CLI output")
	}
	if last.IsError || (last.Subtype != "" && last.Subtype != "success") {
		return nil, fmt.Errorf("cursor-cli: %s", strings.TrimSpace(last.Result))
	}
	text := strings.TrimSpace(last.Result)
	content, thinking := assistanttext.ProcessUnary(text, "", nil)
	id := strings.TrimSpace(last.RequestID)
	if id == "" {
		id = strings.TrimSpace(last.SessionID)
	}
	if id == "" {
		id = "cursor-cli"
	}
	if sessionSink != nil {
		if sid := strings.TrimSpace(last.SessionID); sid != "" {
			sessionSink(sid)
		}
	}
	return &llmv1.AssistantReply{
		Content:         content,
		ThinkingContent: thinking,
		Id:              id,
		FinishReason:    "stop",
	}, nil
}

// Chat runs a single non-streaming CLI invocation.
func (c *Client) Chat(ctx context.Context, messages []*llmv1.Message, opt *llmv1.CallOptions, _ []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	prompt, err := messagesToPrompt(messages)
	if err != nil {
		return nil, err
	}
	ws, cleanup, err := c.prepareInvocationWorkspace(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	args, err := c.baseArgs(ctx, opt, "json", false, ws)
	if err != nil {
		return nil, err
	}
	cmd, err := c.cmd(ctx, prompt, args...)
	if err != nil {
		return nil, err
	}
	if cmd.Dir == "" {
		cmd.Dir = ws // never inherit llmd's own cwd
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	c.registerAgentPID(ctx, cmd)
	waitErr := make(chan error, 1)
	go func() { waitErr <- cmd.Wait() }()
	select {
	case <-ctx.Done():
		killCursorCLIProcessTree(cmd)
		<-waitErr
		return nil, ctx.Err()
	case err := <-waitErr:
		if err != nil {
			return nil, fmt.Errorf("cursor-cli: %w: %s", err, strings.TrimSpace(stderr.String()))
		}
	}
	return parseJSONResult(stdout.Bytes(), backendSessionSinkFromContext(ctx))
}

func (c *Client) registerAgentPID(ctx context.Context, cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	reg := agentpid.DefaultRegistry()
	if reg == nil {
		return
	}
	run, ok := agentpid.FromContext(ctx)
	if !ok {
		return
	}
	reg.Register(cmd.Process.Pid, run)
}

// Generate maps system + user into one prompt and runs the CLI.
func (c *Client) Generate(ctx context.Context, systemPrompt, prompt string, opt *llmv1.CallOptions, _ []*llmv1.ToolDefinition) (*llmv1.AssistantReply, error) {
	if strings.TrimSpace(prompt) == "" {
		return nil, fmt.Errorf("cursor-cli: prompt must be non-empty")
	}
	var msgs []*llmv1.Message
	if t := strings.TrimSpace(systemPrompt); t != "" {
		msgs = append(msgs, &llmv1.Message{Role: "system", Content: t})
	}
	msgs = append(msgs, &llmv1.Message{Role: "user", Content: prompt})
	return c.Chat(ctx, msgs, opt, nil)
}

func thinkingDeltaFromMap(obj map[string]any) string {
	if s, ok := obj["delta"].(string); ok && s != "" {
		return s
	}
	if s, ok := obj["text"].(string); ok && s != "" {
		return s
	}
	if s, ok := obj["content"].(string); ok && s != "" {
		return s
	}
	return ""
}

func textDeltaFromMap(obj map[string]any) string {
	if s, ok := obj["delta"].(string); ok && s != "" {
		return s
	}
	if s, ok := obj["text"].(string); ok && s != "" {
		return s
	}
	if s, ok := obj["content"].(string); ok && s != "" {
		return s
	}
	return ""
}

func extractAssistantPlainText(message any) string {
	if message == nil {
		return ""
	}
	if s, ok := message.(string); ok {
		var v any
		if json.Unmarshal([]byte(s), &v) == nil {
			return extractAssistantPlainText(v)
		}
		return ""
	}
	m, ok := message.(map[string]any)
	if !ok {
		return ""
	}
	c := m["content"]
	if s, ok := c.(string); ok {
		return s
	}
	arr, ok := c.([]any)
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, part := range arr {
		pm, ok := part.(map[string]any)
		if !ok {
			continue
		}
		t, _ := pm["type"].(string)
		isText := t == "text" || t == "output_text" || t == "input_text"
		if !isText {
			continue
		}
		txt, _ := pm["text"].(string)
		b.WriteString(txt)
	}
	return b.String()
}

func parseCursorToolCallEnvelope(toolCall any) (toolName string, payload map[string]any, ok bool) {
	m, ok := toolCall.(map[string]any)
	if !ok || len(m) == 0 {
		return "", nil, false
	}
	for k, v := range m {
		toolName = k
		inner, ok := v.(map[string]any)
		if ok {
			return toolName, inner, true
		}
		return toolName, map[string]any{}, true
	}
	return "", nil, false
}

// ChatStream consumes stream-json output (cursor-cli-agent.ts onStreamEvent shapes).
func (c *Client) ChatStream(ctx context.Context, messages []*llmv1.Message, opt *llmv1.CallOptions, _ func() []*llmv1.ToolDefinition, emit func(*llmv1.ConversationStreamFrame) error) error {
	if emit == nil {
		return fmt.Errorf("cursor-cli: emit callback is required")
	}
	prompt, err := messagesToPrompt(messages)
	if err != nil {
		return err
	}
	ws, cleanup, err := c.prepareInvocationWorkspace(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	args, err := c.baseArgs(ctx, opt, "stream-json", true, ws)
	if err != nil {
		return err
	}
	cmd, err := c.cmd(ctx, prompt, args...)
	if err != nil {
		return err
	}
	if cmd.Dir == "" {
		cmd.Dir = ws // never inherit llmd's own cwd
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	// Included in stream errors: without it a failed turn is undiagnosable from logs.
	var stderr stderrBuffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("cursor-cli: start: %w", err)
	}
	defer func() {
		if cmd.Process == nil {
			return
		}
		_ = cmd.Wait()
	}()
	c.registerAgentPID(ctx, cmd)

	kill := func() { killCursorCLIProcessTree(cmd) }

	// Cancel the Cursor CLI as soon as the gRPC / caller context is done, even while
	// readStreamJSONLine is blocked waiting for stdout (ctx is only polled between lines otherwise).
	ctxStop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			kill()
		case <-ctxStop:
		}
	}()
	defer close(ctxStop)

	turnID, err := common.RandomTurnID()
	if err != nil {
		kill()
		return err
	}
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantTurnStarted{
			AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{
				TurnId:             turnID,
				ThinkingEnabled:    proto.Bool(true),
				StreamingToolCalls: proto.Bool(true),
			},
		},
	}); err != nil {
		kill()
		return err
	}

	var (
		visTextRaw      string
		visAsstRaw      string
		visFedRaw       string
		fullThinking    strings.Builder
		visEmitted      string
		thinkEmitted    string
		visHalted       bool
		thinkHalted     bool
		sawToolActivity bool
		visPipe         assistanttext.VisiblePipeline
		reasonPipe      assistanttext.ReasoningPipeline
		streamErr       error
		jr              jsonResult
		sawResult       bool
		finishReason    = "stop"
	)
	memHook := memctx.AssistantMemoryHook(ctx)

	emitTextDelta := func(d string) {
		if d == "" || streamErr != nil || visHalted {
			return
		}
		ne, wire, divergence, regression := suffixwire.Compute(visEmitted, d)
		if regression {
			llmdlog.Stdout("cursor-cli", llmdlog.LevelWarn, "cursor-cli: visible stream regression (keeping wire transcript); emitted=%s chunk=%s",
				suffixwire.Fingerprint(visEmitted), suffixwire.Fingerprint(d))
		}
		if divergence {
			llmdlog.Stdout("cursor-cli", llmdlog.LevelWarn, "cursor-cli: visible stream divergence (halting visible deltas for remainder of turn); emitted=%s chunk=%s",
				suffixwire.Fingerprint(visEmitted), suffixwire.Fingerprint(d))
			visHalted = true
			return
		}
		if wire == "" {
			visEmitted = ne
			return
		}
		streamErr = emit(&llmv1.ConversationStreamFrame{
			SchemaVersion: conversationevents.SchemaVersion,
			Frame: &llmv1.ConversationStreamFrame_AssistantTextDelta{
				AssistantTextDelta: &llmv1.EvAssistantTextDelta{TurnId: turnID, Delta: wire},
			},
		})
		if streamErr == nil {
			visEmitted = ne
		}
	}
	emitThinkingDelta := func(d string) {
		if d == "" || streamErr != nil || thinkHalted {
			return
		}
		ne, wire, divergence, regression := suffixwire.Compute(thinkEmitted, d)
		if regression {
			llmdlog.Stdout("cursor-cli", llmdlog.LevelWarn, "cursor-cli: thinking stream regression (keeping wire transcript); emitted=%s chunk=%s",
				suffixwire.Fingerprint(thinkEmitted), suffixwire.Fingerprint(d))
		}
		if divergence {
			llmdlog.Stdout("cursor-cli", llmdlog.LevelWarn, "cursor-cli: thinking stream divergence (halting thinking deltas for remainder of turn); emitted=%s chunk=%s",
				suffixwire.Fingerprint(thinkEmitted), suffixwire.Fingerprint(d))
			thinkHalted = true
			return
		}
		if wire == "" {
			thinkEmitted = ne
			return
		}
		streamErr = emit(&llmv1.ConversationStreamFrame{
			SchemaVersion: conversationevents.SchemaVersion,
			Frame: &llmv1.ConversationStreamFrame_AssistantThinkingDelta{
				AssistantThinkingDelta: &llmv1.EvAssistantThinkingDelta{TurnId: turnID, Delta: wire},
			},
		})
		if streamErr == nil {
			thinkEmitted = ne
		}
	}
	emitThinkFromAssistant := emitThinkingDelta
	// Native `type:thinking` stream-json events bypass VisiblePipeline; run the same memory + <activity>
	// stripping as other backends so activity snapshots fire during the thinking phase.
	emitThinkingPostPipeline := func(delta string) {
		if delta == "" {
			return
		}
		if streamErr != nil || thinkHalted {
			return
		}
		fullThinking.WriteString(delta)
		emitThinkingDelta(delta)
	}
	emitActivitySnap := func(headline string) {
		h := strings.TrimSpace(headline)
		if h == "" || streamErr != nil {
			return
		}
		streamErr = emit(&llmv1.ConversationStreamFrame{
			SchemaVersion: conversationevents.SchemaVersion,
			Frame: &llmv1.ConversationStreamFrame_AssistantActivitySnapshot{
				AssistantActivitySnapshot: &llmv1.EvAssistantActivitySnapshot{TurnId: turnID, Headline: h},
			},
		})
	}

	// reconcileVisible merges the two visible reconstructions (type:text concat + latest
	// type:assistant snapshot) and feeds only the not-yet-fed raw suffix into the pipeline,
	// so each raw byte reaches the tag-stripping splitter exactly once regardless of how the
	// two sources interleave.
	reconcileVisible := func() {
		suffix, newFed := reconcileVisibleSuffix(visFedRaw, canonicalVisible(visTextRaw, visAsstRaw))
		if suffix == "" {
			return
		}
		visFedRaw = newFed
		visPipe.Feed(suffix, memHook, emitActivitySnap, emitTextDelta, emitThinkFromAssistant)
	}

	reader := bufio.NewReader(stdout)

lines:
	for {
		if err := ctx.Err(); err != nil {
			kill()
			return err
		}
		if streamErr != nil {
			kill()
			return streamErr
		}
		line, err := readStreamJSONLine(reader)
		if err == io.EOF {
			break
		}
		if err != nil {
			kill()
			return fmt.Errorf("cursor-cli stream: %w%s", err, stderr.suffix())
		}
		if len(line) == 0 {
			continue
		}
		var obj map[string]any
		if err := json.Unmarshal(line, &obj); err != nil {
			continue
		}
		typ, _ := obj["type"].(string)
		switch typ {
		case "thinking":
			d := thinkingDeltaFromMap(obj)
			if d != "" {
				reasonPipe.Feed(d, memHook, emitActivitySnap, emitThinkingPostPipeline)
			}
		case "text":
			// Incremental delta: extend the type:text reconstruction, then reconcile against
			// the assistant snapshot so an increment already absorbed by a snapshot cannot
			// double-emit.
			d := textDeltaFromMap(obj)
			if d != "" {
				visTextRaw += d
				reconcileVisible()
			}
		case "assistant":
			// Cumulative snapshot: record it as the assistant reconstruction and reconcile.
			// Both type:text and type:assistant describe the same message with unreliable
			// ordering and minor formatting drift; reconcileVisible merges them into one
			// monotonic raw stream instead of delta-stripping each source independently.
			msg := obj["message"]
			if msg == nil {
				msg = obj["delta"]
			}
			full := extractAssistantPlainText(msg)
			if full == "" {
				continue
			}
			visAsstRaw = full
			reconcileVisible()
		case "tool_call":
			callID, _ := obj["call_id"].(string)
			callID = strings.TrimSpace(callID)
			if callID == "" {
				continue
			}
			sawToolActivity = true
			subtype, _ := obj["subtype"].(string)
			name, payload, ok := parseCursorToolCallEnvelope(obj["tool_call"])
			if !ok {
				continue
			}
			switch subtype {
			case "started":
				started := &llmv1.EvToolCallStreamStarted{TurnId: turnID, Id: callID}
				if name != "" {
					started.Name = proto.String(name)
				}
				if err := emit(&llmv1.ConversationStreamFrame{
					SchemaVersion: conversationevents.SchemaVersion,
					Frame:         &llmv1.ConversationStreamFrame_ToolCallStreamStarted{ToolCallStreamStarted: started},
				}); err != nil {
					kill()
					return err
				}
			case "completed":
				argsJSON := "{}"
				if payload != nil {
					b, err := json.Marshal(payload)
					if err != nil {
						kill()
						return fmt.Errorf("cursor-cli tool_call args: %w", err)
					}
					argsJSON = string(b)
				}
				if err := emit(&llmv1.ConversationStreamFrame{
					SchemaVersion: conversationevents.SchemaVersion,
					Frame: &llmv1.ConversationStreamFrame_ToolCallStreamFinished{
						ToolCallStreamFinished: &llmv1.EvToolCallStreamFinished{
							TurnId: turnID, Id: callID, Name: name, ArgumentsJson: argsJSON,
						},
					},
				}); err != nil {
					kill()
					return err
				}
				// Capture tool call for SQLite persistence.
				// Cursor CLI does not expose tool results separately, so only the
				// call (name + args) is recorded; no tool_result row is added.
				if sink := toolcapture.FromContext(ctx); sink != nil && name != "" && callID != "" {
					sink.Add(&llmv1.Message{
						Role: "assistant",
						ToolCalls: []*llmv1.ToolCall{{
							Id:            callID,
							Name:          name,
							ArgumentsJson: argsJSON,
						}},
					})
				}
			}
		case "result":
			if err := json.Unmarshal(line, &jr); err != nil {
				// Otherwise this surfaces only as "no result object" with no cause.
				llmdlog.Stdout("cursor-cli", llmdlog.LevelWarn, "cursor-cli: unparseable result line: %v", err)
				continue
			}
			if fn := backendSessionSinkFromContext(ctx); fn != nil {
				if rs := strings.TrimSpace(jr.SessionID); rs != "" {
					fn(rs)
				}
			}
			sawResult = true
			break lines
		}
	}
	drainStreamJSONLines(reader)
	if streamErr != nil {
		kill()
		return streamErr
	}
	if !sawResult {
		kill()
		return fmt.Errorf("cursor-cli: no result object in CLI stream%s", stderr.suffix())
	}
	if jr.IsError || (jr.Subtype != "" && jr.Subtype != "success") {
		kill()
		return fmt.Errorf("cursor-cli: %s", strings.TrimSpace(jr.Result))
	}

	visPipe.Flush(memHook, emitActivitySnap, emitTextDelta, emitThinkFromAssistant)
	reasonPipe.Flush(memHook, emitActivitySnap, emitThinkingPostPipeline)
	if streamErr != nil {
		kill()
		return streamErr
	}

	finalUsage := &llmv1.TokenUsage{
		InputTokens:      proto.Int64(jr.Usage.InputTokens),
		OutputTokens:     proto.Int64(jr.Usage.OutputTokens),
		CacheReadTokens:  proto.Int64(jr.Usage.CacheReadTokens),
		CacheWriteTokens: proto.Int64(jr.Usage.CacheWriteTokens),
	}
	msgID := strings.TrimSpace(jr.RequestID)
	if msgID == "" {
		msgID = strings.TrimSpace(jr.SessionID)
	}
	if msgID == "" {
		msgID = "cursor-cli"
	}
	content, thinking, finErr := finalizeCursorCLIStreamBodies(jr, visEmitted, thinkEmitted, fullThinking.String(), memHook, sawToolActivity)
	if finErr != nil {
		kill()
		return finErr
	}
	reply := &llmv1.AssistantReply{
		Content:         content,
		ThinkingContent: thinking,
		Id:              msgID,
		FinishReason:    finishReason,
	}
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantTurnFinished{
			AssistantTurnFinished: &llmv1.EvAssistantTurnFinished{
				TurnId:             turnID,
				AssistantMessageId: proto.String(msgID),
				FinishReason:       proto.String(finishReason),
				Usage:              finalUsage,
			},
		},
	}); err != nil {
		kill()
		return err
	}
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_StreamComplete{
			StreamComplete: reply,
		},
	}); err != nil {
		kill()
		return err
	}

	return nil
}
