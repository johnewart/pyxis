package llmdhost

import (
	"fmt"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/agentprofile"
)

// staticProfileRegistry returns this host's static profile tier: llmd's own
// code-defined profiles (agentprofile.Default) plus the host's. It is a fresh
// registry per Host, so two Hosts in one process never see each other's
// profiles and the process-wide Default is never written.
func staticProfileRegistry(host []*llmv1.AgentProfile) (*agentprofile.Registry, error) {
	reg := agentprofile.NewRegistry()
	builtin := agentprofile.Default.List()
	defs := make([]agentprofile.Profile, 0, len(builtin)+len(host))
	for _, p := range builtin {
		defs = append(defs, *p)
	}
	for i, ap := range host {
		if ap == nil {
			return nil, fmt.Errorf("profile %d is nil", i)
		}
		defs = append(defs, *agentprofile.FromProto(ap))
	}
	if err := reg.Register(defs...); err != nil {
		return nil, err
	}
	return reg, nil
}
