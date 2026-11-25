package chunk

import (
	"fmt"
	"strings"
	"unicode/utf8"
)

const (
	DefaultChunkSize    = 1024
	DefaultOverlap      = 0
	minChunkSize        = 64
	maxChunkSize        = 256_000
	maxOverlap          = 256_000
)

// Params holds validated chunking parameters.
type Params struct {
	Size    int
	Overlap int
}

// Resolve returns defaults for zero/negative inputs and validates the pair.
func Resolve(chunkSize, overlap int) (Params, error) {
	size := chunkSize
	if size <= 0 {
		size = DefaultChunkSize
	}
	if size < minChunkSize || size > maxChunkSize {
		return Params{}, fmt.Errorf("chunk_size_chars must be between %d and %d", minChunkSize, maxChunkSize)
	}
	o := overlap
	if overlap < 0 {
		o = DefaultOverlap
	}
	if o > maxOverlap {
		return Params{}, fmt.Errorf("chunk_overlap_chars too large (max %d)", maxOverlap)
	}
	if o >= size {
		return Params{}, fmt.Errorf("chunk_overlap_chars must be less than chunk_size_chars")
	}
	return Params{Size: size, Overlap: o}, nil
}

// SingleUTF8 returns one chunk for non-empty trimmed text (prepared/codesearch documents).
func SingleUTF8(text string) []string {
	t := strings.TrimSpace(text)
	if t == "" {
		return nil
	}
	return []string{t}
}

// SplitUTF8 splits text into overlapping character chunks (Unicode code points).
func SplitUTF8(text string, p Params) []string {
	t := strings.TrimSpace(text)
	if t == "" {
		return nil
	}
	if utf8.RuneCountInString(t) <= p.Size {
		return []string{t}
	}
	var out []string
	runes := []rune(t)
	step := p.Size - p.Overlap
	if step <= 0 {
		step = p.Size
	}
	for start := 0; start < len(runes); {
		end := start + p.Size
		if end > len(runes) {
			end = len(runes)
		}
		out = append(out, string(runes[start:end]))
		if end == len(runes) {
			break
		}
		start += step
	}
	return out
}
