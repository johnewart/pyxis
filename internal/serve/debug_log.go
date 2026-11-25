package serve

import (
	"context"
	"fmt"
	"sort"
	"strings"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/grpcauth"
	"github.com/johnewart/pyxis/internal/serverutil"
	"github.com/johnewart/pyxis/internal/toolmerge"
	"github.com/johnewart/pyxis/llmdlog"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

const debugMaxProtoJSON = 16384

// debugMaxSystemPromptBytes caps chat-debug dumps of assembled system prompt text.
const debugMaxSystemPromptBytes = 262144

// DebugEnabled reports whether LLMD_DEBUG is on (verbose non-chat
// diagnostics, e.g. MCP scope logging).
func DebugEnabled() bool {
	return llmdlog.DebugEnabled()
}

// ChatDebugEnabled reports whether chat diagnostics are being captured:
// LLMD_DEBUG_CHAT=1, or LLMD_DEBUG_CHAT_FILE naming an NDJSON file. Guard
// expensive dumps (protojson, prompt assembly) with this.
func ChatDebugEnabled() bool {
	return llmdlog.ChatDebugEnabled()
}

// TrafficDebugEnabled reports whether LLMD_DEBUG_TRAFFIC is on: gates the
// high-volume per-message stream Send/Recv body dumps, kept separate from
// LLMD_DEBUG_CHAT so the stream lifecycle can be logged without the per-chunk
// firehose.
func TrafficDebugEnabled() bool {
	return llmdlog.TrafficDebugEnabled()
}

func isLlmServiceMethod(fullMethod string) bool {
	return strings.HasPrefix(fullMethod, "/llm.v1.LlmService/")
}

// debugLogf writes RPC-level chat diagnostics to the process log.
//
// These are diagnostics ABOUT a chat call — gRPC envelopes, timings, tool
// surface sizes — not the conversation itself. They deliberately do NOT go
// through llmdlog.Chat: the chat NDJSON file is reserved for actual prompts
// and payloads (see internal/llm/chatdebug), and mixing transport noise into
// it would defeat the point of having the file. They are gated on
// LLMD_DEBUG_CHAT=1 specifically, not ChatDebugEnabled, so setting only
// LLMD_DEBUG_CHAT_FILE gives a clean capture with no extra log output.
func debugLogf(format string, args ...any) {
	if !llmdlog.ChatDebugLogEnabled() {
		return
	}
	// NDJSON on stdout so a host's managed-llmd handler can parse and merge into diagnostics.
	llmdlog.Stdout("server", llmdlog.LevelDebug, format, args...)
}

// debugChatf logs an RPC-level diagnostic under a stable event name. The
// event is folded into the message rather than emitted as a chat record, for
// the reason given on debugLogf.
func debugChatf(event, format string, args ...any) {
	if event == "" {
		debugLogf(format, args...)
		return
	}
	debugLogf("["+event+"] "+format, args...)
}

// DebugLogf is the exported form of debugLogf, kept so internal/server (and other
// packages that have not yet moved their own debug logging here) can log
// through the same gated sink without duplicating it.
func DebugLogf(format string, args ...any) {
	debugLogf(format, args...)
}

// DebugChatf is the exported form of debugChatf, for callers that can name
// the event they are recording.
func DebugChatf(event, format string, args ...any) {
	debugChatf(event, format, args...)
}

// DebugChatFields logs an RPC-level diagnostic whose structured fields are
// rendered into the message. Chat PAYLOADS are recorded elsewhere — see
// internal/llm/chatdebug — and are the only thing that reaches the NDJSON
// chat file.
func DebugChatFields(event string, fields map[string]any, format string, args ...any) {
	if !llmdlog.ChatDebugLogEnabled() {
		return
	}
	if len(fields) == 0 {
		debugChatf(event, format, args...)
		return
	}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	b.WriteString(fmt.Sprintf(format, args...))
	for _, k := range keys {
		b.WriteString(fmt.Sprintf(" %s=%v", k, fields[k]))
	}
	debugChatf(event, "%s", b.String())
}

const debugMaxUserAgentRunes = 160

// debugGRPCCallContext returns a short line for DEBUG logs: resolved tenant scope, peer, optional user-agent.
func debugGRPCCallContext(ctx context.Context) string {
	if ctx == nil {
		return "ctx=<nil>"
	}
	var parts []string
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		if sc, err := serverutil.ScopeFromContext(ctx); err == nil {
			parts = append(parts, sc.String())
		} else {
			parts = append(parts, "scope=<unresolved: "+err.Error()+">")
		}
		if v := md.Get("user-agent"); len(v) > 0 && strings.TrimSpace(v[0]) != "" {
			parts = append(parts, "user-agent="+truncateRunes(strings.TrimSpace(v[0]), debugMaxUserAgentRunes))
		}
	} else {
		parts = append(parts, "metadata=<none>")
	}
	if p, ok := peer.FromContext(ctx); ok && p.Addr != nil {
		parts = append(parts, "peer="+p.Addr.String())
	}
	if id := grpcauth.ClientID(ctx); id != "" {
		parts = append(parts, "client="+id)
	}
	return strings.Join(parts, " ")
}

func protoDebugJSON(m proto.Message) string {
	if m == nil {
		return "<nil>"
	}
	b, err := protojson.MarshalOptions{EmitUnpopulated: false}.Marshal(m)
	if err != nil {
		return fmt.Sprintf("<protojson: %v>", err)
	}
	return truncateRunes(string(b), debugMaxProtoJSON)
}

// debugLogCallOptions logs wire vs effective CallOptions for a chat call.
func debugLogCallOptions(rpc, agentID, sessionID string, wire, effective *llmv1.CallOptions) {
	if !llmdlog.ChatDebugLogEnabled() {
		return
	}
	DebugChatFields(
		"call_options",
		map[string]any{
			"rpc":        rpc,
			"agent_id":   agentID,
			"session_id": sessionID,
			"wire":       callOptionsDebugJSON(wire),
			"effective":  callOptionsDebugJSON(effective),
		},
		"%s call options agent_id=%q session_id=%q wire=%s effective=%s",
		rpc,
		agentID,
		sessionID,
		callOptionsDebugJSON(wire),
		callOptionsDebugJSON(effective),
	)
}

// DebugLogCallOptions is the exported form of debugLogCallOptions, kept so
// internal/server can log through the same chat-gated sink without
// duplicating the formatting logic.
func DebugLogCallOptions(rpc, agentID, sessionID string, wire, effective *llmv1.CallOptions) {
	debugLogCallOptions(rpc, agentID, sessionID, wire, effective)
}

func callOptionsDebugJSON(opt *llmv1.CallOptions) string {
	if opt == nil {
		return "<nil>"
	}
	return protoDebugJSON(opt)
}

// TruncateRunes is the exported form of truncateRunes.
func TruncateRunes(s string, maxBytes int) string {
	return truncateRunes(s, maxBytes)
}

func truncateRunes(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	// Prefer not splitting a UTF-8 rune.
	b := []byte(s)
	if len(b) <= maxBytes {
		return s
	}
	b = b[:maxBytes]
	for len(b) > 0 && b[len(b)-1]&0b1100_0000 == 0b1000_0000 {
		b = b[:len(b)-1]
	}
	return string(b) + fmt.Sprintf(" …(truncated, was %d bytes)", len(s))
}

func toolDefNames(defs []*llmv1.ToolDefinition) []string {
	out := make([]string, 0, len(defs))
	for _, d := range defs {
		if d == nil {
			continue
		}
		n := strings.TrimSpace(d.GetName())
		if n != "" {
			out = append(out, n)
		}
	}
	return out
}

// ToolDefNames is the exported form of toolDefNames, kept so internal/server can
// use the same helper without duplicating it.
func ToolDefNames(defs []*llmv1.ToolDefinition) []string {
	return toolDefNames(defs)
}

func mutationToolNamesFiltered(all, kept []*llmv1.ToolDefinition) []string {
	keptSet := make(map[string]struct{}, len(kept))
	for _, d := range kept {
		if d == nil {
			continue
		}
		if n := strings.TrimSpace(d.GetName()); n != "" {
			keptSet[n] = struct{}{}
		}
	}
	var out []string
	for _, d := range all {
		if d == nil {
			continue
		}
		n := strings.TrimSpace(d.GetName())
		if n == "" {
			continue
		}
		if _, ok := keptSet[n]; ok {
			continue
		}
		if toolmerge.IsMutationTool(d) {
			out = append(out, n)
		}
	}
	return out
}

// MutationToolNamesFiltered is the exported form of mutationToolNamesFiltered.
func MutationToolNamesFiltered(all, kept []*llmv1.ToolDefinition) []string {
	return mutationToolNamesFiltered(all, kept)
}

func cursorModeForReadOnly(readOnly bool) string {
	if readOnly {
		return "ask"
	}
	return ""
}

// CursorModeForReadOnly is the exported form of cursorModeForReadOnly.
func CursorModeForReadOnly(readOnly bool) string {
	return cursorModeForReadOnly(readOnly)
}
