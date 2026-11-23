package llmdhost

import (
	"strings"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/agentprofile"
)

func hostProfile(id string) *llmv1.AgentProfile {
	return &llmv1.AgentProfile{
		Id: id, Name: id, Description: "plugin agent",
		SystemPromptSections: []*llmv1.SystemPromptSection{{Id: "workspace", Content: "Do the thing."}},
		ToolMapping:          []*llmv1.ToolMapping{{ToolName: "story_manuscript", Enabled: true}},
	}
}

func TestStaticProfileRegistryHoldsHostProfilesAsStatic(t *testing.T) {
	reg, err := staticProfileRegistry([]*llmv1.AgentProfile{hostProfile("plugin-a"), hostProfile("plugin-b")})
	if err != nil {
		t.Fatalf("staticProfileRegistry: %v", err)
	}
	p, ok := reg.Get("plugin-a")
	if !ok {
		t.Fatal("plugin-a not registered")
	}
	if !p.Static || p.Description != "plugin agent" || len(p.ToolMapping) != 1 || len(p.SystemPromptSections) != 1 {
		t.Errorf("plugin-a = %+v", p)
	}
	// A static id shadows a dynamic profile with the same id.
	if !agentprofile.NewResolver(reg, nil).IsStatic("plugin-b") {
		t.Error("plugin-b is not static")
	}
	// Each host gets its own registry: the process-wide default is untouched.
	if _, ok := agentprofile.Default.Get("plugin-a"); ok {
		t.Error("host profile leaked into agentprofile.Default")
	}
}

func TestStaticProfileRegistryRejectsBadProfiles(t *testing.T) {
	for name, c := range map[string]struct {
		in   []*llmv1.AgentProfile
		want string
	}{
		"duplicate": {[]*llmv1.AgentProfile{hostProfile("x"), hostProfile("x")}, "duplicate"},
		"no name":   {[]*llmv1.AgentProfile{{Id: "y"}}, "name is required"},
		"nil":       {[]*llmv1.AgentProfile{nil}, "nil"},
	} {
		if _, err := staticProfileRegistry(c.in); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: err = %v, want %q", name, err, c.want)
		}
	}
}
