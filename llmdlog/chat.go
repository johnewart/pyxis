package llmdlog

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// The chat debug stream.
//
// Chat records go to two independent sinks: the process log (NDJSON on
// stdout, what a host's managed-subprocess handler already parses) when
// LLMD_DEBUG_CHAT=1, and a dedicated NDJSON file when LLMD_DEBUG_CHAT_FILE is
// set. The file exists because the interesting chat payloads — full prompts,
// tool surfaces, streamed replies — are far too large to leave interleaved in
// a shared log, and because one object per line is directly greppable and
// jq-able after the fact.

// ChatRecord is one chat debug event. It is a superset of Record (same
// level/component/timestamp/message keys, so a reader can treat both streams
// uniformly) plus the chat-specific routing fields.
type ChatRecord struct {
	Level     string         `json:"level"`
	Component string         `json:"component"`
	Stream    string         `json:"stream"`
	Module    string         `json:"module,omitempty"`
	Event     string         `json:"event,omitempty"`
	Timestamp string         `json:"timestamp"`
	Message   string         `json:"message"`
	Fields    map[string]any `json:"fields,omitempty"`
}

const chatStream = "chat"

// Chat writes one chat debug record to whichever sinks are enabled. It is a
// no-op when chat debugging is off, so callers may call it freely — but a
// caller whose ARGUMENTS are expensive to build (protojson, prompt assembly)
// must still guard with ChatDebugEnabled.
func Chat(module, event, format string, args ...any) {
	ChatFields(module, event, nil, format, args...)
}

// ChatFields is Chat with structured fields attached to the record. The
// fields are what makes the NDJSON file worth having over a text log: they
// survive as typed JSON rather than being flattened into the message.
func ChatFields(module, event string, fields map[string]any, format string, args ...any) {
	if !ChatDebugEnabled() {
		return
	}
	rec := ChatRecord{
		Level:     string(LevelDebug),
		Component: componentLlmd,
		Stream:    chatStream,
		Module:    module,
		Event:     event,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Message:   fmt.Sprintf(format, args...),
		Fields:    fields,
	}
	line, err := json.Marshal(rec)
	if err != nil {
		return
	}
	line = append(line, '\n')
	if ChatDebugLogEnabled() {
		_, _ = os.Stdout.Write(line)
	}
	defaultChatFile.write(line)
}

// chatFileSink appends chat records to LLMD_DEBUG_CHAT_FILE. It opens lazily
// on first record and holds the handle for the process lifetime.
//
// A failure here NEVER propagates: debug logging must not be able to take the
// server down, and a chat turn must not fail because a log path was bad. The
// error is reported once on stderr and the sink then stays disabled, rather
// than retrying the open on every single record.
type chatFileSink struct {
	mu       sync.Mutex
	path     string
	file     *os.File
	disabled bool
}

var defaultChatFile = &chatFileSink{}

func (s *chatFileSink) write(line []byte) {
	path := ChatDebugFile()
	if path == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	// The path changed (tests, a re-exec): reopen rather than keep writing to
	// a file nobody asked for any more.
	if s.file != nil && s.path != path {
		_ = s.file.Close()
		s.file = nil
		s.disabled = false
	}
	if s.disabled && s.path == path {
		return
	}
	if s.file == nil {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			s.path = path
			s.disabled = true
			Stderr("chatdebug", LevelWarn,
				"%s=%q could not be opened (%v); chat debug file logging is off for this process",
				EnvDebugChatFile, path, err)
			return
		}
		s.path = path
		s.file = f
		s.disabled = false
	}
	if _, err := s.file.Write(line); err != nil {
		_ = s.file.Close()
		s.file = nil
		s.disabled = true
		Stderr("chatdebug", LevelWarn,
			"writing %s=%q failed (%v); chat debug file logging is off for this process",
			EnvDebugChatFile, path, err)
	}
}

// CloseChatDebugFile closes the chat NDJSON file if one is open. Safe to call
// when none is; safe to call more than once.
func CloseChatDebugFile() {
	defaultChatFile.mu.Lock()
	defer defaultChatFile.mu.Unlock()
	if defaultChatFile.file != nil {
		_ = defaultChatFile.file.Close()
		defaultChatFile.file = nil
	}
	defaultChatFile.disabled = false
}
