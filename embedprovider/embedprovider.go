// Package embedprovider is the seam a host implements to tell llmd where its
// embedding backends live.
//
// llmd's knowledge index needs one thing from a provider registry: given a
// provider id, the typed config for talking to that backend. Source is that one
// method, and these types are the answer shape.
//
// They are deliberately PLAIN: no gorm tags, no column names, no TableName
// methods. llmd persists embedding providers in its own tables, but that schema
// is llmd's business — a host supplying configs from a Kubernetes secret, a
// config file or its own database should not have to satisfy a struct shaped
// like someone else's SQL. Publishing the storage rows would have made llmd's
// table layout part of its public API, which is the opposite of the point.
package embedprovider

import (
	"context"

	"github.com/johnewart/pyxis/tenantscope"
)

// Kind names the embedding backend family. Exactly one of Config's pointer
// fields is set, matching Kind.
type Kind string

const (
	KindOllama       Kind = "ollama"
	KindOpenAICompat Kind = "openai_compat"
	KindBedrock      Kind = "bedrock"
)

// Ollama configures an Ollama embedding endpoint.
type Ollama struct {
	BaseURL    string
	EmbedModel string
	// Dimensions must match what the index was built with; a mismatch is a
	// FailedPrecondition from the knowledge service rather than silently wrong
	// similarity scores.
	Dimensions int
}

// OpenAICompat configures any OpenAI-compatible embedding endpoint.
type OpenAICompat struct {
	BaseURL    string
	APIKey     string
	EmbedModel string
	Dimensions int
}

// Bedrock configures an AWS Bedrock embedding model.
type Bedrock struct {
	Region      string
	Profile     string
	EmbedModel  string
	Dimensions  int
	EndpointURL string
}

// Config is the resolved config for one embedding provider.
type Config struct {
	ProviderID   string
	Kind         Kind
	Ollama       *Ollama
	OpenAICompat *OpenAICompat
	Bedrock      *Bedrock
}

// Source resolves a provider id to its config for one tenant and scope.
//
// A host resolves the provider registered for sc. It should apply the same
// rule llmd's own store does: a provider registered in sc's exact scope wins,
// otherwise the tenant-wide one (ScopeID == ""); never another tenant's.
//
// Returning an error is the honest answer for an unknown or unconfigured
// provider: llmd surfaces it rather than falling back to a default backend,
// because embedding with the wrong model produces an index that looks fine and
// answers badly.
type Source interface {
	Get(ctx context.Context, sc tenantscope.Scope, providerID string) (*Config, error)
}
