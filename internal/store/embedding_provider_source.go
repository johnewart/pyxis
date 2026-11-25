package store

import (
	"context"

	"github.com/johnewart/pyxis/embedprovider"
	"github.com/johnewart/pyxis/internal/serverutil"
)

// Source adapts this repository to the public embedprovider.Source interface.
//
// The adaptation lives here, on the side that knows both shapes, so that
// neither the public seam depends on llmd's gorm rows nor the knowledge store
// depends on llmd's storage. A host supplying its own Source replaces this
// entirely; llmd's default just reads its own tables.
func (r *EmbeddingProviderRepo) Source() embedprovider.Source {
	return repoSource{repo: r}
}

type repoSource struct{ repo *EmbeddingProviderRepo }

func (s repoSource) Get(ctx context.Context, sc serverutil.Scope, providerID string) (*embedprovider.Config, error) {
	cfg, err := s.repo.Get(ctx, sc, providerID)
	if err != nil {
		return nil, err
	}
	if cfg == nil {
		return nil, nil
	}
	out := &embedprovider.Config{
		ProviderID: cfg.ProviderID,
		Kind:       embedprovider.Kind(cfg.Kind),
	}
	if row := cfg.Ollama; row != nil {
		out.Ollama = &embedprovider.Ollama{
			BaseURL:    row.BaseURL,
			EmbedModel: row.EmbedModel,
			Dimensions: row.Dimensions,
		}
	}
	if row := cfg.OpenAICompat; row != nil {
		out.OpenAICompat = &embedprovider.OpenAICompat{
			BaseURL:    row.BaseURL,
			APIKey:     row.APIKey,
			EmbedModel: row.EmbedModel,
			Dimensions: row.Dimensions,
		}
	}
	if row := cfg.Bedrock; row != nil {
		out.Bedrock = &embedprovider.Bedrock{
			Region:      row.Region,
			Profile:     row.Profile,
			EmbedModel:  row.EmbedModel,
			Dimensions:  row.Dimensions,
			EndpointURL: row.EndpointURL,
		}
	}
	return out, nil
}
