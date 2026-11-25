// Package texttoolcall detects tool-call markup that a model emitted as plain
// text and either strips it or converts it into structured calls.
//
// Supported markup:
//
//	<tool_call>{"name":"x","arguments":{...}}</tool_call>
//	<function_call>{"name":"x","arguments":{...}}</function_call>
//	<tool_use>{"name":"x","arguments":{...}}</tool_use>
//	<function_call name="x"><parameter name="k">v</parameter></function_call>
//	<invoke name="x"><parameter name="k">v</parameter></invoke>
//	<function_calls> ...invoke blocks... </function_calls>
//
// Only tools in the offered list match. Anything else, including markup that
// names an unknown tool or text that merely mentions angle brackets, is left
// byte for byte as it was.
package texttoolcall

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

// Mode selects what happens to matched markup.
type Mode int

const (
	// ModeUnspecified behaves exactly like ModeStrip.
	ModeUnspecified Mode = iota
	// ModeStrip removes matched markup from the text.
	ModeStrip
	// ModeConvert removes matched markup and returns it as ParsedCalls. A
	// block that cannot be parsed is stripped and the failure is recorded in
	// its Detection.
	ModeConvert
)

// ParsedCall is a structured tool call converted from text markup.
type ParsedCall struct {
	Name string
	// Arguments is a JSON object encoded as a string ("{}" when empty).
	Arguments string
}

// Detection records one matched markup block.
type Detection struct {
	// Name is the tool name in the markup.
	Name string
	// Raw is the exact markup text that was matched.
	Raw string
	// Converted is true when the block became a ParsedCall.
	Converted bool
	// Error describes why a CONVERT-mode parse failed (empty otherwise).
	Error string
}

var (
	openRe    = regexp.MustCompile(`<(tool_call|function_call|tool_use|invoke)(?:\s+name\s*=\s*"([^"]*)")?\s*>`)
	paramRe   = regexp.MustCompile(`(?s)<parameter\s+name\s*=\s*"([^"]*)"\s*>(.*?)</parameter>`)
	wrapperRe = regexp.MustCompile(`(?s)<function_calls>(.*?)</function_calls>`)
)

// Process scans text for tool-call markup naming a tool in offered. It returns
// the cleaned text, the converted calls (CONVERT mode only) and one Detection
// per matched block. Text outside matched blocks is never modified.
func Process(text string, offered []string, mode Mode) (cleaned string, calls []ParsedCall, detections []Detection) {
	allowed := make(map[string]bool, len(offered))
	for _, n := range offered {
		allowed[n] = true
	}
	if len(allowed) == 0 {
		return text, nil, nil
	}

	// Handle <function_calls> wrappers first: process the inner content, and
	// drop the wrapper too when only whitespace remains inside.
	var sb strings.Builder
	prev := 0
	for _, m := range wrapperRe.FindAllStringSubmatchIndex(text, -1) {
		innerClean, c, d := processBlocks(text[m[2]:m[3]], allowed, mode)
		if len(d) == 0 {
			continue
		}
		sb.WriteString(text[prev:m[0]])
		if strings.TrimSpace(innerClean) != "" {
			sb.WriteString(text[m[0]:m[2]])
			sb.WriteString(innerClean)
			sb.WriteString(text[m[3]:m[1]])
		}
		prev = m[1]
		calls = append(calls, c...)
		detections = append(detections, d...)
	}
	if len(detections) > 0 {
		sb.WriteString(text[prev:])
		text = sb.String()
	}

	// Remaining blocks outside wrappers. Offered blocks inside wrappers are
	// already gone; unknown ones are not offered, so they stay.
	out, c, d := processBlocks(text, allowed, mode)
	calls = append(calls, c...)
	detections = append(detections, d...)
	return out, calls, detections
}

func processBlocks(text string, allowed map[string]bool, mode Mode) (string, []ParsedCall, []Detection) {
	var (
		sb    strings.Builder
		calls []ParsedCall
		dets  []Detection
	)
	pos := 0 // start of text not yet copied
	scan := 0
	for scan < len(text) {
		m := openRe.FindStringSubmatchIndex(text[scan:])
		if m == nil {
			break
		}
		start := scan + m[0]
		bodyStart := scan + m[1]
		tag := text[scan+m[2] : scan+m[3]]
		attrName := ""
		if m[4] >= 0 {
			attrName = text[scan+m[4] : scan+m[5]]
		}
		closeTag := "</" + tag + ">"
		j := strings.Index(text[bodyStart:], closeTag)
		if j < 0 {
			scan = bodyStart
			continue
		}
		body := text[bodyStart : bodyStart+j]
		end := bodyStart + j + len(closeTag)

		call, err := parseBlock(tag, attrName, body)
		if call.Name == "" || !allowed[call.Name] {
			// Unknown or unidentifiable: leave untouched.
			scan = bodyStart
			continue
		}
		det := Detection{Name: call.Name, Raw: text[start:end]}
		if mode == ModeConvert {
			if err != nil {
				det.Error = err.Error()
			} else {
				det.Converted = true
				calls = append(calls, call)
			}
		}
		dets = append(dets, det)
		sb.WriteString(text[pos:start])
		pos = end
		scan = end
	}
	if len(dets) == 0 {
		return text, nil, nil
	}
	sb.WriteString(text[pos:])
	return sb.String(), calls, dets
}

// parseBlock extracts the call. The returned call's Name is set whenever the
// name could be determined, even if err is non-nil.
func parseBlock(tag, attrName, body string) (ParsedCall, error) {
	if attrName != "" {
		args, err := paramsToArgs(body)
		return ParsedCall{Name: attrName, Arguments: args}, err
	}
	if tag == "invoke" {
		return ParsedCall{}, fmt.Errorf("invoke block missing name")
	}
	var raw struct {
		Name       string          `json:"name"`
		Arguments  json.RawMessage `json:"arguments"`
		Parameters json.RawMessage `json:"parameters"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(body)), &raw); err != nil {
		return ParsedCall{}, fmt.Errorf("invalid tool call JSON: %w", err)
	}
	call := ParsedCall{Name: raw.Name, Arguments: "{}"}
	args := raw.Arguments
	if len(args) == 0 {
		args = raw.Parameters
	}
	if len(args) == 0 || string(args) == "null" {
		return call, nil
	}
	// Some models encode arguments as a JSON string containing an object.
	var s string
	if json.Unmarshal(args, &s) == nil {
		if !json.Valid([]byte(s)) {
			return call, fmt.Errorf("tool call %q arguments string is not valid JSON", raw.Name)
		}
		call.Arguments = s
		return call, nil
	}
	call.Arguments = string(args)
	return call, nil
}

// paramsToArgs builds a JSON object from <parameter> elements, or accepts a
// JSON object body when there are none. Each value is parsed as JSON when
// valid and kept as a string otherwise.
func paramsToArgs(body string) (string, error) {
	matches := paramRe.FindAllStringSubmatch(body, -1)
	if len(matches) == 0 {
		trimmed := strings.TrimSpace(body)
		if trimmed == "" {
			return "{}", nil
		}
		var obj map[string]json.RawMessage
		if err := json.Unmarshal([]byte(trimmed), &obj); err != nil {
			return "{}", fmt.Errorf("invalid arguments body: %w", err)
		}
		return trimmed, nil
	}
	obj := make(map[string]json.RawMessage, len(matches))
	for _, m := range matches {
		val := m[2]
		if t := strings.TrimSpace(val); json.Valid([]byte(t)) {
			obj[m[1]] = json.RawMessage(t)
			continue
		}
		b, err := json.Marshal(val)
		if err != nil {
			return "{}", fmt.Errorf("encode parameter %q: %w", m[1], err)
		}
		obj[m[1]] = b
	}
	b, err := json.Marshal(obj)
	if err != nil {
		return "{}", fmt.Errorf("encode arguments: %w", err)
	}
	return string(b), nil
}
