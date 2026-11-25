// Package activitytag splits a single model text stream on literal <activity>...</activity>
// boundaries, handling tags split across chunks. Outside tags maps to the outward stream;
// inside tags is user-facing status (not shown in assistant text). Nested <activity inside a
// region is treated as literal until the first closing </activity>.
package activitytag

import (
	"bytes"
	"strings"
)

const openTag = "<activity>"
const closeTag = "</activity>"

// Splitter parses streaming chunks and invokes emitText for outside-tag text and emitActivity
// with the full inner headline when a closing tag completes. Empty trimmed headlines are skipped.
type Splitter struct {
	inActivity bool
	carry      []byte
}

// Reset clears parser state for a new assistant turn.
func (s *Splitter) Reset() {
	s.inActivity = false
	s.carry = nil
}

// Feed appends chunk and emits segments. Call Flush when the stream ends.
func (s *Splitter) Feed(chunk string, emitText func(delta string), emitActivity func(headline string)) {
	if chunk == "" {
		return
	}
	if emitText == nil {
		emitText = func(string) {}
	}
	if emitActivity == nil {
		emitActivity = func(string) {}
	}
	s.carry = append(s.carry, chunk...)
	for len(s.carry) > 0 {
		if !s.inActivity {
			idx := bytes.Index(s.carry, []byte(openTag))
			if idx >= 0 {
				if idx > 0 {
					emitText(string(s.carry[:idx]))
				}
				s.carry = s.carry[idx+len(openTag):]
				s.inActivity = true
				continue
			}
			keep := longestIncompleteTagSuffix(s.carry, []byte(openTag))
			if keep > 0 {
				emitSafePrefix(&s.carry, keep, emitText)
				return
			}
			emitText(string(s.carry))
			s.carry = nil
			return
		}
		j := bytes.Index(s.carry, []byte(closeTag))
		if j >= 0 {
			inner := strings.TrimSpace(string(s.carry[:j]))
			if inner != "" {
				emitActivity(inner)
			}
			s.carry = s.carry[j+len(closeTag):]
			s.inActivity = false
			continue
		}
		// No closing tag yet: buffer inner content until more chunks or Flush.
		return
	}
}

// Flush releases trailing bytes. Outside mode emits text. Inside an unclosed activity region,
// pass through the opening tag and buffered bytes as literal visible text instead of extracting
// a headline — incomplete tags are not protocol at end-of-stream.
func (s *Splitter) Flush(emitText func(delta string), emitActivity func(headline string)) {
	if len(s.carry) == 0 {
		return
	}
	if emitText == nil {
		emitText = func(string) {}
	}
	if emitActivity == nil {
		emitActivity = func(string) {}
	}
	if s.inActivity {
		emitText(openTag + string(s.carry))
	} else {
		emitText(string(s.carry))
	}
	s.carry = nil
	s.inActivity = false
}

// StripComplete processes a full string (non-streaming) and returns text with activity regions removed.
func StripComplete(s string, onActivity func(headline string)) string {
	var sp Splitter
	var out strings.Builder
	emitText := func(o string) {
		if o != "" {
			out.WriteString(o)
		}
	}
	if onActivity == nil {
		onActivity = func(string) {}
	}
	sp.Feed(s, emitText, onActivity)
	sp.Flush(emitText, onActivity)
	return out.String()
}

func emitSafePrefix(carry *[]byte, keep int, emit func(string)) {
	if len(*carry) <= keep {
		return
	}
	emit(string((*carry)[:len(*carry)-keep]))
	*carry = append([]byte(nil), (*carry)[len(*carry)-keep:]...)
}

func longestIncompleteTagSuffix(b, tag []byte) int {
	n := len(b)
	if n == 0 {
		return 0
	}
	lim := len(tag) - 1
	if lim > n {
		lim = n
	}
	for k := lim; k > 0; k-- {
		if bytes.Equal(b[n-k:], tag[:k]) {
			return k
		}
	}
	return 0
}
