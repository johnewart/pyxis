package claudecodeclillm

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/johnewart/pyxis/agentpid"
	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/assistant/conversationevents"
	"github.com/johnewart/pyxis/internal/llm/common"
	"github.com/johnewart/pyxis/internal/llm/common/assistanttext"
	"github.com/johnewart/pyxis/internal/llm/common/memctx"
	"github.com/johnewart/pyxis/internal/llm/common/suffixwire"
	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/protobuf/proto"
)

func claudeThinkingDeltaFromMap(obj map[string]any) string {
	if ev, ok := obj["event"].(map[string]any); ok {
		if delta, ok := ev["delta"].(map[string]any); ok {
			if t, _ := delta["type"].(string); t == "thinking_delta" {
				if s, _ := delta["thinking"].(string); s != "" {
					return s
				}
			}
			if s, _ := delta["text"].(string); s != "" {
				return s
			}
		}
	}
	if s, ok := obj["delta"].(string); ok && s != "" {
		return s
	}
	if s, ok := obj["text"].(string); ok && s != "" {
		return s
	}
	return ""
}

func claudeTextDeltaFromMap(obj map[string]any) string {
	if ev, ok := obj["event"].(map[string]any); ok {
		if delta, ok := ev["delta"].(map[string]any); ok {
			if t, _ := delta["type"].(string); t == "text_delta" {
				if s, _ := delta["text"].(string); s != "" {
					return s
				}
			}
		}
	}
	if s, ok := obj["delta"].(string); ok && s != "" {
		return s
	}
	if s, ok := obj["text"].(string); ok && s != "" {
		return s
	}
	return ""
}

func claudeExtractAssistantPlainText(message any) string {
	if message == nil {
		return ""
	}
	if s, ok := message.(string); ok {
		return s
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
		if t != "text" && t != "output_text" {
			continue
		}
		txt, _ := pm["text"].(string)
		b.WriteString(txt)
	}
	return b.String()
}

type streamSessionResult struct {
	reply *llmv1.AssistantReply
}

// claudeMCPInventoryRetries bounds respawns after a lost MCP-connect race. Contention
// (e.g. multiple agent tabs spawning CLI processes at once on the same machine) makes
// this race more likely, not less, so a single immediate retry can lose twice in a row;
// a couple of extra, backed-off attempts give the race more chances to resolve without
// masking a genuinely broken MCP config forever.
const claudeMCPInventoryRetries = 2

// claudeMCPInventoryRetryBackoff is the pause before each respawn after an empty
// inventory. It is deliberately short: this is working around CLI-internal connect
// timing jitter, not a slow external dependency.
const claudeMCPInventoryRetryBackoff = 400 * time.Millisecond

// runStreamV2 runs one CLI spawn, retrying (bounded, with backoff) when that spawn came
// up without its MCP tools. The inventory gate fires before any frame reaches emit, so
// retries cannot duplicate user-visible output.

func (c *V2Client) runStreamV2(
	ctx context.Context,
	messages []*llmv1.Message,
	opt *llmv1.CallOptions,
	tools []*llmv1.ToolDefinition,
	emit func(*llmv1.ConversationStreamFrame) error,
) (*streamSessionResult, error) {
	out, err := c.runStreamAttemptV2(ctx, messages, opt, tools, emit)
	if !errors.Is(err, errClaudeMCPInventoryEmpty) {
		return out, err
	}
	for attempt := 1; attempt <= claudeMCPInventoryRetries; attempt++ {
		llmdlog.Stdout("claude-code-cli-v2", llmdlog.LevelWarn,
			"mcp inventory gate: %v; respawning (attempt %d/%d)", err, attempt, claudeMCPInventoryRetries)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(claudeMCPInventoryRetryBackoff):
		}
		out, err = c.runStreamAttemptV2(ctx, messages, opt, tools, emit)
		if !errors.Is(err, errClaudeMCPInventoryEmpty) {
			return out, err
		}
	}
	return nil, fmt.Errorf("%w (retried %d times; the llmd MCP server registered no tools for this spawn, "+
		"so the agent would have run without write_code/search)", err, claudeMCPInventoryRetries)
}

func (c *V2Client) runStreamAttemptV2(
	ctx context.Context,
	messages []*llmv1.Message,
	opt *llmv1.CallOptions,
	tools []*llmv1.ToolDefinition,
	emit func(*llmv1.ConversationStreamFrame) error,
) (*streamSessionResult, error) {
	base := &Client{cfg: c.cfg, tools: tools}
	ctx, cancel := base.withTimeout(ctx)
	defer cancel()

	// Callers with no tools (e.g. GenerateOnce's stateless one-shot completions,
	// which never pass a tools slice) have nothing to reach through MCP for, so
	// skip auto-attaching the llmd HTTP MCP server rather than spawn a session
	// that predictably reports an empty tool inventory. An explicit
	// mcp_config_path in config is still honoured even when tools is empty,
	// since that's an operator opt-in rather than an inferred default.
	var mcpPath string
	cleanupMCP := func() {}
	var autoLlmdMCP bool
	if len(tools) > 0 || strings.TrimSpace(c.cfg.McpConfigPath) != "" {
		var mcpErr error
		mcpPath, cleanupMCP, autoLlmdMCP, mcpErr = base.prepareMcpConfigPath(ctx)
		if mcpErr != nil {
			return nil, mcpErr
		}
	}
	defer cleanupMCP()

	// Tools were requested but no MCP bridge resolved to deliver them. Refuse the
	// turn: the model would be told about tools it cannot call, and the inventory
	// gate below only runs when MCP WAS configured, so this case used to pass
	// silently. SkipAutoMCP is an explicit operator opt-out and is honoured.
	if len(tools) > 0 && mcpPath == "" && !autoLlmdMCP && !c.cfg.SkipAutoMCP {
		return nil, fmt.Errorf("claude-code-cli-v2: %d tools requested but no llmd MCP bridge is configured "+
			"(embedded: call llmdhost Host.StartMCP and InstallProcessDefaults; standalone: set LLMD_MCP_HTTP_ADDR "+
			"or LLMD_MCP_HTTP_BASE_URL); refusing to run the turn without its tools", len(tools))
	}

	resumeID := ResumeSessionIDFromContext(ctx)
	userLines, err := messagesToStreamUserLines(messages, resumeID)
	if err != nil {
		return nil, err
	}

	args := c.buildStreamArgsV2(ctx, opt, tools, mcpPath, autoLlmdMCP, systemContentFromMessages(messages))
	cmd, err := base.cmd(ctx, args...)
	if err != nil {
		return nil, err
	}
	// First defer registered, so it runs last: after the process is killed/waited.
	defer releaseNeutralCwd(cmd)
	log := newCLILog("claude-code-cli-v2")
	if bin, berr := base.resolvedBinary(); berr == nil {
		// Record which binary (and version) this spawn actually ran. Both are cheap
		// after the first call and are what identify a stale/pinned CLI when the
		// spawn later comes up with no MCP tools.
		log.bin = bin
		log.version = CachedClaudeCLIVersion(ctx, bin, base.cfg.Cwd)
		log.exec(bin, args, resumeID)
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	defer func() { _ = stdin.Close() }()
	// Own the stdout/stderr pipes (see claudeCLIStdio): cmd.Wait below runs
	// concurrently with reading, and must not close the read ends under us.
	stdio, err := attachClaudeCLIStdio(log, cmd)
	if err != nil {
		return nil, err
	}
	// Registered before the kill/wait defer, so it runs after the process is gone.
	defer stdio.Close()
	stderrSnapshot := stdio.stderrSnapshot
	setClaudeCLIProcessGroup(cmd)

	err = cmd.Start()
	stdio.closeChildEnds()
	if err != nil {
		return nil, fmt.Errorf("claude-code-cli-v2: start: %w", err)
	}
	registerClaudeAgentPID(ctx, cmd)

	kill := func() { killClaudeCLIProcessTree(cmd) }
	procDone := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(procDone)
	}()
	defer func() {
		kill()
		select {
		case <-procDone:
		case <-time.After(5 * time.Second):
		}
	}()

	ctxStop := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			kill()
		case <-ctxStop:
		}
	}()
	defer close(ctxStop)

	reader := bufio.NewReader(stdio.stdout)
	lines := startStreamJSONLineReader(log, reader)
	lineSrc := newStreamJSONLineSource(lines)

	if err := kickClaudeStreamJSONHandshake(ctx, stdin, lineSrc, stderrSnapshot, procDone); err != nil {
		kill()
		return nil, err
	}

	// Do not wait for system/init before user frames: the CLI emits init after stdin
	// traffic (and MCP bootstrap via WaitForMcpServers during the turn). Blocking here
	// deadlocks when init follows the first user message or when MCP is still connecting.
	// The inventory gate below does that wait *after* stdin, which is safe.

	if err := writeStreamUserLines(stdin, userLines); err != nil {
		kill()
		return nil, fmt.Errorf("claude-code-cli-v2: stdin: %w", err)
	}
	// EOF tells the CLI no further user frames are coming for this invocation.
	if err := stdin.Close(); err != nil {
		kill()
		return nil, fmt.Errorf("claude-code-cli-v2: stdin close: %w", err)
	}

	// Never serve a turn whose MCP tools failed to resolve: the model would be told to
	// use tools it cannot see and would fabricate tool calls as prose instead.
	if mcpPath != "" || autoLlmdMCP {
		if err := verifyClaudeMCPInventory(ctx, lineSrc, stderrSnapshot, procDone, c.cfg.DeferToolSchemas, log); err != nil {
			kill()
			return nil, err
		}
	}

	jr, streamErr := c.consumeStreamV2(ctx, lineSrc, procDone, emit, kill)
	if streamErr != nil {
		stderrMsg := strings.TrimSpace(stderrSnapshot())
		if stderrMsg != "" && strings.Contains(streamErr.Error(), "no result") {
			return nil, annotateClaudeAuthError(
				fmt.Errorf("claude-code-cli-v2: %w: %s", streamErr, stderrMsg), stderrMsg)
		}
		return nil, annotateClaudeAuthError(streamErr, stderrMsg)
	}

	memHook := memctx.AssistantMemoryHook(ctx)
	content := jr.content
	thinking := jr.thinking
	if emit == nil {
		content, thinking = assistanttext.ProcessUnary(strings.TrimSpace(jr.result.Result), "", memHook)
	}
	turnID, err := common.RandomTurnID()
	if err != nil {
		return nil, err
	}
	return &streamSessionResult{reply: &llmv1.AssistantReply{
		Content:         content,
		ThinkingContent: thinking,
		Id:              turnID,
		FinishReason:    "stop",
	}}, nil
}

type consumedStream struct {
	result   claudeJSONResult
	content  string
	thinking string
}

func (c *V2Client) consumeStreamV2(
	ctx context.Context,
	lineSrc *streamJSONLineSource,
	procDone <-chan struct{},
	emit func(*llmv1.ConversationStreamFrame) error,
	kill func(),
) (*consumedStream, error) {
	var jr claudeJSONResult
	sawResult := false
	finishReason := "stop"

	if emit == nil {
		for {
			if err := ctx.Err(); err != nil {
				kill()
				return nil, err
			}
			line, err := nextStreamJSONLineFromSource(ctx, lineSrc)
			if err == io.EOF {
				break
			}
			if err != nil {
				kill()
				return nil, fmt.Errorf("claude-code-cli-v2 stream: %w", err)
			}
			if len(line) == 0 {
				continue
			}
			maybeCaptureClaudeBackendSessionID(ctx, line)
			var obj map[string]any
			if json.Unmarshal(line, &obj) != nil {
				continue
			}
			if typ, _ := obj["type"].(string); typ == "result" {
				if err := json.Unmarshal(line, &jr); err != nil {
					// Otherwise this surfaces only as "no result object" with no cause.
					llmdlog.Stdout("claude-code-cli-v2", llmdlog.LevelWarn, "unparseable result line: %v", err)
					continue
				}
				if sink := backendSessionSinkFromContext(ctx); sink != nil {
					if sid := strings.TrimSpace(jr.SessionID); sid != "" {
						sink(sid)
					}
				}
				sawResult = true
				break
			}
		}
		drainStreamJSONLineSource(lineSrc, procDone)
		if !sawResult {
			kill()
			return nil, fmt.Errorf("claude-code-cli-v2: no result object in CLI stream")
		}
		if jr.IsError || (jr.Subtype != "" && jr.Subtype != "success") {
			kill()
			return nil, fmt.Errorf("claude-code-cli-v2: %s", strings.TrimSpace(jr.Result))
		}
		return &consumedStream{result: jr}, nil
	}

	turnID, err := common.RandomTurnID()
	if err != nil {
		kill()
		return nil, err
	}
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantTurnStarted{
			AssistantTurnStarted: &llmv1.EvAssistantTurnStarted{
				TurnId:             turnID,
				ThinkingEnabled:    proto.Bool(true),
				StreamingToolCalls: proto.Bool(false),
			},
		},
	}); err != nil {
		kill()
		return nil, err
	}

	memHook := memctx.AssistantMemoryHook(ctx)
	var (
		visTextRaw   string
		visAsstRaw   string
		visFedRaw    string
		fullThinking strings.Builder
		visEmitted   string
		thinkEmitted string
		visHalted    bool
		thinkHalted  bool
		visPipe      assistanttext.VisiblePipeline
		reasonPipe   assistanttext.ReasoningPipeline
		streamErr    error
	)

	emitTextDelta := func(d string) {
		if d == "" || streamErr != nil || visHalted {
			return
		}
		ne, wire, divergence, regression := suffixwire.Compute(visEmitted, d)
		if regression {
			llmdlog.Stdout("claude-code-cli-v2", llmdlog.LevelWarn, "visible stream regression; emitted=%s chunk=%s",
				suffixwire.Fingerprint(visEmitted), suffixwire.Fingerprint(d))
		}
		if divergence {
			visHalted = true
			return
		}
		if wire == "" {
			visEmitted = ne
			return
		}
		streamErr = emit(&llmv1.ConversationStreamFrame{
			SchemaVersion: conversationevents.SchemaVersion,
			Frame:         &llmv1.ConversationStreamFrame_AssistantTextDelta{AssistantTextDelta: &llmv1.EvAssistantTextDelta{TurnId: turnID, Delta: wire}},
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
			llmdlog.Stdout("claude-code-cli-v2", llmdlog.LevelWarn, "thinking stream regression; emitted=%s chunk=%s",
				suffixwire.Fingerprint(thinkEmitted), suffixwire.Fingerprint(d))
		}
		if divergence {
			thinkHalted = true
			return
		}
		if wire == "" {
			thinkEmitted = ne
			return
		}
		streamErr = emit(&llmv1.ConversationStreamFrame{
			SchemaVersion: conversationevents.SchemaVersion,
			Frame:         &llmv1.ConversationStreamFrame_AssistantThinkingDelta{AssistantThinkingDelta: &llmv1.EvAssistantThinkingDelta{TurnId: turnID, Delta: wire}},
		})
		if streamErr == nil {
			thinkEmitted = ne
		}
	}
	emitActivitySnap := func(headline string) {
		h := strings.TrimSpace(headline)
		if h == "" || streamErr != nil {
			return
		}
		streamErr = emit(&llmv1.ConversationStreamFrame{
			SchemaVersion: conversationevents.SchemaVersion,
			Frame:         &llmv1.ConversationStreamFrame_AssistantActivitySnapshot{AssistantActivitySnapshot: &llmv1.EvAssistantActivitySnapshot{TurnId: turnID, Headline: h}},
		})
	}
	emitThinkingPostPipeline := func(delta string) {
		if delta == "" || streamErr != nil || thinkHalted {
			return
		}
		fullThinking.WriteString(delta)
		emitThinkingDelta(delta)
	}
	reconcileVisible := func() {
		suffix, newFed := reconcileClaudeVisibleSuffix(visFedRaw, canonicalClaudeVisible(visTextRaw, visAsstRaw))
		if suffix == "" {
			return
		}
		visFedRaw = newFed
		visPipe.Feed(suffix, memHook, emitActivitySnap, emitTextDelta, emitThinkingDelta)
	}

	// lastCallInputUsage / lastCallOutputToks track the LAST individual Anthropic
	// API call made by the Claude Code subprocess (via message_start / message_delta
	// stream events). A single Claude Code invocation may make many internal API
	// calls (one per tool-use round); the result-line "usage" field is the
	// cumulative SUM of all of them — which looks like a massive context window
	// even when the actual window is small. We use the per-call figures instead
	// for the context bar, and retain the result-line cost for billing.
	var (
		lastCallInputUsage *llmv1.TokenUsage
		lastCallOutputToks int64
	)

	var resultLine []byte
	for {
		if err := ctx.Err(); err != nil {
			kill()
			return nil, err
		}
		if streamErr != nil {
			kill()
			return nil, streamErr
		}
		line, err := nextStreamJSONLineFromSource(ctx, lineSrc)
		if err == io.EOF {
			break
		}
		if err != nil {
			kill()
			return nil, fmt.Errorf("claude-code-cli-v2 stream: %w", err)
		}
		if len(line) == 0 {
			continue
		}
		maybeCaptureClaudeBackendSessionID(ctx, line)
		var obj map[string]any
		if err := json.Unmarshal(line, &obj); err != nil {
			continue
		}
		typ, _ := obj["type"].(string)
		switch typ {
		case "stream_event":
			if d := claudeThinkingDeltaFromMap(obj); d != "" {
				reasonPipe.Feed(d, memHook, emitActivitySnap, emitThinkingPostPipeline)
			} else if d := claudeTextDeltaFromMap(obj); d != "" {
				visTextRaw += d
				reconcileVisible()
			}
			// Track per-API-call token usage so the context bar shows the actual
			// context window size, not the cumulative sum across all tool rounds.
			if u := extractStreamEventInputUsage(obj); u != nil {
				lastCallInputUsage = u
				lastCallOutputToks = 0 // reset output counter for the new call
			} else if out := extractStreamEventOutputTokens(obj); out > 0 {
				lastCallOutputToks = out
			}
		case "thinking":
			if d := claudeThinkingDeltaFromMap(obj); d != "" {
				reasonPipe.Feed(d, memHook, emitActivitySnap, emitThinkingPostPipeline)
			}
		case "text":
			if d := claudeTextDeltaFromMap(obj); d != "" {
				visTextRaw += d
				reconcileVisible()
			}
		case "assistant":
			msg := obj["message"]
			if msg == nil {
				msg = obj["delta"]
			}
			if full := claudeExtractAssistantPlainText(msg); full != "" {
				visAsstRaw = full
				reconcileVisible()
			}
			// Capture tool_use blocks for persistence in SQLite.
			if sink := ToolCaptureSinkFromContext(ctx); sink != nil {
				captureAssistantMessage(sink, msg)
			}
		case "user":
			// Capture tool_result blocks for persistence in SQLite.
			if sink := ToolCaptureSinkFromContext(ctx); sink != nil {
				captureUserMessage(sink, obj["message"])
			}
		case "result":
			resultLine = append([]byte(nil), line...)
			if err := json.Unmarshal(line, &jr); err != nil {
				// Otherwise this surfaces only as "no result object" with no cause.
				llmdlog.Stdout("claude-code-cli-v2", llmdlog.LevelWarn, "unparseable result line: %v", err)
				continue
			}
			if sink := backendSessionSinkFromContext(ctx); sink != nil {
				if sid := strings.TrimSpace(jr.SessionID); sid != "" {
					sink(sid)
				}
			}
			sawResult = true
			goto done
		}
	}
done:
	drainStreamJSONLineSource(lineSrc, procDone)
	visPipe.Flush(memHook, emitActivitySnap, emitTextDelta, emitThinkingDelta)
	reasonPipe.Flush(memHook, emitActivitySnap, emitThinkingPostPipeline)
	if streamErr != nil {
		kill()
		return nil, streamErr
	}
	if !sawResult {
		kill()
		return nil, fmt.Errorf("claude-code-cli-v2: no result object in CLI stream")
	}
	if jr.IsError || (jr.Subtype != "" && jr.Subtype != "success") {
		kill()
		return nil, fmt.Errorf("claude-code-cli-v2: %s", strings.TrimSpace(jr.Result))
	}

	content, thinking, finErr := finalizeClaudeStreamBodies(jr, visEmitted, thinkEmitted, fullThinking.String(), memHook)
	if finErr != nil {
		kill()
		return nil, finErr
	}
	msgID := turnID
	// Prefer per-call usage (last message_start + message_delta) for the context
	// bar — this is the true context-window size for the final API call.
	// Fall back to the cumulative result-line usage for single-call runs or when
	// no stream_event usage was captured. Always pull cost from the result line
	// since that reflects the total billable amount for the whole invocation.
	resultLineUsage := tokenUsageFromClaudeResultLine(resultLine)
	var usage *llmv1.TokenUsage
	if lastCallInputUsage != nil {
		usage = lastCallInputUsage
		if lastCallOutputToks > 0 {
			usage.OutputTokens = proto.Int64(lastCallOutputToks)
		}
		if resultLineUsage != nil && resultLineUsage.TotalCostUsd != nil {
			usage.TotalCostUsd = resultLineUsage.TotalCostUsd
		}
	} else {
		usage = resultLineUsage
	}
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame: &llmv1.ConversationStreamFrame_AssistantTurnFinished{
			AssistantTurnFinished: &llmv1.EvAssistantTurnFinished{
				TurnId: turnID, AssistantMessageId: proto.String(msgID), FinishReason: proto.String(finishReason),
				Usage: usage,
			},
		},
	}); err != nil {
		kill()
		return nil, err
	}
	if err := emit(&llmv1.ConversationStreamFrame{
		SchemaVersion: conversationevents.SchemaVersion,
		Frame:         &llmv1.ConversationStreamFrame_StreamComplete{StreamComplete: &llmv1.AssistantReply{Content: content, ThinkingContent: thinking, Id: msgID, FinishReason: finishReason, Usage: usage}},
	}); err != nil {
		kill()
		return nil, err
	}
	return &consumedStream{result: jr, content: content, thinking: thinking}, nil
}

func registerClaudeAgentPID(ctx context.Context, cmd *exec.Cmd) {
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

func canonicalClaudeVisible(textRaw, asstRaw string) string {
	if strings.TrimSpace(asstRaw) != "" {
		return asstRaw
	}
	return textRaw
}

func reconcileClaudeVisibleSuffix(fedRaw, canonical string) (suffix, newFed string) {
	canonical = strings.TrimSpace(canonical)
	if canonical == "" {
		return "", fedRaw
	}
	if strings.HasPrefix(canonical, fedRaw) {
		return canonical[len(fedRaw):], canonical
	}
	if fedRaw != "" && strings.HasPrefix(fedRaw, canonical) {
		return "", fedRaw
	}
	if fedRaw == "" {
		return canonical, canonical
	}
	return canonical, canonical
}
