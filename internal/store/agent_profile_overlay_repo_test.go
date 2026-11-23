package store

import (
	"context"
	"testing"

	llmv1 "github.com/johnewart/pyxis/gen/llmv1"
	"github.com/johnewart/pyxis/internal/serverutil"
	"google.golang.org/protobuf/proto"
)

func TestAgentProfileOverlayRepoRoundTrip(t *testing.T) {
	repo := NewAgentProfileOverlayRepo(NewTestSQLite(t))
	ctx := context.Background()
	a := serverutil.Scope{TenantID: "ws-a"}
	b := serverutil.Scope{TenantID: "ws-b"}

	if ov, err := repo.Get(ctx, a, "editor"); err != nil || ov != nil {
		t.Fatalf("Get before Set = %v, %v; want nil, nil", ov, err)
	}
	in := &llmv1.AgentProfileOverlay{
		BaseId:        "editor",
		DefaultModel:  proto.String("m"),
		ToolOverrides: []*llmv1.ToolMapping{{ToolName: "x", Enabled: false}},
		ExtraSections: []*llmv1.SystemPromptSection{{Id: "mine", Content: "c"}},
	}
	stored, err := repo.Set(ctx, a, in)
	if err != nil || stored.GetUpdatedAt() == "" {
		t.Fatalf("Set = %v, %v", stored, err)
	}
	got, err := repo.Get(ctx, a, "editor")
	if err != nil || !proto.Equal(got, stored) {
		t.Fatalf("Get = %v, %v; want %v", got, err, stored)
	}
	// Set replaces wholesale.
	if _, err := repo.Set(ctx, a, &llmv1.AgentProfileOverlay{BaseId: "editor", MemoryEnabled: proto.Bool(true)}); err != nil {
		t.Fatal(err)
	}
	got, _ = repo.Get(ctx, a, "editor")
	if got.DefaultModel != nil || len(got.GetToolOverrides()) != 0 || !got.GetMemoryEnabled() {
		t.Errorf("after replace = %v", got)
	}
	// Workspaces are isolated.
	if ov, _ := repo.Get(ctx, b, "editor"); ov != nil {
		t.Errorf("workspace b sees a's overlay: %v", ov)
	}
	if list, err := repo.List(ctx, a); err != nil || len(list) != 1 || list[0].GetBaseId() != "editor" {
		t.Errorf("List = %v, %v", list, err)
	}
	if ok, err := repo.Delete(ctx, a, "editor"); err != nil || !ok {
		t.Fatalf("Delete = %v, %v", ok, err)
	}
	if ov, _ := repo.Get(ctx, a, "editor"); ov != nil {
		t.Errorf("after Delete = %v", ov)
	}
	if _, err := repo.Set(ctx, serverutil.Scope{}, in); err == nil {
		t.Error("Set without workspace succeeded")
	}
}
