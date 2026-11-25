/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 */

package embed_test

import (
	"context"
	"math"
	"testing"

	"github.com/johnewart/pyxis/embed"
)

// cosine is how callers compare these vectors, so it is how the tests judge
// them.
func cosine(a, b []float32) float64 {
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}

func embedOne(t *testing.T, e embed.Embedder, text string) []float32 {
	t.Helper()
	vecs, err := e.Embed(context.Background(), []string{text})
	if err != nil {
		t.Fatalf("embedding %q: %v", text, err)
	}
	if len(vecs) != 1 {
		t.Fatalf("embedding %q returned %d vectors, want 1", text, len(vecs))
	}
	return vecs[0]
}

// One vector per text, in order: a caller matches them back to its own inputs
// by position, so a reordered or short result is silently wrong.
func TestHashingReturnsOneVectorPerTextInOrder(t *testing.T) {
	var e embed.Hashing

	vecs, err := e.Embed(context.Background(), []string{"annual_salary", "home_address", ""})
	if err != nil {
		t.Fatalf("embedding: %v", err)
	}
	if len(vecs) != 3 {
		t.Fatalf("got %d vectors for 3 texts", len(vecs))
	}
	for i, v := range vecs {
		if len(v) != embed.DefaultHashingDimensions {
			t.Fatalf("vector %d is %d wide, want %d", i, len(v), embed.DefaultHashingDimensions)
		}
	}
	if cosine(vecs[0], embedOne(t, e, "annual_salary")) < 0.999 {
		t.Fatal("the same text embedded twice is not the same vector")
	}
}

/*
 * What the stub can do: spelling.
 *
 * The names one column is written with across systems -- snake_case, camelCase,
 * spaced -- fold together, which is the property that makes the stub useful for
 * wiring a pipeline up at all.
 */
func TestHashingFoldsNamingStyles(t *testing.T) {
	var e embed.Hashing

	snake := embedOne(t, e, "annual_salary")
	camel := embedOne(t, e, "annualSalary")
	spaced := embedOne(t, e, "Annual Salary")

	if got := cosine(snake, camel); got < 0.99 {
		t.Fatalf("annual_salary vs annualSalary = %.3f, want them to fold together", got)
	}
	if got := cosine(snake, spaced); got < 0.99 {
		t.Fatalf("annual_salary vs \"Annual Salary\" = %.3f, want them to fold together", got)
	}
}

/*
 * What the stub CANNOT do, pinned so nobody mistakes it for a model.
 *
 * "compensation" and "pay" mean the same thing and share almost no letters, so
 * the stub scores them as strangers. That is the whole gap a trained embedder
 * exists to close, and a measurement taken with this embedder measures the
 * plumbing rather than the idea.
 */
func TestHashingComparesSpellingNotMeaning(t *testing.T) {
	var e embed.Hashing

	if got := cosine(embedOne(t, e, "compensation"), embedOne(t, e, "pay")); got > 0.3 {
		t.Fatalf("compensation vs pay = %.3f: this stub is not supposed to know they are synonyms", got)
	}
}

// An empty text is the zero vector, not an error: comparing it scores zero
// against everything, which is the truthful answer for a name nobody wrote.
func TestHashingEmbedsEmptyTextAsZero(t *testing.T) {
	var e embed.Hashing

	for _, v := range embedOne(t, e, "   ") {
		if v != 0 {
			t.Fatal("an empty text produced a non-zero vector")
		}
	}
}

func TestHashingHonoursItsWidth(t *testing.T) {
	e := embed.Hashing{Dimensions: 64}

	if got := len(embedOne(t, e, "annual_salary")); got != 64 {
		t.Fatalf("vector is %d wide, want 64", got)
	}
	if got := embed.HashingModel(64).Dimensions; got != 64 {
		t.Fatalf("HashingModel(64).Dimensions = %d, want 64", got)
	}
}

// The stub says which "model" it is, so a score taken with it is never read as
// one taken with a real model.
func TestHashingModelNamesItselfAndIsNotFetchable(t *testing.T) {
	m := embed.HashingModel(0)

	if m.ID != embed.HashingName {
		t.Fatalf("HashingModel().ID = %q, want %q", m.ID, embed.HashingName)
	}
	if m.Fetchable() {
		t.Fatal("the stub reports itself fetchable; there is nothing to fetch")
	}
}

/*
 * A model with a URL and no digest is NOT fetchable.
 *
 * A model file is executable input to a runtime. Fetching one nothing can
 * verify means loading whatever arrived, so the honest answer is to refuse
 * rather than to trust the URL.
 */
func TestAModelWithNoDigestIsNotFetchable(t *testing.T) {
	unverifiable := embed.Model{ID: "x", Dimensions: 384, URL: "https://example.invalid/model.onnx"}
	if unverifiable.Fetchable() {
		t.Fatal("a model with no digest reports itself fetchable")
	}

	verifiable := unverifiable
	verifiable.SHA256 = "0000000000000000000000000000000000000000000000000000000000000000"
	if !verifiable.Fetchable() {
		t.Fatal("a model with a URL and a digest does not report itself fetchable")
	}
}

// The interface is what a host holds, so the stub has to satisfy it.
var _ embed.Embedder = embed.Hashing{}
