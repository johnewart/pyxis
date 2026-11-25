// Package toolargs bounds tool arguments for display on the event stream.
//
// EvToolExecutionStarted carries the arguments a tool was invoked with so a UI
// can say "Reading lines 15-200 of foo.go" instead of just "read_code". Those
// frames are streamed AND persisted per tool call (SessionEventRepo), so the
// raw arguments cannot go on the wire unbounded: a single write_code call
// carries an entire file body, which would bloat every session's event log for
// a label nobody reads past the first line.
//
// Bounding is per-VALUE, not a prefix clip of the whole payload. Clipping the
// first N bytes loses whichever fields happen to sort late — `{"content": <64KiB>,
// "path": "big.go"}` marshals with content first, so a prefix clip drops the
// one field a label is built from. Eliding oversized values instead keeps every
// key and every small scalar, which is exactly the identifying material
// (paths, line ranges, globs, queries) and never the bulk payload.
//
// The cap is a display budget, not a security boundary. These are the same
// arguments the model already produced and the tool already received; nothing
// is being hidden, only shortened.
package toolargs

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

const (
	// MaxDisplayBytes bounds the whole arguments_json payload.
	MaxDisplayBytes = 2048
	// MaxValueBytes bounds any single field's value. Generous for a path,
	// glob, query or line range; far below a file body.
	MaxValueBytes = 256
)

// ForDisplay returns argsJSON bounded for display, and whether anything was
// shortened. The result stays valid JSON whenever the input was a JSON object,
// so a consumer can parse it and read the fields that survived.
//
// Empty or whitespace-only input yields ("", false): there is nothing to show
// and nothing was shortened, so the field is simply left unset.
func ForDisplay(argsJSON string) (string, bool) {
	if strings.TrimSpace(argsJSON) == "" {
		return "", false
	}
	if len(argsJSON) <= MaxDisplayBytes {
		return argsJSON, false
	}
	if out, ok := elideObject(argsJSON); ok {
		return out, true
	}
	// Not a JSON object (a bare array, a scalar, or malformed). Nothing
	// structural to preserve, so fall back to a prefix clip.
	return clip(argsJSON), true
}

// elideObject shortens oversized values field-by-field, preserving every key.
// Reports false when argsJSON is not a JSON object, leaving the caller to clip.
func elideObject(argsJSON string) (string, bool) {
	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(argsJSON), &fields); err != nil {
		return "", false
	}
	for key, raw := range fields {
		if len(raw) <= MaxValueBytes {
			continue
		}
		fields[key] = elideValue(raw)
	}
	out, err := json.Marshal(fields)
	if err != nil {
		return "", false
	}
	// Still oversized: a very wide object (hundreds of fields) can exceed the
	// budget on keys alone. Drop the largest remaining fields until it fits,
	// so what survives is the small identifying material.
	if len(out) > MaxDisplayBytes {
		out, err = dropLargestUntilFits(fields)
		if err != nil {
			return "", false
		}
	}
	return string(out), true
}

// elideValue replaces one oversized value with a short, honest stand-in.
func elideValue(raw json.RawMessage) json.RawMessage {
	// A long string keeps its head: the first bytes of a path or query are
	// usually the informative part.
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		head := s
		if len(head) > MaxValueBytes {
			head = head[:MaxValueBytes]
			for len(head) > 0 && !utf8.ValidString(head) {
				head = head[:len(head)-1]
			}
		}
		b, err := json.Marshal(head + "…")
		if err != nil {
			return json.RawMessage(`"…"`)
		}
		return b
	}
	// Arrays/objects/other: say how much was dropped rather than showing a
	// meaningless fragment.
	b, err := json.Marshal(fmt.Sprintf("…%d bytes elided…", len(raw)))
	if err != nil {
		return json.RawMessage(`"…"`)
	}
	return b
}

// dropLargestUntilFits removes the biggest fields until the object fits the
// display budget. Deterministic: ties break by key name so the same arguments
// always render the same way.
func dropLargestUntilFits(fields map[string]json.RawMessage) ([]byte, error) {
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		li, lj := len(fields[keys[i]]), len(fields[keys[j]])
		if li != lj {
			return li > lj
		}
		return keys[i] < keys[j]
	})
	for _, k := range keys {
		out, err := json.Marshal(fields)
		if err != nil {
			return nil, err
		}
		if len(out) <= MaxDisplayBytes {
			return out, nil
		}
		delete(fields, k)
	}
	return json.Marshal(fields)
}

// clip is the non-object fallback: a plain prefix cut, trimmed so it never
// ends mid-rune. The frame is a proto string field, and an invalid UTF-8 tail
// fails marshaling outright — which would drop the whole frame and lose the
// tool-started event, not just its arguments.
func clip(s string) string {
	cut := s[:MaxDisplayBytes]
	for len(cut) > 0 && !utf8.ValidString(cut) {
		cut = cut[:len(cut)-1]
	}
	return cut
}
