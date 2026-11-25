package lexstore

import (
	"sort"
	"strings"
	"unicode"
)

const (
	bm25K1 = 1.2
	bm25B  = 0.75
	// definedTermWeight is how many extra occurrences a chunk's DEFINED
	// names' terms count for: a hit on what a chunk defines beats a mention.
	definedTermWeight = 3
	maxTermLen        = 64
)

// Terms tokenizes text the way the index does: every identifier-ish run
// ([letters digits _]+) yields its lowercased whole form AND its parts split
// on '_', lower->Upper, acronym->Word (HTTPServer -> http, server) and
// letter<->digit boundaries. Terms shorter than 2 runes are dropped.
func Terms(s string) map[string]int {
	out := map[string]int{}
	add := func(t string) {
		if len(t) >= 2 && len(t) <= maxTermLen {
			out[t]++
		}
	}
	var word []rune
	flush := func() {
		if len(word) == 0 {
			return
		}
		whole := strings.ToLower(string(word))
		add(whole)
		if parts := splitIdentifier(word); len(parts) > 1 {
			for _, p := range parts {
				if p != whole {
					add(p)
				}
			}
		}
		word = word[:0]
	}
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
			word = append(word, r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

func splitIdentifier(w []rune) []string {
	var parts []string
	var cur []rune
	emit := func() {
		if len(cur) > 0 {
			parts = append(parts, strings.ToLower(string(cur)))
			cur = cur[:0]
		}
	}
	for i, r := range w {
		if r == '_' {
			emit()
			continue
		}
		if i > 0 && len(cur) > 0 {
			prev := w[i-1]
			switch {
			case unicode.IsLower(prev) && unicode.IsUpper(r):
				emit()
			case unicode.IsUpper(prev) && unicode.IsUpper(r) && i+1 < len(w) && unicode.IsLower(w[i+1]):
				emit()
			case unicode.IsDigit(prev) != unicode.IsDigit(r) && prev != '_':
				emit()
			}
		}
		cur = append(cur, r)
	}
	emit()
	return parts
}

// chunkTerms is the weighted term bag for one chunk: its text, plus extra
// weight on the terms of the names it defines.
func chunkTerms(text string, defines []string) (map[string]int, int) {
	terms := Terms(text)
	for _, d := range defines {
		for t := range Terms(d) {
			terms[t] += definedTermWeight
		}
	}
	length := 0
	for _, n := range terms {
		length += n
	}
	return terms, length
}

func trigramsOf(term string) []string {
	r := []rune(term)
	if len(r) < 3 {
		return nil
	}
	out := make([]string, 0, len(r)-2)
	for i := 0; i+3 <= len(r); i++ {
		out = append(out, string(r[i:i+3]))
	}
	return out
}

// nameTrigrams are the distinct trigrams of every name's terms, sorted.
func nameTrigrams(names ...[]string) []string {
	set := map[string]bool{}
	for _, list := range names {
		for _, n := range list {
			for t := range Terms(n) {
				for _, g := range trigramsOf(t) {
					set[g] = true
				}
			}
		}
	}
	out := make([]string, 0, len(set))
	for g := range set {
		out = append(out, g)
	}
	sort.Strings(out)
	return out
}

// rrfK is reciprocal-rank fusion's damping constant.
const rrfK = 60

// FuseRRF merges ranked lists (each deduped by document) by reciprocal rank
// fusion; the first record seen for a document is kept.
func FuseRRF(lists ...[]SearchHit) []SearchHit {
	type acc struct {
		hit   SearchHit
		score float64
		order int
	}
	by := map[string]*acc{}
	order := 0
	for _, list := range lists {
		for rank, h := range list {
			key := h.LogicalDocumentID + "\x00" + h.SourceID
			a := by[key]
			if a == nil {
				a = &acc{hit: h, order: order}
				order++
				by[key] = a
			}
			a.score += 1 / float64(rrfK+rank+1)
		}
	}
	out := make([]*acc, 0, len(by))
	for _, a := range by {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].score == out[j].score {
			return out[i].order < out[j].order
		}
		return out[i].score > out[j].score
	})
	hits := make([]SearchHit, len(out))
	for i, a := range out {
		a.hit.Score = a.score
		hits[i] = a.hit
	}
	return hits
}
