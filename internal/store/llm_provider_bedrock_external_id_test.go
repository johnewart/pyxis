package store

import (
	"context"
	"testing"

	"github.com/johnewart/pyxis/internal/serverutil"
)

func TestUpsertBedrockExternalIDKeptWhenUnset(t *testing.T) {
	ctx := context.Background()
	repo := NewLlmProviderRepo(NewTestSQLite(t))
	sc := serverutil.Scope{TenantID: "t1"}

	get := func() string {
		t.Helper()
		cfg, err := repo.Get(ctx, sc, "b1")
		if err != nil {
			t.Fatalf("Get: %v", err)
		}
		if cfg.Bedrock == nil {
			t.Fatalf("not a bedrock provider: %+v", cfg)
		}
		return cfg.Bedrock.ExternalID
	}

	if err := repo.UpsertBedrock(ctx, sc, "b1", LlmProviderBedrockRow{Region: "us-east-1", ExternalID: "secret-1"}); err != nil {
		t.Fatalf("initial upsert: %v", err)
	}
	if err := repo.UpsertBedrock(ctx, sc, "b1", LlmProviderBedrockRow{Region: "us-west-2"}); err != nil {
		t.Fatalf("update without external_id: %v", err)
	}
	if got := get(); got != "secret-1" {
		t.Fatalf("external_id after unset update = %q, want secret-1", got)
	}
	if err := repo.UpsertBedrock(ctx, sc, "b1", LlmProviderBedrockRow{Region: "us-west-2", ExternalID: "secret-2"}); err != nil {
		t.Fatalf("update with external_id: %v", err)
	}
	if got := get(); got != "secret-2" {
		t.Fatalf("external_id after replace = %q, want secret-2", got)
	}
}
