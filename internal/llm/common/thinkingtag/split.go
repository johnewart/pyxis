// Package thinkingtag splits a single model text stream on literal <thinking>...</thinking>
// boundaries, handling tags split across chunks. Outside tags maps to user-visible text;
// inside tags maps to thinking/reasoning. Nested <thinking> inside a thinking region is
// treated as literal characters until the first closing </thinking>.
package thinkingtag

import (
	"bytes"
)

const openTag = "<thinking>"
const closeTag = "</thinking>"

// Splitter parses streaming chunks and invokes emitText / emitThinking for non-empty segments.
type Splitter struct {
	inThinking bool
	carry      []byte
}

// Reset clears parser state for a new assistant turn.
func (s *Splitter) Reset() {
	s.inThinking = false
	s.carry = nil
}

// Feed appends chunk and emits deltas. Call Flush when the stream ends.
func (s *Splitter) Feed(chunk string, emitText, emitThinking func(delta string)) {
	if chunk == "" {
		return
	}
	s.carry = append(s.carry, chunk...)
	for len(s.carry) > 0 {
		if !s.inThinking {
			idx := bytes.Index(s.carry, []byte(openTag))
			if idx >= 0 {
				if idx > 0 {
					emitText(string(s.carry[:idx]))
				}
				s.carry = s.carry[idx+len(openTag):]
				s.inThinking = true
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
			if j > 0 {
				emitThinking(string(s.carry[:j]))
			}
			s.carry = s.carry[j+len(closeTag):]
			s.inThinking = false
			continue
		}
		keep := longestIncompleteTagSuffix(s.carry, []byte(closeTag))
		if keep > 0 {
			emitSafePrefix(&s.carry, keep, emitThinking)
			return
		}
		// Hold unclosed thinking until Flush or more chunks.
		return
	}
}

// Flush releases trailing bytes. Outside mode emits text. Inside an unclosed thinking region,
// pass through the opening tag and buffered bytes as literal visible text instead of routing
// to thinking — incomplete tags are not protocol at end-of-stream.
func (s *Splitter) Flush(emitText, emitThinking func(delta string)) {
	if len(s.carry) == 0 {
		return
	}
	if s.inThinking {
		emitText(openTag + string(s.carry))
	} else {
		emitText(string(s.carry))
	}
	s.carry = nil
	s.inThinking = false
}

func emitSafePrefix(carry *[]byte, keep int, emit func(string)) {
	if len(*carry) <= keep {
		return
	}
	emit(string((*carry)[:len(*carry)-keep]))
	*carry = append([]byte(nil), (*carry)[len(*carry)-keep:]...)
}

// longestIncompleteTagSuffix returns the longest k < len(tag) such that the last k bytes
// of b equal the first k bytes of tag (possible incomplete tag at end of chunk).
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
