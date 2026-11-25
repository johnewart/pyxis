// Package memorytag strips literal <memory>...</memory> regions from a streamed string.
// Inside regions are not emitted as visible text; when a closing tag is seen, OnBlock receives
// the trimmed inner text (skipped if empty). Handles tags split across chunks.
package memorytag

import (
	"bytes"
	"strings"
)

const openTag = "<memory>"
const closeTag = "</memory>"

// Splitter removes memory regions from the outward stream and optionally reports each block.
type Splitter struct {
	inMemory bool
	carry    []byte
	blockBuf strings.Builder
	// OnBlock is invoked when a </memory> completes with non-empty trimmed inner text.
	OnBlock func(text string)
}

// Reset clears parser state for a new assistant turn.
func (s *Splitter) Reset() {
	s.inMemory = false
	s.carry = nil
	s.blockBuf.Reset()
}

// Feed parses chunk; emitOutside receives bytes outside memory tags (may be empty).
func (s *Splitter) Feed(chunk string, emitOutside func(outside string)) {
	if chunk == "" {
		return
	}
	s.carry = append(s.carry, chunk...)
	for len(s.carry) > 0 {
		if !s.inMemory {
			idx := bytes.Index(s.carry, []byte(openTag))
			if idx >= 0 {
				if idx > 0 {
					emitOutside(string(s.carry[:idx]))
				}
				s.carry = s.carry[idx+len(openTag):]
				s.inMemory = true
				s.blockBuf.Reset()
				continue
			}
			keep := longestIncompleteTagSuffix(s.carry, []byte(openTag))
			if keep > 0 {
				emitSafePrefix(&s.carry, keep, emitOutside)
				return
			}
			emitOutside(string(s.carry))
			s.carry = nil
			return
		}
		j := bytes.Index(s.carry, []byte(closeTag))
		if j >= 0 {
			if j > 0 {
				s.blockBuf.Write(s.carry[:j])
			}
			s.flushBlock()
			s.carry = s.carry[j+len(closeTag):]
			s.inMemory = false
			continue
		}
		keep := longestIncompleteTagSuffix(s.carry, []byte(closeTag))
		if keep > 0 {
			if len(s.carry) > keep {
				s.blockBuf.Write(s.carry[:len(s.carry)-keep])
			}
			s.carry = append([]byte(nil), s.carry[len(s.carry)-keep:]...)
			return
		}
		s.blockBuf.Write(s.carry)
		s.carry = nil
		return
	}
}

// Flush emits any trailing outside text. Inside an unclosed memory region, pass through
// the opening tag and buffered bytes as literal visible text instead of routing to OnBlock —
// incomplete tags are not protocol at end-of-stream.
func (s *Splitter) Flush(emitOutside func(outside string)) {
	if s.inMemory {
		emitOutside(openTag + s.blockBuf.String() + string(s.carry))
	} else if len(s.carry) > 0 {
		emitOutside(string(s.carry))
	}
	s.carry = nil
	s.blockBuf.Reset()
	s.inMemory = false
}

func (s *Splitter) flushBlock() {
	t := strings.TrimSpace(s.blockBuf.String())
	s.blockBuf.Reset()
	if t != "" && s.OnBlock != nil {
		s.OnBlock(t)
	}
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

// StripComplete processes a full string (non-streaming) and returns text with memory regions removed.
func StripComplete(s string, onBlock func(text string)) string {
	var sp Splitter
	sp.OnBlock = onBlock
	var out strings.Builder
	emit := func(o string) {
		if o != "" {
			out.WriteString(o)
		}
	}
	sp.Feed(s, emit)
	sp.Flush(emit)
	return out.String()
}
