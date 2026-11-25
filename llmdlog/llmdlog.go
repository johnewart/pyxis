// Package llmdlog writes one JSON object per line to stdout/stderr.
//
// The format is a CONTRACT, not an implementation detail: a managing parent
// process parses these records into its diagnostics stream. That is why this
// package is public in both directions.
//
//   - WRITING: a host that runs alongside llmd (or wraps it) emits its own
//     records with Stdout/Stderr/Line, so one reader parses one format. A host
//     hand-rolling an approximation of this shape is how the two silently drift
//     until the parser starts skipping lines.
//   - READING: SetSink intercepts llmd's own records instead of letting them go
//     to the process's stdout. llmdhost.Config.Logger is the supported way in.
package llmdlog

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// Level is a coarse log level for routing on the client side.
type Level string

const (
	LevelDebug Level = "DEBUG"
	LevelInfo  Level = "INFO"
	LevelWarn  Level = "WARN"
	LevelError Level = "ERROR"
)

// Record is the on-the-wire JSON shape for llmd diagnostics.
type Record struct {
	Level     string `json:"level"`
	Component string `json:"component"`
	Module    string `json:"module,omitempty"`
	Timestamp string `json:"timestamp"`
	Message   string `json:"message"`
}

const componentLlmd = "llmd"

// Sink receives every record instead of it being written to stdout/stderr.
//
// This exists for a process that EMBEDS llmd: llmd's diagnostics are otherwise
// written straight to the process's own stdout, where an embedder's logging
// system never sees them and an operator reading that system concludes llmd
// said nothing. A sink routes them wherever the host's logs already go.
//
// The default (no sink) writes the NDJSON lines below to stdout/stderr
// unchanged: that format is a CONTRACT with a managing parent process which
// parses it into its diagnostics stream, so it is not something to "modernise"
// out from under that reader.
type Sink func(Record)

var (
	sinkMu sync.RWMutex
	sink   Sink
)

// SetSink installs the process-wide record sink; nil restores stdout/stderr.
// See llmdhost.Config.Logger for the supported way to set this.
func SetSink(s Sink) {
	sinkMu.Lock()
	defer sinkMu.Unlock()
	sink = s
}

func currentSink() Sink {
	sinkMu.RLock()
	defer sinkMu.RUnlock()
	return sink
}

// Line writes a single JSON log line to w (include trailing newline), or hands
// the record to the installed Sink instead.
func Line(w *os.File, module string, level Level, format string, args ...any) {
	rec := Record{
		Level:     string(level),
		Component: componentLlmd,
		Module:    module,
		Timestamp: time.Now().UTC().Format(time.RFC3339Nano),
		Message:   fmt.Sprintf(format, args...),
	}
	if s := currentSink(); s != nil {
		s(rec)
		return
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return
	}
	b = append(b, '\n')
	_, _ = w.Write(b)
}

// Stdout writes a structured log line to stdout.
func Stdout(module string, level Level, format string, args ...any) {
	Line(os.Stdout, module, level, format, args...)
}

// Stderr writes a structured log line to stderr.
func Stderr(module string, level Level, format string, args ...any) {
	Line(os.Stderr, module, level, format, args...)
}

// Fatalf writes an ERROR line to stderr (or the Sink) and exits with code 1.
//
// It exits the PROCESS, so it is only ever correct in a composition root that
// owns the process. Library code inside llmd returns an error instead; a
// library that calls this takes down its embedder's process over a condition
// that host might have handled.
func Fatalf(module string, format string, args ...any) {
	Stderr(module, LevelError, format, args...)
	os.Exit(1)
}
