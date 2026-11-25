/*
 * Copyright 2026 John Ewart <john@johnewart.net>
 */

// Package embed is the seam for turning short texts into vectors in-process.
//
// llmd already has embedprovider, which configures embedding backends reached
// over HTTP (Ollama, OpenAI-compatible, Bedrock). This is the other half: an
// Embedder a host can hold and call directly, for work that must not leave the
// process or wait on a network hop -- scoring names against descriptions,
// classifying structure, ranking candidates.
//
// The interface is deliberately one method. Everything else in this package
// is support: a model descriptor so a host can say which model it means, and
// a deterministic embedder that needs no model at all, for wiring a pipeline
// up before a real one exists.
package embed

import "context"

// Embedder turns short texts into vectors. One call embeds several texts and
// returns one vector per text, in the same order.
//
// An implementation returns vectors of a single fixed width (see
// Model.Dimensions). Callers compare them with cosine similarity, so an
// implementation should return unit vectors or document that it does not.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
}

// Model describes one embedding model: what to call it, how wide its vectors
// are, where to fetch it and what it should hash to.
//
// Digest is not decoration. A model file is executable input to a runtime, so
// a host that downloads one verifies it against this before loading it, and
// refuses a mismatch rather than running whatever arrived.
type Model struct {
	// ID is the model's name, e.g. "all-MiniLM-L6-v2".
	ID string
	// Dimensions is the width of the vectors it produces. An index or a
	// stored vector set built at one width cannot be compared with another,
	// so a caller checks this rather than discovering it from bad scores.
	Dimensions int
	// URL is where the model file can be fetched from. Empty means the
	// model is not fetchable and must already be present.
	URL string
	// SHA256 is the expected digest of the file at URL, lower-case hex.
	// Empty means the file cannot be verified, which a host should treat as
	// a reason to refuse rather than a reason to trust it.
	SHA256 string
	// SizeBytes is the file's size, so a host can say what a fetch will
	// cost before starting it.
	SizeBytes int64
	// Note is a short line about what the model is for, shown to whoever
	// picks it.
	Note string
}

// Fetchable reports whether this model can be downloaded and verified. A model
// with a URL and no digest is NOT fetchable: fetching it would mean loading a
// file nothing checked.
func (m Model) Fetchable() bool {
	return m.URL != "" && m.SHA256 != ""
}
