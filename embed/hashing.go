/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 */

package embed

import (
	"context"
	"hash/fnv"
	"math"
	"strings"
)

/*
 * A deterministic embedder that needs no model.
 *
 * It hashes character trigrams into a fixed number of buckets and normalises
 * the result, so two strings that share substrings score closer than two that
 * share none. That is enough to wire a pipeline up, write its tests and watch
 * the plumbing work before a real model exists.
 *
 * It is NOT a semantic model and must not be read as one. It knows that
 * "salary" and "salaries" look alike; it has no idea that "compensation" and
 * "pay" mean the same thing, which is the entire reason anyone wants a trained
 * embedder. Any measurement taken with this embedder measures the plumbing,
 * not the idea -- so a caller that reports scores should say which embedder
 * produced them.
 */

// HashingName is the id a Hashing embedder reports as its model, so a
// measurement taken with it is never mistaken for one taken with a real model.
const HashingName = "hashing-stub"

// DefaultHashingDimensions is the vector width a Hashing embedder uses when
// none is given. It matches the common small-model width, so swapping a real
// model in later does not change the shape of anything stored.
const DefaultHashingDimensions = 384

// Hashing is a deterministic, model-free Embedder. Its zero value works and
// uses DefaultHashingDimensions.
type Hashing struct {
	// Dimensions is the vector width. Zero means
	// DefaultHashingDimensions.
	Dimensions int
}

// HashingModel describes the stub as a Model, for a host that reports which
// model it is using. It is not fetchable: there is nothing to fetch.
func HashingModel(dimensions int) Model {
	if dimensions <= 0 {
		dimensions = DefaultHashingDimensions
	}
	return Model{
		ID:         HashingName,
		Dimensions: dimensions,
		Note:       "deterministic trigram hashing, no model: it compares spelling, not meaning",
	}
}

func (h Hashing) dimensions() int {
	if h.Dimensions <= 0 {
		return DefaultHashingDimensions
	}
	return h.Dimensions
}

// Embed returns one unit vector per text. It never fails, and an empty text
// returns the zero vector rather than an error: a caller comparing vectors
// treats a zero vector as "no similarity to anything", which is the truthful
// answer for a name nobody wrote.
func (h Hashing) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	width := h.dimensions()
	out := make([][]float32, 0, len(texts))
	for _, text := range texts {
		out = append(out, hashVector(text, width))
	}
	return out, nil
}

// hashVector buckets a string's character trigrams and returns the unit
// vector. Lower-cased and with word separators folded to spaces, so
// annual_salary, annualSalary and "annual salary" hash alike -- the names the
// same column is written with in different systems.
func hashVector(text string, width int) []float32 {
	vec := make([]float32, width)
	normalised := normalise(text)
	if normalised == "" {
		return vec
	}
	// Padded so the first and last characters carry as much weight as the
	// middle ones, which is what makes short names comparable at all.
	padded := "  " + normalised + "  "
	runes := []rune(padded)
	for i := 0; i+3 <= len(runes); i++ {
		gram := string(runes[i : i+3])
		h := fnv.New32a()
		// Hash.Write never returns an error; the signature has one to
		// satisfy io.Writer.
		_, _ = h.Write([]byte(gram))
		vec[int(h.Sum32()%uint32(width))]++
	}
	return unit(vec)
}

// normalise lower-cases and turns anything that is not a letter or digit into a
// space, so snake_case, camelCase and spaced names fold together.
func normalise(text string) string {
	var b strings.Builder
	var prevLower bool
	for _, r := range text {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			prevLower = r >= 'a' && r <= 'z'
		case r >= 'A' && r <= 'Z':
			// A capital after a lower-case letter starts a word:
			// annualSalary is two words, not one.
			if prevLower {
				b.WriteByte(' ')
			}
			b.WriteRune(r + ('a' - 'A'))
			prevLower = false
		default:
			b.WriteByte(' ')
			prevLower = false
		}
	}
	return strings.Join(strings.Fields(b.String()), " ")
}

// unit scales a vector to length 1, leaving an all-zero vector alone.
func unit(vec []float32) []float32 {
	var sum float64
	for _, v := range vec {
		sum += float64(v) * float64(v)
	}
	if sum == 0 {
		return vec
	}
	norm := float32(math.Sqrt(sum))
	for i := range vec {
		vec[i] /= norm
	}
	return vec
}
