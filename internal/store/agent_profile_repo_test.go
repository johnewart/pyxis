package store

import (
	"context"
	"errors"
	"testing"
	"time"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/agentprofile"
	"github.com/johnewart/pyxis/internal/serverutil"
	"gorm.io/gorm"
)

func newAgentProfileRepo(t *testing.T) *AgentProfileRepo {
	t.Helper()
	return NewAgentProfileRepo(NewTestSQLite(t))
}

func sampleAgentProfile(name string) *agentprofile.Profile {
	return &agentprofile.Profile{
		Name:        name,
		Description: "a sample profile",
		SystemPromptSections: []*llmv1.SystemPromptSection{
			{Id: "persona", Content: "You are helpful.", Tags: []string{"base"}},
			{Id: "rules", Content: "Be terse."},
		},
		LlmProviderID: "provider-a",
		DefaultModel:  "model-a",
		MemoryEnabled: true,
		ToolMapping: []agentprofile.ToolMapping{
			{ToolName: "read_code", Enabled: true},
			{ToolName: "run_command", Enabled: false},
		},
	}
}

func TestAgentProfileToolMappingJSON_RoundTrip(t *testing.T) {
	in := []agentprofile.ToolMapping{
		{ToolName: "read_code", Enabled: true},
		{ToolName: "run_command", Enabled: false},
	}
	s, err := MarshalToolMappingJSON(in)
	if err != nil {
		t.Fatal(err)
	}
	if s == "" {
		t.Fatal("non-empty mapping must not marshal to empty string")
	}
	out, err := UnmarshalToolMappingJSON(s)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0] != in[0] || out[1] != in[1] {
		t.Fatalf("round trip mismatch: %+v vs %+v", out, in)
	}
	if out[1].Enabled {
		t.Fatal("enabled=false must survive the round trip")
	}

	// Empty both directions.
	for _, empty := range [][]agentprofile.ToolMapping{nil, {}} {
		s, err := MarshalToolMappingJSON(empty)
		if err != nil {
			t.Fatal(err)
		}
		if s != "" {
			t.Fatalf("empty mapping must marshal to \"\", got %q", s)
		}
	}
	for _, s := range []string{"", "   ", "\n"} {
		out, err := UnmarshalToolMappingJSON(s)
		if err != nil {
			t.Fatalf("unmarshal %q: %v", s, err)
		}
		if out != nil {
			t.Fatalf("unmarshal %q must yield nil, got %+v", s, out)
		}
	}
	if _, err := UnmarshalToolMappingJSON("{not json"); err == nil {
		t.Fatal("malformed input must error")
	}
}

func TestAgentProfileRepo_RoundTrip(t *testing.T) {
	r := newAgentProfileRepo(t)
	ctx := context.Background()

	p := sampleAgentProfile("zeta")
	if err := r.Create(ctx, serverutil.Scope{TenantID: "ws-a"}, p); err != nil {
		t.Fatal(err)
	}
	if p.ID == "" {
		t.Fatal("Create must assign an id when blank")
	}
	if p.CreatedAt.IsZero() || p.UpdatedAt.IsZero() {
		t.Fatal("Create must set timestamps")
	}

	got, err := r.Get(ctx, serverutil.Scope{TenantID: "ws-a"}, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != p.ID || got.Name != "zeta" || got.Description != p.Description ||
		got.LlmProviderID != "provider-a" || got.DefaultModel != "model-a" || !got.MemoryEnabled {
		t.Fatalf("scalar fields mismatch: %+v", got)
	}
	if got.Static {
		t.Fatal("DB rows must not be static")
	}
	if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatal("timestamps must be non-zero")
	}
	if got.CreatedAt.Location() != time.UTC || got.UpdatedAt.Location() != time.UTC {
		t.Fatalf("timestamps must be UTC: %v %v", got.CreatedAt.Location(), got.UpdatedAt.Location())
	}
	if len(got.ToolMapping) != 2 || got.ToolMapping[0] != p.ToolMapping[0] || got.ToolMapping[1] != p.ToolMapping[1] {
		t.Fatalf("tool mapping mismatch: %+v", got.ToolMapping)
	}
	if len(got.SystemPromptSections) != 2 {
		t.Fatalf("expected 2 sections, got %d", len(got.SystemPromptSections))
	}
	for i, want := range p.SystemPromptSections {
		sec := got.SystemPromptSections[i]
		if sec.GetId() != want.GetId() || sec.GetContent() != want.GetContent() || len(sec.GetTags()) != len(want.GetTags()) {
			t.Fatalf("section %d mismatch: %v vs %v", i, sec, want)
		}
	}

	// List orders by name.
	if err := r.Create(ctx, serverutil.Scope{TenantID: "ws-a"}, sampleAgentProfile("alpha")); err != nil {
		t.Fatal(err)
	}
	if err := r.Create(ctx, serverutil.Scope{TenantID: "ws-a"}, sampleAgentProfile("mid")); err != nil {
		t.Fatal(err)
	}
	list, err := r.List(ctx, serverutil.Scope{TenantID: "ws-a"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 3 || list[0].Name != "alpha" || list[1].Name != "mid" || list[2].Name != "zeta" {
		names := make([]string, 0, len(list))
		for _, x := range list {
			names = append(names, x.Name)
		}
		t.Fatalf("List must order by name, got %v", names)
	}

	// Update replaces every field wholesale and preserves CreatedAt.
	createdAt := got.CreatedAt
	upd := &agentprofile.Profile{
		ID:                   p.ID,
		Name:                 "zeta-renamed",
		Description:          "",
		SystemPromptSections: nil,
		LlmProviderID:        "provider-b",
		DefaultModel:         "",
		MemoryEnabled:        false,
		ToolMapping:          nil,
	}
	if err := r.Update(ctx, serverutil.Scope{TenantID: "ws-a"}, upd); err != nil {
		t.Fatal(err)
	}
	got, err = r.Get(ctx, serverutil.Scope{TenantID: "ws-a"}, p.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "zeta-renamed" || got.Description != "" || got.LlmProviderID != "provider-b" ||
		got.DefaultModel != "" || got.MemoryEnabled || len(got.ToolMapping) != 0 || len(got.SystemPromptSections) != 0 {
		t.Fatalf("Update must replace every field: %+v", got)
	}
	if !got.CreatedAt.Equal(createdAt) {
		t.Fatalf("Update must preserve CreatedAt: %v vs %v", got.CreatedAt, createdAt)
	}
	if got.UpdatedAt.Before(createdAt) {
		t.Fatalf("UpdatedAt %v must not precede CreatedAt %v", got.UpdatedAt, createdAt)
	}

	// Delete then not-found.
	ok, err := r.Delete(ctx, serverutil.Scope{TenantID: "ws-a"}, p.ID)
	if err != nil || !ok {
		t.Fatalf("first delete: ok=%v err=%v", ok, err)
	}
	if _, err := r.Get(ctx, serverutil.Scope{TenantID: "ws-a"}, p.ID); !errors.Is(err, agentprofile.ErrNotFound) {
		t.Fatalf("Get after delete must be ErrNotFound, got %v", err)
	}
	ok, err = r.Delete(ctx, serverutil.Scope{TenantID: "ws-a"}, p.ID)
	if err != nil || ok {
		t.Fatalf("second delete: ok=%v err=%v", ok, err)
	}
}

func TestAgentProfileRepo_WorkspaceIsolation(t *testing.T) {
	r := newAgentProfileRepo(t)
	ctx := context.Background()

	p := sampleAgentProfile("shared-name")
	if err := r.Create(ctx, serverutil.Scope{TenantID: "ws-a"}, p); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Get(ctx, serverutil.Scope{TenantID: "ws-b"}, p.ID); !errors.Is(err, agentprofile.ErrNotFound) {
		t.Fatalf("Get across workspaces must be ErrNotFound, got %v", err)
	}
	list, err := r.List(ctx, serverutil.Scope{TenantID: "ws-b"})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("List in another workspace must be empty, got %d", len(list))
	}
	if ok, err := r.Delete(ctx, serverutil.Scope{TenantID: "ws-b"}, p.ID); err != nil || ok {
		t.Fatalf("Delete across workspaces must not match: ok=%v err=%v", ok, err)
	}
	if err := r.Update(ctx, serverutil.Scope{TenantID: "ws-b"}, &agentprofile.Profile{ID: p.ID, Name: "x"}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("Update across workspaces must be ErrRecordNotFound, got %v", err)
	}
}

func TestAgentProfileRepo_DuplicateName(t *testing.T) {
	r := newAgentProfileRepo(t)
	ctx := context.Background()

	if err := r.Create(ctx, serverutil.Scope{TenantID: "ws-a"}, sampleAgentProfile("dup")); err != nil {
		t.Fatal(err)
	}
	if err := r.Create(ctx, serverutil.Scope{TenantID: "ws-a"}, sampleAgentProfile("dup")); !errors.Is(err, ErrAgentProfileNameTaken) {
		t.Fatalf("second create with same name must be ErrAgentProfileNameTaken, got %v", err)
	}
	// Same name in a different workspace is fine.
	if err := r.Create(ctx, serverutil.Scope{TenantID: "ws-b"}, sampleAgentProfile("dup")); err != nil {
		t.Fatalf("same name in another workspace must succeed: %v", err)
	}

	// Update onto another profile's name collides; onto its own name does not.
	other := sampleAgentProfile("other")
	if err := r.Create(ctx, serverutil.Scope{TenantID: "ws-a"}, other); err != nil {
		t.Fatal(err)
	}
	if err := r.Update(ctx, serverutil.Scope{TenantID: "ws-a"}, &agentprofile.Profile{ID: other.ID, Name: "dup"}); !errors.Is(err, ErrAgentProfileNameTaken) {
		t.Fatalf("update onto taken name must be ErrAgentProfileNameTaken, got %v", err)
	}
	if err := r.Update(ctx, serverutil.Scope{TenantID: "ws-a"}, &agentprofile.Profile{ID: other.ID, Name: "other", Description: "kept name"}); err != nil {
		t.Fatalf("update keeping own name must succeed: %v", err)
	}
}

func TestAgentProfileRepo_UpdateMissing(t *testing.T) {
	r := newAgentProfileRepo(t)
	err := r.Update(context.Background(), serverutil.Scope{TenantID: "ws-a"}, &agentprofile.Profile{ID: "nope", Name: "x"})
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("Update of missing id must be ErrRecordNotFound, got %v", err)
	}
}

func TestAgentProfileRepo_NilRepoErrors(t *testing.T) {
	var r *AgentProfileRepo
	ctx := context.Background()
	if err := r.Create(ctx, serverutil.Scope{TenantID: "ws"}, sampleAgentProfile("x")); err == nil {
		t.Fatal("nil repo Create must error")
	}
	if _, err := r.Get(ctx, serverutil.Scope{TenantID: "ws"}, "id"); err == nil {
		t.Fatal("nil repo Get must error")
	}
	if _, err := r.List(ctx, serverutil.Scope{TenantID: "ws"}); err == nil {
		t.Fatal("nil repo List must error")
	}
	if _, err := r.Delete(ctx, serverutil.Scope{TenantID: "ws"}, "id"); err == nil {
		t.Fatal("nil repo Delete must error")
	}
	if err := r.Update(ctx, serverutil.Scope{TenantID: "ws"}, &agentprofile.Profile{ID: "id", Name: "x"}); err == nil {
		t.Fatal("nil repo Update must error")
	}
}
