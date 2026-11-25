package server

import (
	"context"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/llm/clidiscovery"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// providerLogModule tags provider registration/update/discovery log lines, so a
// whole "add a provider" attempt can be followed with `grep llm-providers`.
const providerLogModule = "llm-providers"

// claudeDiscoveryKind is the discovery kind that currently has a full
// connection/auth probe behind it (see CheckProviderConnection).
const claudeDiscoveryKind = clidiscovery.KindClaude

// discoveryKindFor maps a provider type to the CLI discovery knows how to find.
// Provider types with no CLI behind them have nothing to scan for.
func discoveryKindFor(t llmv1.LlmProviderType) (clidiscovery.Kind, bool) {
	switch t {
	case llmv1.LlmProviderType_LLM_PROVIDER_TYPE_CLAUDE_CODE,
		llmv1.LlmProviderType_LLM_PROVIDER_TYPE_CLAUDE_CODE_V2:
		return clidiscovery.KindClaude, true
	case llmv1.LlmProviderType_LLM_PROVIDER_TYPE_CURSOR_CLI:
		return clidiscovery.KindCursor, true
	default:
		return "", false
	}
}

// DiscoverProviderBinaries scans this machine for a CLI provider's binary. See
// DiscoverProviderBinariesRequest in llm.proto for why the scan has to happen
// server-side rather than in whatever is driving the UI.
//
// Finding nothing is not an error: an empty binaries list plus the searched
// locations is a complete, useful answer, and modelling it as a gRPC error would
// force clients to dig a list out of an error string.
func (s *LlmServer) DiscoverProviderBinaries(ctx context.Context, req *llmv1.DiscoverProviderBinariesRequest) (*llmv1.DiscoverProviderBinariesResponse, error) {
	// See CheckProviderConnection: an unset provider_type gets its own message,
	// because "UNSPECIFIED is not backed by a CLI binary" describes the zero value
	// rather than the mistake that produced it.
	if req.GetProviderType() == llmv1.LlmProviderType_LLM_PROVIDER_TYPE_UNSPECIFIED {
		return nil, status.Error(codes.InvalidArgument,
			"provider_type is required and was not set (got LLM_PROVIDER_TYPE_UNSPECIFIED); "+
				"pass the CLI-backed provider type to scan for, e.g. LLM_PROVIDER_TYPE_CLAUDE_CODE_V2")
	}
	kind, ok := discoveryKindFor(req.GetProviderType())
	if !ok {
		return nil, status.Errorf(codes.InvalidArgument,
			"provider type %s is not backed by a CLI binary, so there is nothing to scan for",
			req.GetProviderType())
	}

	cands, err := clidiscovery.Discover(ctx, kind, req.GetBinary())
	if err != nil {
		return nil, status.Errorf(codes.Internal, "%v", err)
	}

	out := make([]*llmv1.DiscoveredProviderBinary, 0, len(cands))
	for _, c := range cands {
		out = append(out, &llmv1.DiscoveredProviderBinary{
			Path:    c.Path,
			Version: c.Version,
			Source:  string(c.Source),
			Origin:  c.Origin,
			Usable:  c.Usable,
			Error:   c.Error,
		})
	}
	return &llmv1.DiscoverProviderBinariesResponse{
		Binaries: out,
		Searched: clidiscovery.SearchedLocations(kind),
	}, nil
}
