package llmdlog

import (
	"os"
	"strings"
)

// The llmd debug switches, in one place.
//
// Every knob is `LLMD_`-prefixed: llmd runs as a managed subprocess of other
// tools, and the unprefixed names this replaced (DEBUG, LLM_DEBUG,
// DEBUG_LLM_TRAFFIC, DEBUG_EMBEDDINGS) collided with whatever the parent
// process happened to export. There is deliberately no fallback to the old
// names: a silently-ignored debug flag is worse than one that visibly does
// nothing, and `llmd --debug-env` (see main) prints what is actually on.
const (
	// EnvDebug gates general diagnostics: provider scans, MCP scope, CLI
	// subprocess lifecycle, the auto debug-HTTP listener.
	EnvDebug = "LLMD_DEBUG"
	// EnvDebugChat gates chat diagnostics: LlmService RPC bodies, call
	// options, assembled system prompts, per-turn tool surfaces.
	EnvDebugChat = "LLMD_DEBUG_CHAT"
	// EnvDebugChatFile is a path: when set, every chat debug record is also
	// written there as NDJSON (one JSON object per line). Setting it turns
	// chat capture on by itself — asking for the file is asking for the data.
	EnvDebugChatFile = "LLMD_DEBUG_CHAT_FILE"
	// EnvDebugTraffic gates the per-chunk provider firehose (every stream
	// frame, every gRPC Send/Recv body). Separate from EnvDebugChat so the
	// chat lifecycle can be read without drowning in deltas.
	EnvDebugTraffic = "LLMD_DEBUG_TRAFFIC"
	// EnvDebugEmbeddings gates per-call embedding logging.
	EnvDebugEmbeddings = "LLMD_DEBUG_EMBEDDINGS"
)

// envFlag reports whether name is set to a truthy value. Absent means off: a
// debug firehose must be switched on deliberately, never by omission.
func envFlag(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// DebugEnabled reports whether LLMD_DEBUG is on.
func DebugEnabled() bool { return envFlag(EnvDebug) }

// ChatDebugEnabled reports whether chat diagnostics are being captured —
// either LLMD_DEBUG_CHAT is on, or LLMD_DEBUG_CHAT_FILE names a file to write
// them to. A caller gating an expensive dump (protojson, prompt assembly)
// must check this, not the raw env var.
func ChatDebugEnabled() bool {
	return envFlag(EnvDebugChat) || ChatDebugFile() != ""
}

// ChatDebugLogEnabled reports whether chat records go to the process log.
// False when only LLMD_DEBUG_CHAT_FILE is set: that routes them to the file
// alone, keeping the parent process's log readable.
func ChatDebugLogEnabled() bool { return envFlag(EnvDebugChat) }

// ChatDebugFile returns the NDJSON chat log path, or "" when unset.
func ChatDebugFile() string { return strings.TrimSpace(os.Getenv(EnvDebugChatFile)) }

// TrafficDebugEnabled reports whether LLMD_DEBUG_TRAFFIC is on.
func TrafficDebugEnabled() bool { return envFlag(EnvDebugTraffic) }

// EmbeddingDebugEnabled reports whether LLMD_DEBUG_EMBEDDINGS is on.
func EmbeddingDebugEnabled() bool { return envFlag(EnvDebugEmbeddings) }

// DebugSettings is the resolved state of every switch, for the one startup
// line that tells an operator what they actually turned on.
type DebugSettings struct {
	Debug      bool
	Chat       bool
	ChatToLog  bool
	ChatFile   string
	Traffic    bool
	Embeddings bool
}

// ResolveDebugSettings reads every switch once.
func ResolveDebugSettings() DebugSettings {
	return DebugSettings{
		Debug:      DebugEnabled(),
		Chat:       ChatDebugEnabled(),
		ChatToLog:  ChatDebugLogEnabled(),
		ChatFile:   ChatDebugFile(),
		Traffic:    TrafficDebugEnabled(),
		Embeddings: EmbeddingDebugEnabled(),
	}
}

// Any reports whether any debug output at all is enabled.
func (s DebugSettings) Any() bool {
	return s.Debug || s.Chat || s.Traffic || s.Embeddings
}

// Summary renders the enabled switches for a startup log line.
func (s DebugSettings) Summary() string {
	var on []string
	if s.Debug {
		on = append(on, EnvDebug+"=1")
	}
	if s.ChatToLog {
		on = append(on, EnvDebugChat+"=1")
	}
	if s.ChatFile != "" {
		on = append(on, EnvDebugChatFile+"="+s.ChatFile)
	}
	if s.Traffic {
		on = append(on, EnvDebugTraffic+"=1")
	}
	if s.Embeddings {
		on = append(on, EnvDebugEmbeddings+"=1")
	}
	if len(on) == 0 {
		return "none"
	}
	return strings.Join(on, " ")
}
